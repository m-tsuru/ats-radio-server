package device

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"testing"
)

func TestClientHandshakeStatusAndTune(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		checks := []struct {
			want  string
			reply string
		}{
			{"HELLO 1\n", "OK\n"},
			{"STATUS\n", `{"state":"receiving","frequency_hz":80000000,"mode":"FM","volume":35,"rssi":42,"snr":27,"battery_mv":3910,"external_power":false,"wifi_rssi_dbm":-54,"ip":"192.0.2.5","time_synced":true,"streaming":false}` + "\n"},
			{"TUNE FM 90000000\n", "OK\n"},
		}
		for _, check := range checks {
			line, err := reader.ReadString('\n')
			if err != nil {
				done <- err
				return
			}
			if line != check.want {
				done <- fmt.Errorf("got command %q, want %q", line, check.want)
				return
			}
			if _, err := io.WriteString(conn, check.reply); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()

	client := NewClient("unused", 1, 2)
	client.controlAddress = listener.Addr().String()
	status, err := client.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.RSSI != 42 || status.FrequencyHz != 80_000_000 {
		t.Fatalf("unexpected status: %+v", status)
	}
	if err := client.Tune(context.Background(), ModeFM, 90_000_000); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestClientReadsPCMHeaderWithoutLosingPayload(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = io.WriteString(conn, "PCM 16000 16 1 LE\nDATA")
	}()

	client := NewClient("unused", 1, 2)
	client.audioAddress = listener.Addr().String()
	stream, err := client.OpenAudio(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	payload, err := io.ReadAll(stream.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != "DATA" {
		t.Fatalf("got payload %q", payload)
	}
}
