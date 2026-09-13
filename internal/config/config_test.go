package config

import (
	"strings"
	"testing"
)

func TestDecodeConfigAppliesDefaults(t *testing.T) {
	cfg, err := Decode(strings.NewReader(`{
		"device":{"host":"radio.example","control_port":60000,"audio_port":60001},
		"timezone":"UTC",
		"recording_dir":"/tmp/recordings"
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SocketPath != "/run/radiod/radiod.sock" {
		t.Fatalf("unexpected socket default: %q", cfg.SocketPath)
	}
	if cfg.SchedulePath != "/var/lib/radiod/schedules.json" {
		t.Fatalf("unexpected schedule default: %q", cfg.SchedulePath)
	}
}

func TestDecodeConfigRejectsUnknownField(t *testing.T) {
	_, err := Decode(strings.NewReader(`{"unknown":true}`))
	if err == nil {
		t.Fatal("expected unknown field error")
	}
}

func TestDecodeConfigRejectsInvalidTimezone(t *testing.T) {
	_, err := Decode(strings.NewReader(`{"timezone":"Mars/Olympus"}`))
	if err == nil {
		t.Fatal("expected timezone error")
	}
}
