package session

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"time"

	"github.com/charmbracelet/colorprofile"
	"golang.org/x/term"
)

// Client connects to the TUIOS daemon and handles terminal I/O.
type Client struct {
	conn     net.Conn
	version  string
	attached bool

	// Terminal state
	width    int
	height   int
	oldState *term.State

	// Message handling
	done      chan struct{}
	closeOnce sync.Once
	sendMu    sync.Mutex
	recvMu    sync.Mutex

	// Codec negotiated with daemon (gob by default)
	codec Codec

	// Session info (after attach)
	sessionName string
	sessionID   string

	// Prefix key state for detach detection (Ctrl+B, d)
	prefixActive bool
	prefixKey    byte // Default: Ctrl+B (0x02)
}

// ClientConfig holds configuration for creating a client.
type ClientConfig struct {
	Version    string
	SocketPath string // Optional override
}

// NewClient creates a new daemon client.
func NewClient(cfg *ClientConfig) *Client {
	return &Client{
		version:   cfg.Version,
		done:      make(chan struct{}),
		prefixKey: 0x02,           // Ctrl+B
		codec:     DefaultCodec(), // gob by default
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

// Attach attaches to a session.
func (c *Client) Attach(sessionName string, createIfMissing bool) error {
	msg, err := NewMessageWithCodec(MsgAttach, &AttachPayload{
		SessionName: sessionName,
		CreateNew:   createIfMissing,
		Width:       c.width,
		Height:      c.height,
	}, c.codec)
	if err != nil {
		return err
	}

	if err := c.send(msg); err != nil {
		return err
	}

	// Wait for response
	resp, err := c.recv()
	if err != nil {
		return err
	}

	switch resp.Type {
	case MsgAttached:
		var payload AttachedPayload
		if err := resp.ParsePayloadWithCodec(&payload, c.codec); err != nil {
			return err
		}
		c.attached = true
		c.sessionName = payload.SessionName
		c.sessionID = payload.SessionID
		return nil

	case MsgError:
		var errPayload ErrorPayload
		if err := resp.ParsePayloadWithCodec(&errPayload, c.codec); err != nil {
			return fmt.Errorf("attach failed")
		}
		return fmt.Errorf("attach failed: %s", errPayload.Message)

	default:
		return fmt.Errorf("unexpected response type: %d", resp.Type)
	}
}

// NewSession creates a new session.
func (c *Client) NewSession(name string) error {
	msg, err := NewMessageWithCodec(MsgNew, &NewPayload{
		SessionName: name,
		Width:       c.width,
		Height:      c.height,
	}, c.codec)
	if err != nil {
		return err
	}

	if err := c.send(msg); err != nil {
		return err
	}

	// Wait for response (session list or error)
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

// Detach detaches from the current session.
func (c *Client) Detach() error {
	if !c.attached {
		return fmt.Errorf("not attached to any session")
	}

	msg, err := NewMessageWithCodec(MsgDetach, nil, c.codec)
	if err != nil {
		return err
	}

	if err := c.send(msg); err != nil {
		return err
	}

	// Wait for confirmation
	resp, err := c.recv()
	if err != nil {
		return err
	}

	if resp.Type == MsgDetached {
		c.attached = false
		c.sessionName = ""
		c.sessionID = ""
		return nil
	}

	return fmt.Errorf("unexpected response type: %d", resp.Type)
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

// SendInput sends input bytes to the attached session.
func (c *Client) SendInput(data []byte) error {
	if !c.attached {
		return fmt.Errorf("not attached to any session")
	}

	msg := NewRawMessage(MsgInput, data)
	return c.send(msg)
}

// SendResize notifies the session of a terminal resize.
func (c *Client) SendResize(width, height int) error {
	c.width = width
	c.height = height

	msg, err := NewMessageWithCodec(MsgResize, &ResizePayload{
		Width:  width,
		Height: height,
	}, c.codec)
	if err != nil {
		return err
	}

	return c.send(msg)
}

// ReadOutput reads output from the session.
// Returns the output bytes, or nil on non-output messages.
func (c *Client) ReadOutput() ([]byte, error) {
	msg, err := c.recv()
	if err != nil {
		return nil, err
	}

	switch msg.Type {
	case MsgOutput:
		return msg.Payload, nil
	case MsgSessionEnded:
		return nil, io.EOF
	case MsgDetached:
		c.attached = false
		return nil, io.EOF
	case MsgError:
		var errPayload ErrorPayload
		_ = msg.ParsePayloadWithCodec(&errPayload, c.codec)
		return nil, fmt.Errorf("server error: %s", errPayload.Message)
	default:
		// Other message types, return nil to continue
		return nil, nil
	}
}

// Run runs the interactive terminal session with PTY streaming from the daemon.
// The terminal is put in raw mode and all I/O is streamed to/from the daemon's PTYs.
func (c *Client) Run() error {
	if !c.attached {
		return fmt.Errorf("not attached to any session")
	}

	// Check if stdin is a terminal
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return fmt.Errorf("stdin is not a terminal - run this command from an interactive terminal")
	}

	// Put terminal in raw mode
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return fmt.Errorf("failed to set raw mode: %w", err)
	}
	c.oldState = oldState
	defer c.restoreTerminal()

	// Set up channels for coordination
	errCh := make(chan error, 2)

	// Start goroutine to read from daemon and write to terminal
	go c.readFromDaemon(errCh)

	// Start goroutine to read from terminal and send to daemon
	go c.readFromTerminal(errCh)

	// Start resize watcher
	go c.watchResizeSignals()

	// Wait for error or done signal
	select {
	case err := <-errCh:
		return err
	case <-c.done:
		return nil
	}
}

// readFromDaemon reads messages from the daemon and writes output to the terminal.
func (c *Client) readFromDaemon(errCh chan<- error) {
	for {
		select {
		case <-c.done:
			return
		default:
		}

		// Set a reasonable read deadline to allow checking done channel
		_ = c.conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))

		msg, _, err := ReadMessageWithCodec(c.conn)
		if err != nil {
			// Check for timeout (need to unwrap since ReadMessage wraps errors)
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				continue // Normal timeout, keep reading
			}
			if errors.Is(err, io.EOF) {
				errCh <- fmt.Errorf("daemon disconnected")
				return
			}
			errCh <- fmt.Errorf("read error: %w", err)
			return
		}

		switch msg.Type {
		case MsgOutput:
			// Raw terminal output from the TUIOS TUI
			_, _ = os.Stdout.Write(msg.Payload)

		case MsgPTYOutput:
			// PTY output - try binary format first, then codec format
			ptyID, data, err := ParseBinaryPTYMessage(msg.Payload)
			if err != nil || ptyID == "" {
				// Fall back to codec format
				var payload PTYOutputPayload
				if err := msg.ParsePayloadWithCodec(&payload, c.codec); err != nil {
					_, _ = os.Stdout.Write(msg.Payload)
					continue
				}
				data = payload.Data
			}
			_, _ = os.Stdout.Write(data)

		case MsgSessionEnded:
			errCh <- io.EOF
			return

		case MsgDetached:
			c.attached = false
			// Print detach message (restore terminal first for clean output)
			c.restoreTerminal()
			fmt.Println("\r\n[detached from session]")
			errCh <- nil
			return

		case MsgError:
			var errPayload ErrorPayload
			_ = msg.ParsePayloadWithCodec(&errPayload, c.codec)
			errCh <- fmt.Errorf("server error: %s", errPayload.Message)
			return

		case MsgPTYClosed:
			// A PTY was closed, might want to notify user
			// For now, continue running if there are other PTYs

		case MsgPong:
			// Keepalive response, ignore

		default:
			// Unknown message type, ignore
		}
	}
}

