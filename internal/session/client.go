package session

import (
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"golang.org/x/term"

	"github.com/Gaurav-Gosain/tuios/internal/guestenv"
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
	msg, err := NewMessage(MsgList, nil)
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
	if err := resp.ParsePayload(&payload); err != nil {
		return nil, err
	}

	return payload.Sessions, nil
}

// CreateDetachedSession asks the daemon to create a headless session (with an
// initial window) and no attached client. name may be empty to let the daemon
// generate one. It returns an error if the name is already taken.
func (c *Client) CreateDetachedSession(name string, width, height int) error {
	return c.createSession(name, width, height, false)
}

// CreateGlobalSession creates a session meant to hold panes from more than one
// machine. It is created with no windows, because its first pane is the one
// the user picks a machine for. See NewPayload.Global.
func (c *Client) CreateGlobalSession(name string, width, height int) error {
	return c.createSession(name, width, height, true)
}

func (c *Client) createSession(name string, width, height int, global bool) error {
	msg, err := NewMessage(MsgNew, &NewPayload{
		SessionName: name,
		Width:       width,
		Height:      height,
		Detach:      true,
		Global:      global,
	})
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
		if err := resp.ParsePayload(&errPayload); err != nil {
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
	msg, err := NewMessage(MsgResurrect, &ResurrectPayload{
		SessionName: name,
	})
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
		if err := resp.ParsePayload(&errPayload); err != nil {
			return fmt.Errorf("resurrect failed")
		}
		return fmt.Errorf("resurrect failed: %s", errPayload.Message)

	default:
		return fmt.Errorf("unexpected response type: %d", resp.Type)
	}
}

func (c *Client) sendHello() error {
	// The same TERM detection the standalone window path uses. The hello names
	// no shell: the daemon resolves it from its appearance.preferred_shell,
	// which a config reload keeps current, then $SHELL and the platform
	// default. This client runs behind every control command and shell
	// completion, so it must not load the config, write a default one, or print
	// config warnings.
	termType, colorTerm := guestenv.DetectTerm()
	// A command with no terminal on stdout (a script, a service, CI, an agent)
	// detects NoTTY and answers dumb, and a session it creates would keep
	// TERM=dumb in every pane for its life, although the panes are drawn by
	// tuios and not by this stdout. It names no TERM instead, so the daemon
	// gives the xterm-256color and truecolor an attached client's session and
	// the new-session verb already get. A real terminal that answers dumb
	// keeps its answer, as it does for a standalone window.
	if termType == "dumb" && !term.IsTerminal(int(os.Stdout.Fd())) {
		termType, colorTerm = "", ""
	}

	msg, err := NewMessage(MsgHello, &HelloPayload{
		Version:        c.version,
		Term:           termType,
		ColorTerm:      colorTerm,
		Width:          c.width,
		Height:         c.height,
		PreferredCodec: "gob", // Request gob (default)
		Protocol:       ProtocolVersion,
	})
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

	if resp.Type == MsgError {
		var errPayload ErrorPayload
		_ = resp.ParsePayload(&errPayload)
		return fmt.Errorf("the daemon refused this client: %s", errPayload.Message)
	}
	if resp.Type != MsgWelcome {
		// See TUIClient.ConnectWithCapabilities: a reply of another type means
		// the daemon numbers its messages differently.
		return numberingMismatch(c.version, resp.Type)
	}

	var welcome WelcomePayload
	if err := resp.ParsePayload(&welcome); err != nil {
		return fmt.Errorf("failed to parse welcome: %w", err)
	}

	if protocolMismatch(welcome.Protocol) {
		return daemonProtocolMismatch(c.version, &welcome)
	}

	return nil
}

func (c *Client) send(msg *Message) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()

	_ = c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return WriteMessage(c.conn, msg)
}

func (c *Client) recv() (*Message, error) {
	c.recvMu.Lock()
	defer c.recvMu.Unlock()

	_ = c.conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	return ReadMessage(c.conn)
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
