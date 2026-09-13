package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/m-tsuru/ats-radio-server/internal/config"
	"github.com/m-tsuru/ats-radio-server/internal/device"
	"github.com/m-tsuru/ats-radio-server/internal/recording"
	"github.com/m-tsuru/ats-radio-server/internal/scheduler"
)

const maxIPCRequestBytes = 64 * 1024

type Request struct {
	Command     string                 `json:"command"`
	Mode        device.Mode            `json:"mode,omitempty"`
	FrequencyHz uint64                 `json:"frequency_hz,omitempty"`
	Volume      int                    `json:"volume,omitempty"`
	Format      string                 `json:"format,omitempty"`
	ID          string                 `json:"id,omitempty"`
	Schedule    *scheduler.Reservation `json:"schedule,omitempty"`
}

type Response struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	Data  any    `json:"data,omitempty"`
}

type Status struct {
	State  string        `json:"daemon_state"`
	Detail string        `json:"owner,omitempty"`
	Device device.Status `json:"device"`
}

type Daemon struct {
	cfg      config.Config
	location *time.Location
	device   *device.Client
	store    *scheduler.Store
	recorder *recording.Recorder

	stateMu     sync.Mutex
	state       string
	detail      string
	ownerCancel context.CancelFunc
}

func New(cfg config.Config) (*Daemon, error) {
	location, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return nil, err
	}
	store, err := scheduler.LoadStore(cfg.SchedulePath, location)
	if err != nil {
		return nil, err
	}
	client := device.NewClient(cfg.Device.Host, cfg.Device.ControlPort, cfg.Device.AudioPort)
	return &Daemon{
		cfg:      cfg,
		location: location,
		device:   client,
		store:    store,
		recorder: recording.New(client, cfg.RecordingDir, cfg.FFmpegPath),
		state:    "idle",
	}, nil
}

func (d *Daemon) Serve(ctx context.Context) error {
	if err := os.MkdirAll(filepath.Dir(d.cfg.SocketPath), 0o750); err != nil {
		return fmt.Errorf("create socket directory: %w", err)
	}
	if info, err := os.Lstat(d.cfg.SocketPath); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return fmt.Errorf("refusing to replace non-socket path %s", d.cfg.SocketPath)
		}
		if err := os.Remove(d.cfg.SocketPath); err != nil {
			return fmt.Errorf("remove stale socket: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	listener, err := net.Listen("unix", d.cfg.SocketPath)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", d.cfg.SocketPath, err)
	}
	defer listener.Close()
	defer os.Remove(d.cfg.SocketPath)
	if err := os.Chmod(d.cfg.SocketPath, 0o660); err != nil {
		return fmt.Errorf("set socket permissions: %w", err)
	}

	go d.runScheduler(ctx)
	go func() {
		<-ctx.Done()
		listener.Close()
	}()
	log.Printf("radiod listening on %s", d.cfg.SocketPath)
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				d.shutdown()
				return nil
			}
			if temporary, ok := err.(interface{ Temporary() bool }); ok && temporary.Temporary() {
				log.Printf("temporary accept error: %v", err)
				continue
			}
			return err
		}
		go d.handleConnection(ctx, conn)
	}
}

func (d *Daemon) handleConnection(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	decoder := json.NewDecoder(io.LimitReader(conn, maxIPCRequestBytes))
	decoder.DisallowUnknownFields()
	var request Request
	if err := decoder.Decode(&request); err != nil {
		d.respond(conn, Response{Error: "invalid request: " + err.Error()})
		return
	}
	_ = conn.SetReadDeadline(time.Time{})
	if request.Command == "listen" {
		d.handleListen(ctx, conn)
		return
	}
	response := d.handleRequest(ctx, request)
	d.respond(conn, response)
}

func (d *Daemon) handleRequest(ctx context.Context, request Request) Response {
	switch request.Command {
	case "status":
		status, err := d.device.Status(ctx)
		if err != nil {
			return failure(err)
		}
		state, detail := d.currentState()
		return Response{OK: true, Data: Status{State: state, Detail: detail, Device: status}}
	case "tune":
		if err := d.withIdle(func() error {
			return d.device.Tune(ctx, request.Mode, request.FrequencyHz)
		}); err != nil {
			return failure(err)
		}
		return Response{OK: true}
	case "volume":
		if err := d.withIdle(func() error {
			return d.device.SetVolume(ctx, request.Volume)
		}); err != nil {
			return failure(err)
		}
		return Response{OK: true}
	case "record.start":
		return d.startManualRecording(ctx, request)
	case "record.stop":
		return d.stopManualRecording(ctx)
	case "schedule.list":
		return Response{OK: true, Data: d.store.List()}
	case "schedule.add":
		if request.Schedule == nil {
			return failure(errors.New("schedule is required"))
		}
		if err := d.store.Add(*request.Schedule); err != nil {
			return failure(err)
		}
		return Response{OK: true}
	case "schedule.remove":
		if err := d.store.Remove(request.ID); err != nil {
			return failure(err)
		}
		return Response{OK: true}
	case "schedule.enable":
		if err := d.store.SetEnabled(request.ID, true); err != nil {
			return failure(err)
		}
		return Response{OK: true}
	case "schedule.disable":
		if err := d.store.SetEnabled(request.ID, false); err != nil {
			return failure(err)
		}
		return Response{OK: true}
	default:
		return failure(fmt.Errorf("unsupported command %q", request.Command))
	}
}

