package session

import (
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

// TUIClient is used by the TUIOS TUI to communicate with the daemon.
// It handles PTY I/O and state synchronization.
type TUIClient struct {
	conn   net.Conn
	mu     sync.Mutex
	readMu sync.Mutex

	sessionID   string
	sessionName string

	// Available session names from daemon
	availableSessionNames []string

	// Codec negotiated with daemon (gob by default)
	codec Codec

	// PTY output handlers
	ptyHandlers   map[string]func([]byte)
	ptyHandlersMu sync.RWMutex

	// PTY closed handlers - called when a PTY process exits
	ptyClosedHandlers   map[string]func()
	ptyClosedHandlersMu sync.RWMutex

	// Request/response handling for synchronous calls after readLoop starts
	pendingResponses   map[MessageType]chan *Message
	pendingResponsesMu sync.Mutex

	// State
	connected       bool
	readLoopRunning bool
	done            chan struct{}
}

// NewTUIClient creates a new TUI client for daemon communication.
func NewTUIClient() *TUIClient {
	return &TUIClient{
		codec:             DefaultCodec(), // gob by default
		ptyHandlers:       make(map[string]func([]byte)),
		ptyClosedHandlers: make(map[string]func()),
		pendingResponses:  make(map[MessageType]chan *Message),
		done:              make(chan struct{}),
	}
}

// Connect connects to the daemon and performs handshake.
func (c *TUIClient) Connect(version string, width, height int) error {
	socketPath, err := GetSocketPath()
	if err != nil {
		return fmt.Errorf("failed to get socket path: %w", err)
	}

	conn, err := net.DialTimeout("unix", socketPath, 5*time.Second)
	if err != nil {
		return fmt.Errorf("failed to connect to daemon: %w", err)
	}
	c.conn = conn
	c.connected = true

	// Send hello with gob preference
	msg, err := NewMessageWithCodec(MsgHello, &HelloPayload{
		Version:        version,
		Width:          width,
		Height:         height,
		PreferredCodec: "gob",
	}, c.codec)
	if err != nil {
		_ = conn.Close()
		return err
	}

	if err := c.send(msg); err != nil {
		_ = conn.Close()
		return err
	}

	// Wait for welcome
	resp, err := c.recv()
	if err != nil {
		_ = conn.Close()
		return err
	}

	if resp.Type != MsgWelcome {
		_ = conn.Close()
		return fmt.Errorf("expected welcome, got %d", resp.Type)
	}

	// Parse welcome to get negotiated codec
	var welcome WelcomePayload
	if err := resp.ParsePayloadWithCodec(&welcome, c.codec); err != nil {
		_ = conn.Close()
		return fmt.Errorf("failed to parse welcome: %w", err)
	}

	// Update codec based on what server negotiated
	c.codec = NegotiateCodec(welcome.Codec)

	// Store available session names
	c.availableSessionNames = welcome.SessionNames

	return nil
}

// AttachSession attaches to a session (creates if createNew is true).
// Returns the session state for restoration.
func (c *TUIClient) AttachSession(name string, createNew bool, width, height int) (*SessionState, error) {
	msg, err := NewMessageWithCodec(MsgAttach, &AttachPayload{
		SessionName: name,
		CreateNew:   createNew,
		Width:       width,
		Height:      height,
	}, c.codec)
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

	switch resp.Type {
	case MsgAttached:
		var payload AttachedPayload
		if err := resp.ParsePayloadWithCodec(&payload, c.codec); err != nil {
			return nil, err
		}
		c.sessionID = payload.SessionID
		c.sessionName = payload.SessionName
		return payload.State, nil

	case MsgError:
		var errPayload ErrorPayload
		_ = resp.ParsePayloadWithCodec(&errPayload, c.codec)
		return nil, fmt.Errorf("attach failed: %s", errPayload.Message)

	default:
		return nil, fmt.Errorf("unexpected response: %d", resp.Type)
	}
}

// Detach detaches from the current session.
func (c *TUIClient) Detach() error {
	msg, err := NewMessageWithCodec(MsgDetach, nil, c.codec)
	if err != nil {
		return err
	}
	return c.send(msg)
}

// CreatePTY creates a new PTY in the session.
func (c *TUIClient) CreatePTY(title string, width, height int) (string, error) {
	msg, err := NewMessageWithCodec(MsgCreatePTY, &CreatePTYPayload{
		Title:  title,
		Width:  width,
		Height: height,
	}, c.codec)
	if err != nil {
		return "", err
	}

	resp, err := c.sendAndWaitResponse(msg, MsgPTYCreated, MsgError)
	if err != nil {
		return "", err
	}

	switch resp.Type {
	case MsgPTYCreated:
		var payload PTYCreatedPayload
		if err := resp.ParsePayloadWithCodec(&payload, c.codec); err != nil {
			return "", err
		}
		return payload.ID, nil

	case MsgError:
		var errPayload ErrorPayload
		_ = resp.ParsePayloadWithCodec(&errPayload, c.codec)
		return "", fmt.Errorf("create PTY failed: %s", errPayload.Message)

	default:
		return "", fmt.Errorf("unexpected response: %d", resp.Type)
	}
}

// ClosePTY closes a PTY.
func (c *TUIClient) ClosePTY(ptyID string) error {
	msg, err := NewMessageWithCodec(MsgClosePTY, &ClosePTYPayload{PTYID: ptyID}, c.codec)
	if err != nil {
		return err
	}
	return c.send(msg)
}

// SubscribePTY subscribes to PTY output and registers a handler.
func (c *TUIClient) SubscribePTY(ptyID string, handler func([]byte)) error {
	c.ptyHandlersMu.Lock()
	c.ptyHandlers[ptyID] = handler
	c.ptyHandlersMu.Unlock()

	msg, err := NewMessageWithCodec(MsgSubscribePTY, &SubscribePTYPayload{PTYID: ptyID}, c.codec)
	if err != nil {
		return err
	}
	return c.send(msg)
}

// UnsubscribePTY removes the PTY output handler.
func (c *TUIClient) UnsubscribePTY(ptyID string) {
	c.ptyHandlersMu.Lock()
	delete(c.ptyHandlers, ptyID)
	c.ptyHandlersMu.Unlock()
}

// OnPTYClosed registers a handler to be called when the PTY process exits.
func (c *TUIClient) OnPTYClosed(ptyID string, handler func()) {
	c.ptyClosedHandlersMu.Lock()
	c.ptyClosedHandlers[ptyID] = handler
	c.ptyClosedHandlersMu.Unlock()
}

// WritePTY sends input to a PTY.
func (c *TUIClient) WritePTY(ptyID string, data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	_ = c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return WritePTYInput(c.conn, ptyID, data)
}

// ResizePTY resizes a PTY.
func (c *TUIClient) ResizePTY(ptyID string, width, height int) error {
	msg, err := NewMessageWithCodec(MsgResize, &ResizePTYPayload{
		PTYID:  ptyID,
		Width:  width,
		Height: height,
	}, c.codec)
	if err != nil {
		return err
	}
	return c.send(msg)
}

// UpdateState sends a state update to the daemon.
func (c *TUIClient) UpdateState(state *SessionState) error {
	msg, err := NewMessageWithCodec(MsgUpdateState, state, c.codec)
	if err != nil {
		return err
	}
	return c.send(msg)
}

// KillSession terminates the currently attached session.
// This should be called when the user wants to quit AND kill the session.
func (c *TUIClient) KillSession() error {
	if c.sessionName == "" {
		return nil
	}
	msg, err := NewMessageWithCodec(MsgKill, &KillPayload{
		SessionName: c.sessionName,
	}, c.codec)
	if err != nil {
		return err
	}
	if err := c.send(msg); err != nil {
		return err
	}
	// Wait briefly to ensure the daemon processes the kill message
	// before we close the connection
	time.Sleep(100 * time.Millisecond)
	return nil
}

// GetTerminalState retrieves the terminal state for a PTY (screen + scrollback).
// This is used when attaching to restore terminal content.
func (c *TUIClient) GetTerminalState(ptyID string, includeScrollback bool) (*TerminalState, error) {
	msg, err := NewMessageWithCodec(MsgGetTerminalState, &GetTerminalStatePayload{
		PTYID:             ptyID,
		IncludeScrollback: includeScrollback,
	}, c.codec)
	if err != nil {
		return nil, err
	}

	resp, err := c.sendAndWaitResponse(msg, MsgTerminalState, MsgError)
	if err != nil {
		return nil, err
	}

	switch resp.Type {
	case MsgTerminalState:
		var payload TerminalStatePayload
		if err := resp.ParsePayloadWithCodec(&payload, c.codec); err != nil {
			return nil, err
		}
		return payload.State, nil

	case MsgError:
		var errPayload ErrorPayload
		_ = resp.ParsePayloadWithCodec(&errPayload, c.codec)
		return nil, fmt.Errorf("get terminal state failed: %s", errPayload.Message)

	default:
		return nil, fmt.Errorf("unexpected response: %d", resp.Type)
	}
}

// StartReadLoop starts the background goroutine that reads daemon messages.
// PTY output will be dispatched to registered handlers.
func (c *TUIClient) StartReadLoop() {
	c.readLoopRunning = true
	go c.readLoop()
}

func (c *TUIClient) readLoop() {
	for {
		select {
		case <-c.done:
			return
		default:
		}

		c.readMu.Lock()
		_ = c.conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		msg, _, err := ReadMessageWithCodec(c.conn)
		c.readMu.Unlock()

		if err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				continue
			}
			if errors.Is(err, io.EOF) {
				return
			}
			continue
		}

		// Check if there's a pending response channel for this message type
		c.pendingResponsesMu.Lock()
		if respChan, ok := c.pendingResponses[msg.Type]; ok {
			delete(c.pendingResponses, msg.Type)
			c.pendingResponsesMu.Unlock()
			// Send to the waiting caller
			select {
			case respChan <- msg:
			default:
			}
			continue
		}
		c.pendingResponsesMu.Unlock()

		// Handle message normally
		c.handleMessage(msg)
	}
}

