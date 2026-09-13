package recording

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/m-tsuru/ats-radio-server/internal/device"
	"github.com/m-tsuru/ats-radio-server/internal/scheduler"
)

type RadioDevice interface {
	Tune(context.Context, device.Mode, uint64) error
	SetStream(context.Context, bool) error
	OpenAudio(context.Context) (*device.AudioStream, error)
}

type Request struct {
	Station scheduler.Station
	Format  string
	Prefix  string
}

type Recorder struct {
	device    RadioDevice
	directory string
	ffmpeg    string

	mu      sync.Mutex
	current *Session
}

func New(device RadioDevice, directory, ffmpeg string) *Recorder {
	return &Recorder{device: device, directory: directory, ffmpeg: ffmpeg}
}

type Session struct {
	Path string

	device RadioDevice
	stream *device.AudioStream
	cmd    *exec.Cmd
	done   chan struct{}

	mu       sync.Mutex
	err      error
	stopping bool
}

func (r *Recorder) Start(ctx context.Context, request Request) (*Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.current != nil {
		return nil, errors.New("recording is already active")
	}
	if request.Format != "flac" && request.Format != "wav" {
		return nil, errors.New("recording format must be flac or wav")
	}
	if err := os.MkdirAll(r.directory, 0o750); err != nil {
		return nil, fmt.Errorf("create recording directory: %w", err)
	}
	if err := r.device.Tune(ctx, request.Station.Mode, request.Station.FrequencyHz); err != nil {
		return nil, fmt.Errorf("tune before recording: %w", err)
	}
	if err := r.device.SetStream(ctx, true); err != nil {
		return nil, fmt.Errorf("start device stream: %w", err)
	}
	stream, err := r.device.OpenAudio(ctx)
	if err != nil {
		_ = r.device.SetStream(context.Background(), false)
		return nil, err
	}
	path, err := nextPath(r.directory, request.Prefix, request.Station, request.Format, time.Now())
	if err != nil {
		stream.Close()
		_ = r.device.SetStream(context.Background(), false)
		return nil, err
	}
	inputFormat, err := pcmInputFormat(stream.Header.Bits)
	if err != nil {
		stream.Close()
		_ = r.device.SetStream(context.Background(), false)
		return nil, err
	}
	codec := "flac"
	if request.Format == "wav" {
		codec = "pcm_s16le"
	}
	cmd := exec.Command(r.ffmpeg,
		"-hide_banner", "-loglevel", "warning", "-nostdin",
		"-f", inputFormat,
		"-ar", strconv.Itoa(stream.Header.SampleRate),
		"-ac", strconv.Itoa(stream.Header.Channels),
		"-i", "pipe:0",
		"-c:a", codec,
		"-n", path,
	)
	cmd.Stdin = stream.Reader
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		stream.Close()
		_ = r.device.SetStream(context.Background(), false)
		return nil, fmt.Errorf("start ffmpeg: %w", err)
	}

	session := &Session{
		Path:   path,
		device: r.device,
		stream: stream,
		cmd:    cmd,
		done:   make(chan struct{}),
	}
	r.current = session
	go func() {
		err := cmd.Wait()
		_ = stream.Close()
		stopCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = r.device.SetStream(stopCtx, false)
		session.mu.Lock()
		if session.stopping && err != nil {
			var exitError *exec.ExitError
			if !errors.As(err, &exitError) {
				err = nil
			}
		} else if err == nil && !session.stopping {
			err = errors.New("audio stream ended unexpectedly")
		}
		session.err = err
		session.mu.Unlock()
		close(session.done)

		r.mu.Lock()
		if r.current == session {
			r.current = nil
		}
		r.mu.Unlock()
	}()
	return session, nil
}

func (r *Recorder) Stop(ctx context.Context) error {
	r.mu.Lock()
	session := r.current
	r.mu.Unlock()
	if session == nil {
		return errors.New("no recording is active")
	}
	return session.Stop(ctx)
}

func (r *Recorder) Current() *Session {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current
}

func (s *Session) Stop(ctx context.Context) error {
	s.mu.Lock()
	s.stopping = true
	s.mu.Unlock()
	_ = s.stream.Close()
	select {
	case <-s.done:
		return s.Err()
	case <-ctx.Done():
		if s.cmd.Process != nil {
			_ = s.cmd.Process.Kill()
		}
		<-s.done
		return fmt.Errorf("stop recording: %w", ctx.Err())
	}
}

func (s *Session) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *Session) Done() <-chan struct{} { return s.done }

func pcmInputFormat(bits int) (string, error) {
	switch bits {
	case 16:
		return "s16le", nil
	case 24:
		return "s24le", nil
	case 32:
		return "s32le", nil
	default:
		return "", fmt.Errorf("unsupported PCM bit depth %d", bits)
	}
}

func nextPath(directory, prefix string, station scheduler.Station, format string, now time.Time) (string, error) {
	if prefix != "" {
		prefix += "_"
	}
	base := fmt.Sprintf("%s%s_%d_%s", prefix, now.Format("2006-01-02_150405"), station.FrequencyHz, station.Mode)
	for suffix := 0; suffix < 1000; suffix++ {
		name := base
		if suffix > 0 {
			name += fmt.Sprintf("_%03d", suffix)
		}
		path := filepath.Join(directory, name+"."+format)
		_, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) {
			return path, nil
		}
		if err != nil {
			return "", fmt.Errorf("check recording filename: %w", err)
		}
	}
	return "", errors.New("could not allocate a collision-free recording filename")
}
