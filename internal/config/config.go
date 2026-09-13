package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

const DefaultPath = "/etc/radiod/config.json"

type Device struct {
	Host        string `json:"host"`
	ControlPort int    `json:"control_port"`
	AudioPort   int    `json:"audio_port"`
}

type Config struct {
	Device       Device `json:"device"`
	Timezone     string `json:"timezone"`
	RecordingDir string `json:"recording_dir"`
	SchedulePath string `json:"schedule_path"`
	SocketPath   string `json:"socket_path"`
	FFmpegPath   string `json:"ffmpeg_path"`
}

func Defaults() Config {
	return Config{
		Device: Device{
			Host:        "ats-radio.local",
			ControlPort: 60000,
			AudioPort:   60001,
		},
		Timezone:     "Asia/Tokyo",
		RecordingDir: "/var/lib/radiod/recordings",
		SchedulePath: "/var/lib/radiod/schedules.json",
		SocketPath:   "/run/radiod/radiod.sock",
		FFmpegPath:   "ffmpeg",
	}
}

func Load(path string) (Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return Config{}, err
	}
	defer f.Close()
	return Decode(f)
}

func Decode(r io.Reader) (Config, error) {
	cfg := Defaults()
	decoder := json.NewDecoder(r)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	if err := ensureEOF(decoder); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func ensureEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("decode config trailer: %w", err)
	}
	return errors.New("decode config: multiple JSON values")
}

func (c Config) Validate() error {
	if c.Device.Host == "" {
		return errors.New("device.host is required")
	}
	if !validPort(c.Device.ControlPort) || !validPort(c.Device.AudioPort) {
		return errors.New("device ports must be between 1 and 65535")
	}
	if c.Device.ControlPort == c.Device.AudioPort {
		return errors.New("control and audio ports must differ")
	}
	if _, err := time.LoadLocation(c.Timezone); err != nil {
		return fmt.Errorf("timezone: %w", err)
	}
	if c.RecordingDir == "" || c.SchedulePath == "" || c.SocketPath == "" {
		return errors.New("recording_dir, schedule_path, and socket_path are required")
	}
	if !filepath.IsAbs(c.RecordingDir) || !filepath.IsAbs(c.SchedulePath) || !filepath.IsAbs(c.SocketPath) {
		return errors.New("recording_dir, schedule_path, and socket_path must be absolute")
	}
	if c.FFmpegPath == "" {
		return errors.New("ffmpeg_path is required")
	}
	return nil
}

func validPort(port int) bool { return port > 0 && port <= 65535 }