// readFromTerminal reads from stdin and sends to the daemon.
// Also handles the detach sequence (Ctrl+B, d).
func (c *Client) readFromTerminal(errCh chan<- error) {
	buf := make([]byte, 4096)

	for {
		select {
		case <-c.done:
			return
		default:
		}

		n, err := os.Stdin.Read(buf)
		if err != nil {
			if err == io.EOF {
				errCh <- nil
				return
			}
			errCh <- fmt.Errorf("stdin read error: %w", err)
			return
		}

		if n > 0 {
			// Process input byte by byte to detect detach sequence
			toSend := make([]byte, 0, n)

			for i := range n {
				b := buf[i]

				if c.prefixActive {
					c.prefixActive = false
					if b == 'd' || b == 'D' {
						// Detach sequence detected (Ctrl+B, d)
						_ = c.Detach() // Ignore error, we're exiting anyway
						errCh <- nil
						return
					}
					// Not a detach, send the prefix key and this byte
					toSend = append(toSend, c.prefixKey, b)
					continue
				}

				if b == c.prefixKey {
					// Prefix key pressed, wait for next key
					c.prefixActive = true
					continue
				}

				toSend = append(toSend, b)
			}

			// Send remaining input to daemon
			if len(toSend) > 0 {
				msg := NewRawMessage(MsgInput, toSend)
				if err := c.send(msg); err != nil {
					errCh <- fmt.Errorf("send error: %w", err)
					return
				}
			}
		}
	}
}

// watchResizeSignals monitors for terminal resize and sends updates to daemon.
func (c *Client) watchResizeSignals() {
	// Poll for size changes (cross-platform approach)
	// On Unix, we could use SIGWINCH, but polling works everywhere
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-c.done:
			return
		case <-ticker.C:
			w, h := c.getTerminalSize()
			if w != c.width || h != c.height {
				c.width = w
				c.height = h
				_ = c.SendResize(w, h)
			}
		}
	}
}

// restoreTerminal restores the terminal to its original state.
func (c *Client) restoreTerminal() {
	if c.oldState != nil {
		_ = term.Restore(int(os.Stdin.Fd()), c.oldState)
		c.oldState = nil
	}
}

// ErrRunLocalTUI is returned by Run() to indicate the caller should run TUIOS locally.
// This is deprecated - Run() now streams directly from daemon PTYs.
var ErrRunLocalTUI = fmt.Errorf("run local TUI")

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

// IsAttached returns true if client is attached to a session.
func (c *Client) IsAttached() bool {
	return c.attached
}

// SessionName returns the name of the attached session.
func (c *Client) SessionName() string {
	return c.sessionName
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
