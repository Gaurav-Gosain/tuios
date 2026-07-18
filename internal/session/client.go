package session

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"time"

	"github.com/charmbracelet/colorprofile"
	"golang.org/x/term"
)

// Client connects to the TUIOS daemon for one-shot request/response control
// messages (used by the tuios CLI). It does not stream an interactive session.
type Client struct {
	conn    net.Conn
	version string

	// Terminal size, reported to the daemon in the hello handshake.
	width  int
	height int

	// Message handling
	done      chan struct{}
	closeOnce sync.Once
	sendMu    sync.Mutex
	recvMu    sync.Mutex

	// Codec negotiated with daemon (gob by default)
	codec Codec
}

// ClientConfig holds configuration for creating a client.
type ClientConfig struct {
	Version    string
	SocketPath string // Optional override
}

// NewClient creates a new daemon client.
func NewClient(cfg *ClientConfig) *Client {
	return &Client{
		version: cfg.Version,
		done:    make(chan struct{}),
		codec:   DefaultCodec(), // gob by default
	}
}

// Connect connects to the daemon.
func (c *Client) Connect() error {
	socketPath, err := GetSocketPath()
	if err != nil {
		return fmt.Errorf("failed to get socket path: %w", err)
	}

	conn, err := net.DialTimeout("unix", socketPath, 5*time.Second)
	if err != nil {
		return fmt.Errorf("failed to connect to daemon: %w", err)
	}
	c.conn = conn

	// Get terminal size
	c.width, c.height = c.getTerminalSize()

	// Send hello
	if err := c.sendHello(); err != nil {
		_ = conn.Close()
		return fmt.Errorf("handshake failed: %w", err)
	}

	return nil
}

// Close closes the connection.
// Safe to call multiple times concurrently.
func (c *Client) Close() error {
	var err error
	c.closeOnce.Do(func() {
		close(c.done)
		if c.conn != nil {
			err = c.conn.Close()
		}
	})
	return err
}

// ListSessions returns a list of all sessions.
func (c *Client) ListSessions() ([]SessionInfo, error) {
	msg, err := NewMessageWithCodec(MsgList, nil, c.codec)
	if err != nil {
		return nil, err
	}

	if err := c.send(msg); err != nil {
		return nil, err
	}

	resp, err := c.recv()
	if err != nil {
		return nil, err
	}

	if resp.Type != MsgSessionList {
		return nil, fmt.Errorf("unexpected response type: %d", resp.Type)
	}

	var payload SessionListPayload
	if err := resp.ParsePayloadWithCodec(&payload, c.codec); err != nil {
		return nil, err
	}

	return payload.Sessions, nil
}

// KillSession terminates a session.
func (c *Client) KillSession(name string) error {
	msg, err := NewMessageWithCodec(MsgKill, &KillPayload{
		SessionName: name,
	}, c.codec)
	if err != nil {
		return err
	}

	if err := c.send(msg); err != nil {
		return err
	}

	resp, err := c.recv()
	if err != nil {
		return err
	}

	switch resp.Type {
	case MsgSessionList:
		return nil // Success

	case MsgError:
		var errPayload ErrorPayload
		if err := resp.ParsePayloadWithCodec(&errPayload, c.codec); err != nil {
			return fmt.Errorf("kill failed")
		}
		return fmt.Errorf("kill failed: %s", errPayload.Message)

	default:
		return fmt.Errorf("unexpected response type: %d", resp.Type)
	}
}

// CreateDetachedSession asks the daemon to create a headless session (with an
// initial window) and no attached client. name may be empty to let the daemon
// generate one. It returns an error if the name is already taken.
func (c *Client) CreateDetachedSession(name string, width, height int) error {
	msg, err := NewMessageWithCodec(MsgNew, &NewPayload{
		SessionName: name,
		Width:       width,
		Height:      height,
		Detach:      true,
	}, c.codec)
	if err != nil {
		return err
	}

	if err := c.send(msg); err != nil {
		return err
	}

	resp, err := c.recv()
	if err != nil {
		return err
	}

	switch resp.Type {
	case MsgSessionList:
		return nil // Success

	case MsgError:
		var errPayload ErrorPayload
		if err := resp.ParsePayloadWithCodec(&errPayload, c.codec); err != nil {
			return fmt.Errorf("create failed")
		}
		return fmt.Errorf("create failed: %s", errPayload.Message)

	default:
		return fmt.Errorf("unexpected response type: %d", resp.Type)
	}
}

