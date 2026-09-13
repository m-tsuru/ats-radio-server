package recording

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/m-tsuru/ats-radio-server/internal/device"
	"github.com/m-tsuru/ats-radio-server/internal/scheduler"
)

func TestRecorderStreamsPCMThroughArgumentSafeEncoder(t *testing.T) {
	controlListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer controlListener.Close()
	audioListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer audioListener.Close()
	go mockControl(controlListener)
	go mockAudio(audioListener)

	tempDir := t.TempDir()
	encoderPath := filepath.Join(tempDir, "fake-ffmpeg")
	encoder := "#!/bin/sh\nfor output do :; done\ncat > \"$output\"\n"
	if err := os.WriteFile(encoderPath, []byte(encoder), 0o700); err != nil {
		t.Fatal(err)
	}
	controlPort := listenerPort(t, controlListener)
	audioPort := listenerPort(t, audioListener)
	client := device.NewClient("127.0.0.1", controlPort, audioPort)
	defer client.Close()
	recorder := New(client, tempDir, encoderPath)
	session, err := recorder.Start(context.Background(), Request{
		Station: scheduler.Station{FrequencyHz: 80_000_000, Mode: device.ModeFM},
		Format:  "flac",
	})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	stopCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := session.Stop(stopCtx); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(session.Path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() == 0 {
		t.Fatal("encoder output is empty")
	}
}

func mockControl(listener net.Listener) {
	conn, err := listener.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "HELLO 1" || strings.HasPrefix(line, "TUNE ") || strings.HasPrefix(line, "STREAM ") {
			fmt.Fprintln(conn, "OK")
		} else {
			fmt.Fprintln(conn, "ERR unsupported-command")
		}
	}
}

func mockAudio(listener net.Listener) {
	conn, err := listener.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	fmt.Fprintln(conn, "PCM 16000 16 1 LE")
	samples := make([]byte, 320)
	for {
		if _, err := conn.Write(samples); err != nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func listenerPort(t *testing.T, listener net.Listener) int {
	t.Helper()
	_, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	return port
}
