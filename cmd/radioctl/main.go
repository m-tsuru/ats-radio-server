package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/m-tsuru/ats-radio-server/internal/daemon"
	"github.com/m-tsuru/ats-radio-server/internal/device"
	"github.com/m-tsuru/ats-radio-server/internal/scheduler"
)

const maxResponseBytes = 64 * 1024

func main() {
	root := flag.NewFlagSet("radioctl", flag.ExitOnError)
	socketPath := root.String("socket", "/run/radiod/radiod.sock", "radiod Unix socket")
	root.Usage = usage
	_ = root.Parse(os.Args[1:])
	args := root.Args()
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var err error
	switch args[0] {
	case "status":
		err = simpleCommand(ctx, *socketPath, daemon.Request{Command: "status"}, true)
	case "tune":
		err = tuneCommand(ctx, *socketPath, args[1:])
	case "volume":
		err = volumeCommand(ctx, *socketPath, args[1:])
	case "listen":
		cancel()
		err = listenCommand(context.Background(), *socketPath, args[1:])
	case "record":
		cancel()
		err = recordCommand(context.Background(), *socketPath, args[1:])
	case "schedule":
		err = scheduleCommand(ctx, *socketPath, args[1:])
	default:
		err = fmt.Errorf("unknown command %q", args[0])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "radioctl:", err)
		os.Exit(1)
	}
}

func tuneCommand(ctx context.Context, socketPath string, args []string) error {
	flags := flag.NewFlagSet("tune", flag.ContinueOnError)
	modeName := flags.String("mode", "", "FM, AM, USB, or LSB")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return errors.New("usage: radioctl tune [--mode MODE] FREQUENCY")
	}
	frequencyHz, err := parseFrequency(flags.Arg(0))
	if err != nil {
		return err
	}
	mode := device.Mode(strings.ToUpper(*modeName))
	if mode == "" {
		mode = device.ModeAM
		if frequencyHz >= 64_000_000 {
			mode = device.ModeFM
		}
	}
	if err := device.ValidateStation(mode, frequencyHz); err != nil {
		return err
	}
	return simpleCommand(ctx, socketPath, daemon.Request{Command: "tune", Mode: mode, FrequencyHz: frequencyHz}, false)
}

func volumeCommand(ctx context.Context, socketPath string, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: radioctl volume LEVEL")
	}
	volume, err := strconv.Atoi(args[0])
	if err != nil || volume < 0 || volume > 63 {
		return errors.New("volume must be an integer between 0 and 63")
	}
	return simpleCommand(ctx, socketPath, daemon.Request{Command: "volume", Volume: volume}, false)
}

func recordCommand(ctx context.Context, socketPath string, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: radioctl record start|stop")
	}
	switch args[0] {
	case "stop":
		if len(args) != 1 {
			return errors.New("usage: radioctl record stop")
		}
		stopCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		return simpleCommand(stopCtx, socketPath, daemon.Request{Command: "record.stop"}, false)
	case "start":
		flags := flag.NewFlagSet("record start", flag.ContinueOnError)
		modeName := flags.String("mode", "", "FM, AM, USB, or LSB")
		format := flags.String("format", "flac", "flac or wav")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		request := daemon.Request{Command: "record.start", Format: *format}
		if flags.NArg() > 1 {
			return errors.New("usage: radioctl record start [--format FORMAT] [--mode MODE] [FREQUENCY]")
		}
		if flags.NArg() == 1 {
			frequencyHz, err := parseFrequency(flags.Arg(0))
			if err != nil {
				return err
			}
			mode := device.Mode(strings.ToUpper(*modeName))
			if mode == "" {
				mode = device.ModeAM
				if frequencyHz >= 64_000_000 {
					mode = device.ModeFM
				}
			}
			request.FrequencyHz, request.Mode = frequencyHz, mode
		} else if *modeName != "" {
			return errors.New("--mode requires a frequency")
		}
		startCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		return simpleCommand(startCtx, socketPath, request, true)
	default:
		return fmt.Errorf("unknown record command %q", args[0])
	}
}

func scheduleCommand(ctx context.Context, socketPath string, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: radioctl schedule list|add|remove|enable|disable")
	}
	switch args[0] {
	case "list":
		return simpleCommand(ctx, socketPath, daemon.Request{Command: "schedule.list"}, true)
	case "add":
		if len(args) != 2 {
			return errors.New("usage: radioctl schedule add FILE")
		}
		reservation, err := readReservation(args[1])
		if err != nil {
			return err
		}
		return simpleCommand(ctx, socketPath, daemon.Request{Command: "schedule.add", Schedule: &reservation}, false)
	case "remove", "enable", "disable":
		if len(args) != 2 {
			return fmt.Errorf("usage: radioctl schedule %s ID", args[0])
		}
		return simpleCommand(ctx, socketPath, daemon.Request{Command: "schedule." + args[0], ID: args[1]}, false)
	default:
		return fmt.Errorf("unknown schedule command %q", args[0])
	}
}