func (c *TUIClient) handleMessage(msg *Message) {
	switch msg.Type {
	case MsgPTYOutput:
		// Try binary format first (optimized path from daemon)
		var ptyID string
		var data []byte
		ptyID, data, err := ParseBinaryPTYMessage(msg.Payload)
		if err != nil || ptyID == "" {
			// Fall back to codec format
			var payload PTYOutputPayload
			if err := msg.ParsePayloadWithCodec(&payload, c.codec); err == nil && payload.PTYID != "" {
				ptyID = payload.PTYID
				data = payload.Data
			} else {
				return
			}
		}

		c.ptyHandlersMu.RLock()
		handler := c.ptyHandlers[ptyID]
		c.ptyHandlersMu.RUnlock()

		if handler != nil {
			handler(data)
		}

	case MsgPTYClosed:
		var payload ClosePTYPayload
		if err := msg.ParsePayloadWithCodec(&payload, c.codec); err != nil {
			return
		}
		// Get the closed handler before removing
		c.ptyClosedHandlersMu.RLock()
		closedHandler := c.ptyClosedHandlers[payload.PTYID]
		c.ptyClosedHandlersMu.RUnlock()

		// Remove handlers
		c.ptyHandlersMu.Lock()
		delete(c.ptyHandlers, payload.PTYID)
		c.ptyHandlersMu.Unlock()

		c.ptyClosedHandlersMu.Lock()
		delete(c.ptyClosedHandlers, payload.PTYID)
		c.ptyClosedHandlersMu.Unlock()

		// Call the closed handler to notify window
		if closedHandler != nil {
			closedHandler()
		}

	case MsgDetached:
		// Session detached
		close(c.done)

	case MsgSessionEnded:
		// Session ended
		close(c.done)
	}
}

