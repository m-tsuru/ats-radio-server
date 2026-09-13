package device

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Client struct {
	controlAddress string
	audioAddress   string
	dialer         net.Dialer

	mu     sync.Mutex
	conn   net.Conn
	reader *bufio.Reader
}

func NewClient(host string, controlPort, audioPort int) *Client {
	return &Client{
		controlAddress: net.JoinHostPort(host, strconv.Itoa(controlPort)),
		audioAddress:   net.JoinHostPort(host, strconv.Itoa(audioPort)),
		dialer:         net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second},
	}
}

func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closeLocked()
}

func (c *Client) Status(ctx context.Context) (Status, error) {
	line, err := c.command(ctx, "STATUS")
	if err != nil {
		return Status{}, err
	}
	return ParseStatusLine(line)
}

func (c *Client) Tune(ctx context.Context, mode Mode, frequencyHz uint64) error {
	if err := ValidateStation(mode, frequencyHz); err != nil {
		return err
	}
	line, err := c.command(ctx, fmt.Sprintf("TUNE %s %d", mode, frequencyHz))
	if err != nil {
		return err
	}
	return ParseResponseLine(line)
}

func (c *Client) SetVolume(ctx context.Context, volume int) error {
	if volume < 0 || volume > 63 {
		return errors.New("volume must be between 0 and 63")
	}
	line, err := c.command(ctx, fmt.Sprintf("VOLUME %d", volume))
	if err != nil {
		return err
	}
	return ParseResponseLine(line)
}

func (c *Client) SetStream(ctx context.Context, enabled bool) error {
	action := "STOP"
	if enabled {
		action = "START"
	}
	line, err := c.command(ctx, "STREAM "+action)
	if err != nil {
		return err
	}
	return ParseResponseLine(line)
}

func (c *Client) command(ctx context.Context, command string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if strings.ContainsAny(command, "\r\n") {
		return "", errors.New("command must be a single line")
	}

	for attempt := 0; attempt < 2; attempt++ {
		if err := c.connectLocked(ctx); err != nil {
			return "", err
		}
		deadline := deadlineFromContext(ctx, 5*time.Second)
		if err := c.conn.SetDeadline(deadline); err != nil {
			c.closeLocked()
			return "", err
		}
		if _, err := io.WriteString(c.conn, command+"\n"); err != nil {
			c.closeLocked()
			if attempt == 0 {
				continue
			}
			return "", fmt.Errorf("write device command: %w", err)
		}
		line, err := readLine(c.reader)
		if err != nil {
			c.closeLocked()
			if attempt == 0 {
				continue
			}
			return "", fmt.Errorf("read device response: %w", err)
		}
		_ = c.conn.SetDeadline(time.Time{})
		return line, nil
	}
	return "", errors.New("device command failed")
}

func (c *Client) connectLocked(ctx context.Context) error {
	if c.conn != nil {
		return nil
	}
	conn, err := c.dialer.DialContext(ctx, "tcp", c.controlAddress)
	if err != nil {
		return fmt.Errorf("connect device control %s: %w", c.controlAddress, err)
	}
	c.conn = conn
	c.reader = bufio.NewReaderSize(conn, maxLineBytes)
	if err := conn.SetDeadline(deadlineFromContext(ctx, 5*time.Second)); err != nil {
		c.closeLocked()
		return err
	}
	if _, err := io.WriteString(conn, "HELLO 1\n"); err != nil {
		c.closeLocked()
		return fmt.Errorf("send device handshake: %w", err)
	}
	line, err := readLine(c.reader)
	if err != nil {
		c.closeLocked()
		return fmt.Errorf("read device handshake: %w", err)
	}
	if err := ParseResponseLine(line); err != nil {
		c.closeLocked()
		return fmt.Errorf("device handshake: %w", err)
	}
	_ = conn.SetDeadline(time.Time{})
	return nil
}

func (c *Client) closeLocked() error {
	if c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.conn = nil
	c.reader = nil
	return err
}

type AudioStream struct {
	Header PCMHeader
	Reader io.Reader
	conn   net.Conn
}

func (s *AudioStream) Close() error { return s.conn.Close() }

func (c *Client) OpenAudio(ctx context.Context) (*AudioStream, error) {
	conn, err := c.dialer.DialContext(ctx, "tcp", c.audioAddress)
	if err != nil {
		return nil, fmt.Errorf("connect device audio %s: %w", c.audioAddress, err)
	}
	reader := bufio.NewReaderSize(conn, maxLineBytes)
	_ = conn.SetReadDeadline(deadlineFromContext(ctx, 5*time.Second))
	line, err := readLine(reader)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("read audio header: %w", err)
	}
	header, err := ParsePCMHeader(line)
	if err != nil {
		conn.Close()
		return nil, err
	}
	_ = conn.SetReadDeadline(time.Time{})
	return &AudioStream{Header: header, Reader: reader, conn: conn}, nil
}

func deadlineFromContext(ctx context.Context, fallback time.Duration) time.Time {
	if deadline, ok := ctx.Deadline(); ok {
		return deadline
	}
	return time.Now().Add(fallback)
}