func listenCommand(ctx context.Context, socketPath string, args []string) error {
	flags := flag.NewFlagSet("listen", flag.ContinueOnError)
	player := flags.String("player", "ffplay", "PCM player executable")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("usage: radioctl listen [--player ffplay]")
	}
	conn, reader, response, err := call(ctx, socketPath, daemon.Request{Command: "listen"})
	if err != nil {
		return err
	}
	defer conn.Close()
	if !response.OK {
		return errors.New(response.Error)
	}
	var header device.PCMHeader
	if err := json.Unmarshal(response.Data, &header); err != nil {
		return fmt.Errorf("decode audio metadata: %w", err)
	}
	inputFormat := map[int]string{16: "s16le", 24: "s24le", 32: "s32le"}[header.Bits]
	if inputFormat == "" {
		return fmt.Errorf("unsupported PCM bit depth %d", header.Bits)
	}
	cmd := exec.CommandContext(ctx, *player,
		"-hide_banner", "-loglevel", "warning", "-nodisp", "-autoexit",
		"-f", inputFormat, "-ar", strconv.Itoa(header.SampleRate),
		"-ac", strconv.Itoa(header.Channels), "-i", "pipe:0",
	)
	cmd.Stdin = reader
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

type wireResponse struct {
	OK    bool            `json:"ok"`
	Error string          `json:"error,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
}

func simpleCommand(ctx context.Context, socketPath string, request daemon.Request, printData bool) error {
	conn, _, response, err := call(ctx, socketPath, request)
	if conn != nil {
		defer conn.Close()
	}
	if err != nil {
		return err
	}
	if !response.OK {
		return errors.New(response.Error)
	}
	if printData && len(response.Data) > 0 {
		var pretty any
		if err := json.Unmarshal(response.Data, &pretty); err != nil {
			return err
		}
		output, _ := json.MarshalIndent(pretty, "", "  ")
		fmt.Println(string(output))
	}
	return nil
}

func call(ctx context.Context, socketPath string, request daemon.Request) (net.Conn, *bufio.Reader, wireResponse, error) {
	dialer := net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return nil, nil, wireResponse{}, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if err := json.NewEncoder(conn).Encode(request); err != nil {
		conn.Close()
		return nil, nil, wireResponse{}, err
	}
	reader := bufio.NewReaderSize(conn, maxResponseBytes)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		conn.Close()
		return nil, nil, wireResponse{}, err
	}
	if len(line) > maxResponseBytes {
		conn.Close()
		return nil, nil, wireResponse{}, errors.New("radiod response too large")
	}
	var response wireResponse
	if err := json.Unmarshal(line, &response); err != nil {
		conn.Close()
		return nil, nil, wireResponse{}, err
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, reader, response, nil
}

func readReservation(path string) (scheduler.Reservation, error) {
	var reader io.Reader
	if path == "-" {
		reader = os.Stdin
	} else {
		file, err := os.Open(path)
		if err != nil {
			return scheduler.Reservation{}, err
		}
		defer file.Close()
		reader = file
	}
	decoder := json.NewDecoder(io.LimitReader(reader, maxResponseBytes))
	decoder.DisallowUnknownFields()
	var reservation scheduler.Reservation
	if err := decoder.Decode(&reservation); err != nil {
		return scheduler.Reservation{}, err
	}
	return reservation, nil
}

func parseFrequency(value string) (uint64, error) {
	value = strings.TrimSpace(strings.ToUpper(value))
	multiplier := float64(1)
	if strings.HasSuffix(value, "M") {
		multiplier = 1_000_000
		value = strings.TrimSuffix(value, "M")
	} else if strings.HasSuffix(value, "K") {
		multiplier = 1_000
		value = strings.TrimSuffix(value, "K")
	}
	number, err := strconv.ParseFloat(value, 64)
	if err != nil || number <= 0 || number > math.MaxUint64/multiplier {
		return 0, fmt.Errorf("invalid frequency %q", value)
	}
	hz := number * multiplier
	if hz != math.Trunc(hz) {
		return 0, errors.New("frequency must resolve to a whole number of Hz")
	}
	return uint64(hz), nil
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage: radioctl [-socket PATH] COMMAND

commands:
  status
  tune [--mode FM|AM|USB|LSB] FREQUENCY
  volume LEVEL
  listen [--player ffplay]
  record start [--format flac|wav] [--mode MODE] [FREQUENCY]
  record stop
  schedule list
  schedule add FILE
  schedule remove ID
  schedule enable ID
  schedule disable ID`)
}