// Close closes the connection to the daemon.
func (c *TUIClient) Close() error {
	select {
	case <-c.done:
	default:
		close(c.done)
	}

	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// SessionName returns the attached session name.
func (c *TUIClient) SessionName() string {
	return c.sessionName
}

// IsConnected returns true if connected to daemon.
func (c *TUIClient) IsConnected() bool {
	return c.connected
}

// AvailableSessionNames returns the list of available sessions from the daemon.
func (c *TUIClient) AvailableSessionNames() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string{}, c.availableSessionNames...) // Return a copy
}

func (c *TUIClient) send(msg *Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	_ = c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return WriteMessageWithCodec(c.conn, msg, c.codec)
}

func (c *TUIClient) recv() (*Message, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()

	_ = c.conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	msg, _, err := ReadMessageWithCodec(c.conn)
	return msg, err
}

// sendAndWaitResponse sends a message and waits for a response of the expected type.
// This works even after readLoop has started by registering a pending response channel.
func (c *TUIClient) sendAndWaitResponse(msg *Message, expectedTypes ...MessageType) (*Message, error) {
	// If readLoop isn't running, use simple recv
	if !c.readLoopRunning {
		if err := c.send(msg); err != nil {
			return nil, err
		}
		return c.recv()
	}

	// Create a channel to receive the response
	respChan := make(chan *Message, 1)

	// Register for all expected response types
	c.pendingResponsesMu.Lock()
	for _, t := range expectedTypes {
		c.pendingResponses[t] = respChan
	}
	c.pendingResponsesMu.Unlock()

	// Clean up when done
	defer func() {
		c.pendingResponsesMu.Lock()
		for _, t := range expectedTypes {
			delete(c.pendingResponses, t)
		}
		c.pendingResponsesMu.Unlock()
	}()

	// Send the message
	if err := c.send(msg); err != nil {
		return nil, err
	}

	// Wait for response with timeout
	select {
	case resp := <-respChan:
		return resp, nil
	case <-time.After(30 * time.Second):
		return nil, fmt.Errorf("timeout waiting for response")
	case <-c.done:
		return nil, fmt.Errorf("client closed")
	}
}
