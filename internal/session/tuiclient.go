package session

import (
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

// RemoteCommandHandler is a callback for handling remote commands from the CLI.
// It receives the command payload and returns success/error.
type RemoteCommandHandler func(payload *RemoteCommandPayload) error

// QueryWindowsHandler is a callback for handling window queries from the CLI.
// It receives the query payload and should return a WindowListPayload.
type QueryWindowsHandler func(requestID string) *WindowListPayload

// QuerySessionHandler is a callback for handling session queries from the CLI.
// It receives the query payload and should return a SessionInfoPayload.
type QuerySessionHandler func(requestID string) *SessionInfoPayload

// Multi-client handler types
type StateSyncHandler func(state *SessionState, triggerType, sourceID string)
type ClientJoinedHandler func(clientID string, clientCount int, width, height int)
type ClientLeftHandler func(clientID string, clientCount int)
type SessionResizeHandler func(width, height, clientCount int)
type ForceRefreshHandler func(reason string)

// TUIClient is used by the TUIOS TUI to communicate with the daemon.
// It handles PTY I/O and state synchronization.
type TUIClient struct {
	conn   net.Conn
	mu     sync.Mutex
	readMu sync.Mutex

	sessionID   string
	sessionName string

	// Effective session dimensions (min of all connected clients)
	effectiveWidth  int
	effectiveHeight int

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

	// Remote command handler - called when a remote command is received
	remoteCommandHandler RemoteCommandHandler
	remoteCommandMu      sync.RWMutex

	// Query handlers - called when the CLI queries for information
	queryWindowsHandler QueryWindowsHandler
	querySessionHandler QuerySessionHandler
	queryHandlersMu     sync.RWMutex

	// Multi-client handlers
	stateSyncHandler     StateSyncHandler
	clientJoinedHandler  ClientJoinedHandler
	clientLeftHandler    ClientLeftHandler
	sessionResizeHandler SessionResizeHandler
	forceRefreshHandler  ForceRefreshHandler
	multiClientMu        sync.RWMutex

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

// ClientCapabilities holds terminal graphics capabilities detected from the client's terminal.
type ClientCapabilities struct {
	PixelWidth    int
	PixelHeight   int
	CellWidth     int
	CellHeight    int
	KittyGraphics bool
	SixelGraphics bool
	TerminalName  string
}

// Connect connects to the daemon and performs handshake.
func (c *TUIClient) Connect(version string, width, height int) error {
	return c.ConnectWithCapabilities(version, width, height, nil)
}

// ConnectWithCapabilities connects to the daemon and performs handshake with graphics capabilities.
func (c *TUIClient) ConnectWithCapabilities(version string, width, height int, caps *ClientCapabilities) error {
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

	// Build hello payload with capabilities
	hello := &HelloPayload{
		Version:        version,
		Width:          width,
		Height:         height,
		PreferredCodec: "gob",
	}

	// Add graphics capabilities if provided
	if caps != nil {
		hello.PixelWidth = caps.PixelWidth
		hello.PixelHeight = caps.PixelHeight
		hello.CellWidth = caps.CellWidth
		hello.CellHeight = caps.CellHeight
		hello.KittyGraphics = caps.KittyGraphics
		hello.SixelGraphics = caps.SixelGraphics
		hello.TerminalName = caps.TerminalName
	}

	// Send hello with capabilities
	msg, err := NewMessageWithCodec(MsgHello, hello, c.codec)
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
		c.effectiveWidth = payload.Width
		c.effectiveHeight = payload.Height
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

// UnsubscribePTY removes the PTY output handler and tells the daemon to stop streaming.
func (c *TUIClient) UnsubscribePTY(ptyID string) {
	c.ptyHandlersMu.Lock()
	delete(c.ptyHandlers, ptyID)
	c.ptyHandlersMu.Unlock()

	// Send unsubscribe message to daemon to stop streaming
	msg, err := NewMessageWithCodec(MsgUnsubscribePTY, &UnsubscribePTYPayload{PTYID: ptyID}, c.codec)
	if err != nil {
		return // Silent failure - handler already removed locally
	}
	_ = c.send(msg)
}

// OnPTYClosed registers a handler to be called when the PTY process exits.
func (c *TUIClient) OnPTYClosed(ptyID string, handler func()) {
	c.ptyClosedHandlersMu.Lock()
	c.ptyClosedHandlers[ptyID] = handler
	c.ptyClosedHandlersMu.Unlock()
}

// OnRemoteCommand registers a handler for remote commands from the CLI.
// The handler should execute the command and return an error if it fails.
func (c *TUIClient) OnRemoteCommand(handler RemoteCommandHandler) {
	c.remoteCommandMu.Lock()
	c.remoteCommandHandler = handler
	c.remoteCommandMu.Unlock()
}

// OnQueryWindows registers a handler for window list queries.
func (c *TUIClient) OnQueryWindows(handler QueryWindowsHandler) {
	c.queryHandlersMu.Lock()
	c.queryWindowsHandler = handler
	c.queryHandlersMu.Unlock()
}

// OnQuerySession registers a handler for session info queries.
func (c *TUIClient) OnQuerySession(handler QuerySessionHandler) {
	c.queryHandlersMu.Lock()
	c.querySessionHandler = handler
	c.queryHandlersMu.Unlock()
}

// OnStateSync registers a handler for state sync messages from other clients.
func (c *TUIClient) OnStateSync(handler StateSyncHandler) {
	c.multiClientMu.Lock()
	c.stateSyncHandler = handler
	c.multiClientMu.Unlock()
}

// OnClientJoined registers a handler for when another client joins the session.
func (c *TUIClient) OnClientJoined(handler ClientJoinedHandler) {
	c.multiClientMu.Lock()
	c.clientJoinedHandler = handler
	c.multiClientMu.Unlock()
}

// OnClientLeft registers a handler for when another client leaves the session.
func (c *TUIClient) OnClientLeft(handler ClientLeftHandler) {
	c.multiClientMu.Lock()
	c.clientLeftHandler = handler
	c.multiClientMu.Unlock()
}

// OnSessionResize registers a handler for session resize messages.
// This is called when the effective session size changes (min of all clients).
func (c *TUIClient) OnSessionResize(handler SessionResizeHandler) {
	c.multiClientMu.Lock()
	c.sessionResizeHandler = handler
	c.multiClientMu.Unlock()
}

// OnForceRefresh registers a handler for force refresh messages.
func (c *TUIClient) OnForceRefresh(handler ForceRefreshHandler) {
	c.multiClientMu.Lock()
	c.forceRefreshHandler = handler
	c.multiClientMu.Unlock()
}

// SendWindowList sends a window list response back to the daemon.
func (c *TUIClient) SendWindowList(payload *WindowListPayload) error {
	msg, err := NewMessageWithCodec(MsgWindowList, payload, c.codec)
	if err != nil {
		return err
	}
	return c.send(msg)
}

// SendSessionInfo sends a session info response back to the daemon.
func (c *TUIClient) SendSessionInfo(payload *SessionInfoPayload) error {
	msg, err := NewMessageWithCodec(MsgSessionInfo, payload, c.codec)
	if err != nil {
		return err
	}
	return c.send(msg)
}

// SendCommandResult sends the result of a remote command execution back to the daemon.
func (c *TUIClient) SendCommandResult(requestID string, success bool, message string) error {
	return c.SendCommandResultWithData(requestID, success, message, nil)
}

// SendCommandResultWithData sends the result with optional structured data.
func (c *TUIClient) SendCommandResultWithData(requestID string, success bool, message string, data map[string]any) error {
	msg, err := NewMessageWithCodec(MsgCommandResult, &CommandResultPayload{
		RequestID: requestID,
		Success:   success,
		Message:   message,
		Data:      data,
	}, c.codec)
	if err != nil {
		return err
	}
	return c.send(msg)
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

// NotifyTerminalSize notifies the daemon of this client's terminal size.
// This is used for multi-client size calculation (effective size = min of all clients).
// Called when the terminal is resized.
func (c *TUIClient) NotifyTerminalSize(width, height int) error {
	// Send resize with empty PTYID to indicate client terminal resize
	msg, err := NewMessageWithCodec(MsgResize, &ResizePTYPayload{
		PTYID:  "", // Empty = client terminal resize, not PTY resize
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

	case MsgRemoteCommand:
		// Remote command from CLI routed through daemon
		var payload RemoteCommandPayload
		if err := msg.ParsePayloadWithCodec(&payload, c.codec); err != nil {
			debugLog("[REMOTE] Failed to parse remote command: %v", err)
			return
		}

		debugLog("[REMOTE] Received command: type=%s, tapeCmd=%s, args=%v, keys=%s", payload.CommandType, payload.TapeCommand, payload.TapeArgs, payload.Keys)

		c.remoteCommandMu.RLock()
		handler := c.remoteCommandHandler
		c.remoteCommandMu.RUnlock()

		if handler != nil {
			debugLog("[REMOTE] Executing command with handler")
			if err := handler(&payload); err != nil {
				debugLog("[REMOTE] Command handler error: %v", err)
				// Only send error result here - success results are sent by the actual command handler
				// in update.go after the command executes (with proper data)
				_ = c.SendCommandResult(payload.RequestID, false, err.Error())
			}
			// Don't send success result here - let update.go send it with the actual data
		} else {
			debugLog("[REMOTE] No handler registered for remote commands")
		}

	case MsgQueryWindows:
		// Query for window list
		var payload QueryWindowsPayload
		if err := msg.ParsePayloadWithCodec(&payload, c.codec); err != nil {
			debugLog("[QUERY] Failed to parse query windows: %v", err)
			return
		}

		c.queryHandlersMu.RLock()
		handler := c.queryWindowsHandler
		c.queryHandlersMu.RUnlock()

		if handler != nil {
			result := handler(payload.RequestID)
			if result != nil {
				_ = c.SendWindowList(result)
			}
		}

	case MsgQuerySession:
		// Query for session info
		var payload QuerySessionPayload
		if err := msg.ParsePayloadWithCodec(&payload, c.codec); err != nil {
			debugLog("[QUERY] Failed to parse query session: %v", err)
			return
		}

		c.queryHandlersMu.RLock()
		handler := c.querySessionHandler
		c.queryHandlersMu.RUnlock()

		if handler != nil {
			result := handler(payload.RequestID)
			if result != nil {
				_ = c.SendSessionInfo(result)
			}
		}

	case MsgStateSync:
		// Another client updated the session state
		var payload StateSyncPayload
		if err := msg.ParsePayloadWithCodec(&payload, c.codec); err != nil {
			debugLog("[MULTICLIENT] Failed to parse state sync: %v", err)
			return
		}

		c.multiClientMu.RLock()
		handler := c.stateSyncHandler
		c.multiClientMu.RUnlock()

		if handler != nil {
			handler(payload.State, payload.TriggerType, payload.SourceID)
		}

	case MsgClientJoined:
		// Another client joined the session
		var payload ClientJoinedPayload
		if err := msg.ParsePayloadWithCodec(&payload, c.codec); err != nil {
			debugLog("[MULTICLIENT] Failed to parse client joined: %v", err)
			return
		}

		c.multiClientMu.RLock()
		handler := c.clientJoinedHandler
		c.multiClientMu.RUnlock()

		if handler != nil {
			handler(payload.ClientID, payload.ClientCount, payload.Width, payload.Height)
		}

	case MsgClientLeft:
		// Another client left the session
		var payload ClientLeftPayload
		if err := msg.ParsePayloadWithCodec(&payload, c.codec); err != nil {
			debugLog("[MULTICLIENT] Failed to parse client left: %v", err)
			return
		}

		c.multiClientMu.RLock()
		handler := c.clientLeftHandler
		c.multiClientMu.RUnlock()

		if handler != nil {
			handler(payload.ClientID, payload.ClientCount)
		}

	case MsgSessionResize:
		// Session effective size changed (min of all clients)
		var payload SessionResizePayload
		if err := msg.ParsePayloadWithCodec(&payload, c.codec); err != nil {
			debugLog("[MULTICLIENT] Failed to parse session resize: %v", err)
			return
		}

		// Update stored effective dimensions
		c.effectiveWidth = payload.Width
		c.effectiveHeight = payload.Height

		c.multiClientMu.RLock()
		handler := c.sessionResizeHandler
		c.multiClientMu.RUnlock()

		if handler != nil {
			handler(payload.Width, payload.Height, payload.ClientCount)
		}

	case MsgForceRefresh:
		// Force a re-render
		var payload ForceRefreshPayload
		if err := msg.ParsePayloadWithCodec(&payload, c.codec); err != nil {
			debugLog("[MULTICLIENT] Failed to parse force refresh: %v", err)
			return
		}

		c.multiClientMu.RLock()
		handler := c.forceRefreshHandler
		c.multiClientMu.RUnlock()

		if handler != nil {
			handler(payload.Reason)
		}
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

// EffectiveWidth returns the effective session width (min of all connected clients).
// Returns 0 if not yet set (before attach).
func (c *TUIClient) EffectiveWidth() int {
	return c.effectiveWidth
}

// EffectiveHeight returns the effective session height (min of all connected clients).
// Returns 0 if not yet set (before attach).
func (c *TUIClient) EffectiveHeight() int {
	return c.effectiveHeight
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
