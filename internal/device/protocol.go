package device

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const maxLineBytes = 4096

type Mode string

const (
	ModeFM  Mode = "FM"
	ModeAM  Mode = "AM"
	ModeUSB Mode = "USB"
	ModeLSB Mode = "LSB"
)

type Status struct {
	State         string `json:"state"`
	FrequencyHz   uint64 `json:"frequency_hz"`
	Mode          Mode   `json:"mode"`
	Volume        int    `json:"volume"`
	RSSI          int    `json:"rssi"`
	SNR           int    `json:"snr"`
	BatteryMV     int    `json:"battery_mv"`
	ExternalPower bool   `json:"external_power"`
	WiFiRSSIDBm   int    `json:"wifi_rssi_dbm"`
	IP            string `json:"ip"`
	TimeSynced    bool   `json:"time_synced"`
	Streaming     bool   `json:"streaming"`
}

type ProtocolError struct{ Reason string }

func (e *ProtocolError) Error() string { return "device: " + e.Reason }

func ParseResponseLine(line string) error {
	line = strings.TrimSpace(line)
	if line == "OK" {
		return nil
	}
	if strings.HasPrefix(line, "ERR ") && len(line) > 4 {
		return &ProtocolError{Reason: line[4:]}
	}
	return fmt.Errorf("unexpected device response %q", line)
}

func ParseStatusLine(line string) (Status, error) {
	var status Status
	decoder := json.NewDecoder(strings.NewReader(strings.TrimSpace(line)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&status); err != nil {
		return Status{}, fmt.Errorf("decode device status: %w", err)
	}
	if status.State != "waiting" && status.State != "receiving" {
		return Status{}, fmt.Errorf("decode device status: invalid state %q", status.State)
	}
	if err := ValidateStation(status.Mode, status.FrequencyHz); err != nil {
		return Status{}, fmt.Errorf("decode device status: %w", err)
	}
	if status.Volume < 0 || status.Volume > 63 {
		return Status{}, errors.New("decode device status: volume outside 0..63")
	}
	return status, nil
}

func ValidateStation(mode Mode, frequencyHz uint64) error {
	switch mode {
	case ModeFM:
		if frequencyHz < 64_000_000 || frequencyHz > 108_000_000 {
			return errors.New("FM frequency must be between 64000000 and 108000000 Hz")
		}
	case ModeAM:
		if frequencyHz < 150_000 || frequencyHz > 30_000_000 {
			return errors.New("AM frequency must be between 150000 and 30000000 Hz")
		}
	case ModeUSB, ModeLSB:
		if frequencyHz < 1_700_000 || frequencyHz > 30_000_000 {
			return errors.New("SSB frequency must be between 1700000 and 30000000 Hz")
		}
	default:
		return fmt.Errorf("unsupported mode %q", mode)
	}
	return nil
}

type PCMHeader struct {
	SampleRate int    `json:"sample_rate"`
	Bits       int    `json:"bits"`
	Channels   int    `json:"channels"`
	Endian     string `json:"endianness"`
}

func ParsePCMHeader(line string) (PCMHeader, error) {
	fields := strings.Fields(line)
	if len(fields) != 5 || fields[0] != "PCM" {
		return PCMHeader{}, fmt.Errorf("invalid PCM header %q", strings.TrimSpace(line))
	}
	rate, err := strconv.Atoi(fields[1])
	if err != nil || rate < 8000 || rate > 192000 {
		return PCMHeader{}, errors.New("invalid PCM sample rate")
	}
	bits, err := strconv.Atoi(fields[2])
	if err != nil || (bits != 16 && bits != 24 && bits != 32) {
		return PCMHeader{}, errors.New("invalid PCM bit depth")
	}
	channels, err := strconv.Atoi(fields[3])
	if err != nil || (channels != 1 && channels != 2) {
		return PCMHeader{}, errors.New("invalid PCM channel count")
	}
	if fields[4] != "LE" {
		return PCMHeader{}, errors.New("only little-endian PCM is supported")
	}
	return PCMHeader{SampleRate: rate, Bits: bits, Channels: channels, Endian: fields[4]}, nil
}

func readLine(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		if errors.Is(err, io.EOF) && line != "" {
			return "", errors.New("device response is not newline terminated")
		}
		return "", err
	}
	if len(line) > maxLineBytes {
		return "", errors.New("device response exceeds maximum line length")
	}
	return strings.TrimRight(line, "\r\n"), nil
}