// ResurrectSession asks the daemon to restore a saved session on demand. It is
// a no-op (success) if the session is already live.
func (c *Client) ResurrectSession(name string) error {
	msg, err := NewMessageWithCodec(MsgResurrect, &ResurrectPayload{
		SessionName: name,
	}, c.codec)
	if err != nil {
		return err
	}

	if err := c.send(msg); err != nil {
		return err
	}

	resp, err := c.recv()
	if err != nil {
		return err
	}

	switch resp.Type {
	case MsgSessionList:
		return nil // Success

	case MsgError:
		var errPayload ErrorPayload
		if err := resp.ParsePayloadWithCodec(&errPayload, c.codec); err != nil {
			return fmt.Errorf("resurrect failed")
		}
		return fmt.Errorf("resurrect failed: %s", errPayload.Message)

	default:
		return fmt.Errorf("unexpected response type: %d", resp.Type)
	}
}

func (c *Client) sendHello() error {
	// Detect terminal capabilities
	termType, colorTerm := detectTerminalEnv()

	// Detect shell
	shell := detectShell()

	msg, err := NewMessageWithCodec(MsgHello, &HelloPayload{
		Version:        c.version,
		Term:           termType,
		ColorTerm:      colorTerm,
		Shell:          shell,
		Width:          c.width,
		Height:         c.height,
		PreferredCodec: "gob", // Request gob (default)
	}, c.codec)
	if err != nil {
		return err
	}

	if err := c.send(msg); err != nil {
		return err
	}

	// Wait for welcome
	resp, err := c.recv()
	if err != nil {
		return err
	}

	if resp.Type != MsgWelcome {
		return fmt.Errorf("expected welcome, got message type %d", resp.Type)
	}

	// Parse welcome to get negotiated codec
	var welcome WelcomePayload
	if err := resp.ParsePayloadWithCodec(&welcome, c.codec); err != nil {
		return fmt.Errorf("failed to parse welcome: %w", err)
	}

	// Update codec based on what server negotiated
	c.codec = NegotiateCodec(welcome.Codec)

	return nil
}

func (c *Client) send(msg *Message) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()

	_ = c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return WriteMessageWithCodec(c.conn, msg, c.codec)
}

func (c *Client) recv() (*Message, error) {
	c.recvMu.Lock()
	defer c.recvMu.Unlock()

	_ = c.conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	msg, _, err := ReadMessageWithCodec(c.conn)
	return msg, err
}

func (c *Client) getTerminalSize() (width, height int) {
	width, height, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		return 80, 24 // Default
	}
	return width, height
}

// SendControlMessage sends a control message to the daemon and waits for a response.
// This is used for CLI commands that need to send messages without attaching to a session.
func (c *Client) SendControlMessage(msg *Message) (*Message, error) {
	if err := c.send(msg); err != nil {
		return nil, fmt.Errorf("failed to send message: %w", err)
	}

	// Wait for response
	resp, err := c.recv()
	if err != nil {
		return nil, fmt.Errorf("failed to receive response: %w", err)
	}

	return resp, nil
}

// GetCodec returns the negotiated codec for this client.
func (c *Client) GetCodec() Codec {
	return c.codec
}

// detectTerminalEnv detects TERM and COLORTERM values.
func detectTerminalEnv() (termType, colorTerm string) {
	// Check environment first
	envTerm := os.Getenv("TERM")
	envColorTerm := os.Getenv("COLORTERM")

	if envColorTerm == "truecolor" && envTerm != "" && envTerm != "dumb" {
		return envTerm, envColorTerm
	}

	// Detect using colorprofile
	profile := colorprofile.Detect(os.Stdout, os.Environ())

	switch profile {
	case colorprofile.TrueColor:
		if envTerm != "" {
			termType = envTerm
		} else {
			termType = "xterm-256color"
		}
		colorTerm = "truecolor"

	case colorprofile.ANSI256:
		if envTerm != "" {
			termType = envTerm
		} else {
			termType = "xterm-256color"
		}
		colorTerm = ""

	case colorprofile.ANSI:
		if envTerm != "" && envTerm != "dumb" {
			termType = envTerm
		} else {
			termType = "xterm"
		}
		colorTerm = ""

	default:
		termType = "dumb"
		colorTerm = ""
	}

	return termType, colorTerm
}

// detectShell detects the user's preferred shell.
func detectShell() string {
	// Check SHELL environment variable
	if shell := os.Getenv("SHELL"); shell != "" {
		return shell
	}

	// Platform-specific fallbacks
	if runtime.GOOS == "windows" {
		shells := []string{"powershell.exe", "pwsh.exe", "cmd.exe"}
		for _, shell := range shells {
			if _, err := exec.LookPath(shell); err == nil {
				return shell
			}
		}
		return "cmd.exe"
	}

	// Unix shells
	shells := []string{"/bin/bash", "/bin/zsh", "/bin/fish", "/bin/sh"}
	for _, shell := range shells {
		if _, err := os.Stat(shell); err == nil {
			return shell
		}
	}
	return "/bin/sh"
}
