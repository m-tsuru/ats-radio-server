package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/m-tsuru/ats-radio-server/internal/config"
	"github.com/m-tsuru/ats-radio-server/internal/device"
	"github.com/m-tsuru/ats-radio-server/internal/scheduler"
)

func TestDaemonStatusTuneAndScheduleIPC(t *testing.T) {
	controlListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer controlListener.Close()
	commands := make(chan string, 8)
	go serveMockControl(controlListener, commands)

	tempDir, err := os.MkdirTemp("/tmp", "radiod-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)
	_, portText, _ := net.SplitHostPort(controlListener.Addr().String())
	port, _ := strconv.Atoi(portText)
	cfg := config.Defaults()
	cfg.Device.Host = "127.0.0.1"
	cfg.Device.ControlPort = port
	cfg.Device.AudioPort = port + 1
	cfg.Timezone = "UTC"
	cfg.SocketPath = filepath.Join(tempDir, "radiod.sock")
	cfg.SchedulePath = filepath.Join(tempDir, "schedules.json")
	cfg.RecordingDir = filepath.Join(tempDir, "recordings")

	service, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Serve(ctx) }()
	waitForSocket(t, cfg.SocketPath)

	status := ipcRequest(t, cfg.SocketPath, Request{Command: "status"})
	if !status.OK {
		t.Fatalf("status failed: %s", status.Error)
	}
	if got := <-commands; got != "STATUS" {
		t.Fatalf("got command %q", got)
	}

	tune := ipcRequest(t, cfg.SocketPath, Request{Command: "tune", Mode: device.ModeFM, FrequencyHz: 90_000_000})
	if !tune.OK {
		t.Fatalf("tune failed: %s", tune.Error)
	}
	if got := <-commands; got != "TUNE FM 90000000" {
		t.Fatalf("got command %q", got)
	}

	reservation := scheduler.Reservation{
		ID:      "future",
		Enabled: true,
		Station: scheduler.Station{FrequencyHz: 80_000_000, Mode: device.ModeFM},
		Schedule: scheduler.Rule{
			Type: scheduler.OneShot,
			At:   "2099-01-01T07:00:00Z",
		},
		DurationSeconds: 60,
		Output:          scheduler.Output{Format: "flac"},
	}
	added := ipcRequest(t, cfg.SocketPath, Request{Command: "schedule.add", Schedule: &reservation})
	if !added.OK {
		t.Fatalf("schedule add failed: %s", added.Error)
	}
	listed := ipcRequest(t, cfg.SocketPath, Request{Command: "schedule.list"})
	if !listed.OK {
		t.Fatalf("schedule list failed: %s", listed.Error)
	}
	data, _ := json.Marshal(listed.Data)
	if !strings.Contains(string(data), "future") {
		t.Fatalf("schedule list missing added reservation: %s", data)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("daemon did not stop")
	}
}

func serveMockControl(listener net.Listener, commands chan<- string) {
	conn, err := listener.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	reader := bufio.NewScanner(conn)
	for reader.Scan() {
		line := reader.Text()
		switch {
		case line == "HELLO 1":
			fmt.Fprintln(conn, "OK")
		case line == "STATUS":
			commands <- line
			fmt.Fprintln(conn, `{"state":"receiving","frequency_hz":80000000,"mode":"FM","volume":35,"rssi":42,"snr":27,"battery_mv":3910,"external_power":false,"wifi_rssi_dbm":-54,"ip":"192.0.2.5","time_synced":true,"streaming":false}`)
		case strings.HasPrefix(line, "TUNE "):
			commands <- line
			fmt.Fprintln(conn, "OK")
		default:
			fmt.Fprintln(conn, "ERR unsupported-command")
		}
	}
}

func waitForSocket(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if info, err := os.Stat(path); err == nil && info.Mode()&os.ModeSocket != 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("socket %s was not created", path)
}

func ipcRequest(t *testing.T, path string, request Request) Response {
	t.Helper()
	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(request); err != nil {
		t.Fatal(err)
	}
	var response Response
	if err := json.NewDecoder(conn).Decode(&response); err != nil {
		t.Fatal(err)
	}
	return response
}