func (d *Daemon) startManualRecording(ctx context.Context, request Request) Response {
	if request.Format == "" {
		request.Format = "flac"
	}
	station := scheduler.Station{FrequencyHz: request.FrequencyHz, Mode: request.Mode}
	if station.FrequencyHz == 0 {
		status, err := d.device.Status(ctx)
		if err != nil {
			return failure(err)
		}
		station = scheduler.Station{FrequencyHz: status.FrequencyHz, Mode: status.Mode}
	}
	ownerCtx, cancel := context.WithCancel(context.Background())
	if err := d.acquire("manual_recording", "", cancel); err != nil {
		cancel()
		return failure(err)
	}
	session, err := d.recorder.Start(ownerCtx, recording.Request{Station: station, Format: request.Format})
	if err != nil {
		d.release("manual_recording", "")
		cancel()
		return failure(err)
	}
	go func() {
		<-session.Done()
		if err := session.Err(); err != nil {
			log.Printf("manual recording ended with error: %v", err)
		}
		d.release("manual_recording", "")
		cancel()
	}()
	return Response{OK: true, Data: map[string]string{"path": session.Path}}
}

func (d *Daemon) stopManualRecording(ctx context.Context) Response {
	state, _ := d.currentState()
	if state != "manual_recording" {
		return failure(errors.New("no manual recording is active"))
	}
	if err := d.recorder.Stop(ctx); err != nil {
		return failure(err)
	}
	return Response{OK: true}
}

func (d *Daemon) handleListen(ctx context.Context, conn net.Conn) {
	listenCtx, cancel := context.WithCancel(ctx)
	if err := d.acquire("listening", "", cancel); err != nil {
		d.respond(conn, failure(err))
		cancel()
		return
	}
	defer func() {
		d.release("listening", "")
		cancel()
	}()
	if err := d.device.SetStream(listenCtx, true); err != nil {
		d.respond(conn, failure(err))
		return
	}
	defer d.device.SetStream(context.Background(), false)
	stream, err := d.device.OpenAudio(listenCtx)
	if err != nil {
		d.respond(conn, failure(err))
		return
	}
	defer stream.Close()
	go func() {
		<-listenCtx.Done()
		stream.Close()
	}()
	if err := json.NewEncoder(conn).Encode(Response{OK: true, Data: stream.Header}); err != nil {
		return
	}
	if _, err := io.Copy(conn, stream.Reader); err != nil && !errors.Is(err, net.ErrClosed) {
		log.Printf("listen stream ended: %v", err)
	}
}

func (d *Daemon) runScheduler(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	last := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			for _, occurrence := range scheduler.DueBetween(d.store.List(), last, now, d.location) {
				go d.runScheduledRecording(ctx, occurrence)
			}
			last = now
		}
	}
}

func (d *Daemon) runScheduledRecording(ctx context.Context, occurrence scheduler.Occurrence) {
	id := occurrence.Reservation.ID
	ownerCtx, cancel := context.WithCancel(ctx)
	if err := d.acquire("scheduled_recording", id, cancel); err != nil {
		cancel()
		log.Printf("schedule %s skipped: %v", id, err)
		return
	}
	defer func() {
		d.release("scheduled_recording", id)
		cancel()
	}()
	session, err := d.recorder.Start(ownerCtx, recording.Request{
		Station: occurrence.Reservation.Station,
		Format:  occurrence.Reservation.Output.Format,
		Prefix:  id,
	})
	if err != nil {
		log.Printf("schedule %s could not start: %v", id, err)
		return
	}
	log.Printf("schedule %s recording to %s", id, session.Path)
	timer := time.NewTimer(time.Until(occurrence.End))
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	case <-session.Done():
		if err := session.Err(); err != nil {
			log.Printf("schedule %s stream failed: %v", id, err)
		}
		return
	}
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer stopCancel()
	if err := session.Stop(stopCtx); err != nil {
		log.Printf("schedule %s stop failed: %v", id, err)
	}
}

func (d *Daemon) acquire(state, detail string, cancel context.CancelFunc) error {
	d.stateMu.Lock()
	defer d.stateMu.Unlock()
	if d.state != "idle" {
		if d.detail != "" {
			return fmt.Errorf("device busy: %s %s", d.state, d.detail)
		}
		return fmt.Errorf("device busy: %s", d.state)
	}
	d.state = state
	d.detail = detail
	d.ownerCancel = cancel
	return nil
}

func (d *Daemon) release(state, detail string) {
	d.stateMu.Lock()
	defer d.stateMu.Unlock()
	if d.state == state && d.detail == detail {
		d.state = "idle"
		d.detail = ""
		d.ownerCancel = nil
	}
}

func (d *Daemon) currentState() (string, string) {
	d.stateMu.Lock()
	defer d.stateMu.Unlock()
	return d.state, d.detail
}

func (d *Daemon) withIdle(operation func() error) error {
	d.stateMu.Lock()
	defer d.stateMu.Unlock()
	if d.state != "idle" {
		if d.detail != "" {
			return fmt.Errorf("device busy: %s %s", d.state, d.detail)
		}
		return fmt.Errorf("device busy: %s", d.state)
	}
	return operation()
}

func (d *Daemon) shutdown() {
	d.stateMu.Lock()
	cancel := d.ownerCancel
	d.stateMu.Unlock()
	if cancel != nil {
		cancel()
	}
	if d.recorder.Current() != nil {
		ctx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if err := d.recorder.Stop(ctx); err != nil {
			log.Printf("recording shutdown error: %v", err)
		}
	}
	if err := d.device.Close(); err != nil {
		log.Printf("device close error: %v", err)
	}
}

func (d *Daemon) respond(w io.Writer, response Response) {
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Printf("IPC response error: %v", err)
	}
}

func failure(err error) Response { return Response{Error: err.Error()} }
