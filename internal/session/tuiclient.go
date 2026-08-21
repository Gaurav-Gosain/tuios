package session

import (
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"sync"
	"sync/atomic"
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

// SessionEndedHandler is called when the daemon reports that the attached
// session was terminated. It runs on the read-loop goroutine, so the handler
// should only signal the UI (e.g. p.Send), never mutate model state directly.
type SessionEndedHandler func(sessionName, reason string)

// DisconnectHandler is called once when the read loop tears the connection down
// because of an unexpected disconnect (daemon crash, reset, or framing desync).
// It is not called for an app-initiated Close.
type DisconnectHandler func(err error)

// TUIClient is used by the TUIOS TUI to communicate with the daemon.
// It handles PTY I/O and state synchronization.
type TUIClient struct {
	conn   net.Conn
	mu     sync.Mutex
	readMu sync.Mutex

	sessionID   string
	sessionName string

	// Cached session listing from the daemon, including each session's window
	// summaries once fetched. Seeded name-only from the welcome message and kept
	// current by RefreshSessionList. Guarded by mu.
	availableSessions []SessionInfo

	// cacheGen bumps every time availableSessions changes, so the sidebar can
	// tell whether foreign-session data moved without locking mu per frame.
	cacheGen atomic.Uint64

	// Sessions this client learned by attaching to (creating) them, kept until a
	// daemon listing that could have seen them arrives. localGen counts those
	// additions so a listing already in flight is recognisable as a picture older
	// than what the cache knows. Guarded by mu.
	localSessions []string
	localGen      uint64

	// refreshInFlight drops overlapping background refreshes so a periodic
	// sidebar refresh cannot pile up requests on a busy daemon.
	refreshInFlight atomic.Bool

	// Codec negotiated with daemon (gob by default)
	codec Codec

	// PTY output handlers (raw-byte path)
	ptyHandlers   map[string]func([]byte)
	ptyHandlersMu sync.RWMutex

	// PTY closed handlers - called when a PTY process exits
	ptyClosedHandlers   map[string]func()
	ptyClosedHandlersMu sync.RWMutex

	// ptyResizeHandlers is told the size the daemon's emulator took, at the
	// point in the output stream it took it. Registered alongside the output
	// handler because the two are one stream and their order is the contract.
	ptyResizeHandlers   map[string]func(width, height int)
	ptyResizeHandlersMu sync.RWMutex

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
	disconnectHandler    DisconnectHandler
	sessionEndedHandler  SessionEndedHandler
	sessionEndedOnce     sync.Once // gates the single session-ended notification
	disconnectOnce       sync.Once // gates the single disconnect notification
	multiClientMu        sync.RWMutex

	// Request/response handling for synchronous calls after readLoop starts
	pendingResponses   map[MessageType]chan *Message
	pendingResponsesMu sync.Mutex

	// roundTripMu keeps at most one sendAndWaitResponse outstanding at a time.
	// The read loop demuxes replies by MessageType alone, so two overlapping
	// round-trips awaiting a shared type (MsgError, which every request can
	// return, or the MsgSessionList shared by list/kill/refresh) would overwrite
	// each other's pendingResponses slot and misroute a reply. The background
	// session poll runs on its own goroutine, so it is the one caller that can
	// overlap a UI-goroutine round-trip; serializing here removes the collision
	// without threading a correlation id through the protocol.
	roundTripMu sync.Mutex

	// State
	readLoopRunning bool
	done            chan struct{}
	doneOnce        sync.Once  // gates close(done) so Close is idempotent
	switchMu        sync.Mutex // prevents concurrent SwitchSession calls
}

// NewTUIClient creates a new TUI client for daemon communication.
func NewTUIClient() *TUIClient {
	return &TUIClient{
		codec:             DefaultCodec(), // gob by default
		ptyHandlers:       make(map[string]func([]byte)),
		ptyClosedHandlers: make(map[string]func()),
		ptyResizeHandlers: make(map[string]func(int, int)),
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

	// Build hello payload with capabilities
	hello := &HelloPayload{
		Version:        version,
		Width:          width,
		Height:         height,
		PreferredCodec: "gob",
		Protocol:       ProtocolVersion,
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

	if resp.Type == MsgError {
		// The only thing the daemon refuses at hello is the handshake itself,
		// and its message already names the fix.
		var errPayload ErrorPayload
		_ = resp.ParsePayloadWithCodec(&errPayload, c.codec)
		_ = conn.Close()
		return fmt.Errorf("the daemon refused this client: %s", errPayload.Message)
	}
	if resp.Type != MsgWelcome {
		// A daemon that numbers its messages differently answers the hello with
		// a type this build calls something else. That is exactly what a
		// pre-v0.8.0 daemon does, since its MsgWelcome is 22 and this build's is
		// 23, and it is the same fault the version check exists for, so it is
		// reported the same way rather than as a type number nobody can act on.
		_ = conn.Close()
		return numberingMismatch(version, resp.Type)
	}

	// Parse welcome to get negotiated codec
	var welcome WelcomePayload
	if err := resp.ParsePayloadWithCodec(&welcome, c.codec); err != nil {
		_ = conn.Close()
		return fmt.Errorf("failed to parse welcome: %w", err)
	}

	// Refuse before anything else is exchanged. Proceeding on a protocol the
	// other side does not speak turns a clear message here into a decode error
	// or a stall somewhere far from its cause.
	if protocolMismatch(welcome.Protocol) {
		_ = conn.Close()
		return daemonProtocolMismatch(version, &welcome)
	}

	// Update codec based on what server negotiated
	c.codec = NegotiateCodec(welcome.Codec)

	// Seed the cache name-only; window summaries fill in on the first refresh.
	infos := make([]SessionInfo, 0, len(welcome.SessionNames))
	for _, name := range welcome.SessionNames {
		infos = append(infos, SessionInfo{Name: name})
	}
	c.UpdateSessionCache(infos)

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
		c.NoteSession(payload.SessionName)
		return payload.State, nil

	case MsgError:
		var errPayload ErrorPayload
		_ = resp.ParsePayloadWithCodec(&errPayload, c.codec)
		return nil, &attachRefused{msg: errPayload.Message}

	default:
		return nil, fmt.Errorf("unexpected response: %d", resp.Type)
	}
}

// attachRefused reports an attach the daemon answered and declined, as opposed
// to one that never got an answer at all. Only the first is worth a second round
// trip: if the daemon is not replying, asking it again doubles the wait for an
// answer that is not coming.
type attachRefused struct{ msg string }

func (e *attachRefused) Error() string { return "attach failed: " + e.msg }

// Detach detaches from the current session.
func (c *TUIClient) Detach() error {
	msg, err := NewMessageWithCodec(MsgDetach, nil, c.codec)
	if err != nil {
		return err
	}
	return c.send(msg)
}

// SwitchRollback reports a session switch that failed after the detach had
// already been taken, which is the only failure a caller cannot simply ignore:
// at that instant the connection holds no session.
//
// State non-nil means the client is back on Prev and the caller must rebuild
// from that state, because detaching drops every PTY subscription: the panes it
// still holds are pictures of shells, not shells. State nil means the return
// failed too and the connection is attached to nothing.
type SwitchRollback struct {
	Target string
	Prev   string
	// State is the previous session as the daemon handed it back, when the
	// return attach succeeded.
	State *SessionState
	// Err is why the switch failed.
	Err error
	// Recovery is why the return failed, set only when State is nil.
	Recovery error
}

func (e *SwitchRollback) Error() string {
	if e.State != nil {
		return fmt.Sprintf("switch to %q failed: %v; still on %q", e.Target, e.Err, e.Prev)
	}
	return fmt.Sprintf("switch to %q failed: %v; could not return to %q either (%v). Reattach with 'tuios attach %s'",
		e.Target, e.Err, e.Prev, e.Recovery, e.Prev)
}

func (e *SwitchRollback) Unwrap() error { return e.Err }

// SwitchSession detaches from the current session and attaches to another.
// Safe to call while the read loop is running. Serialized via mutex.
//
// The detach cannot be taken back on its own: the daemon drops every
// subscription and every resume position with it. So an attach that fails after
// it does not simply return; it attaches back to where the client came from and
// says so in a *SwitchRollback, and the caller rebuilds from the state that
// comes with it. A switch the daemon refuses costs the user the switch, not the
// session.
func (c *TUIClient) SwitchSession(targetName string, width, height int) (*SessionState, error) {
	c.switchMu.Lock()
	defer c.switchMu.Unlock()

	prevName := c.sessionName
	debugLog("[SWITCH] Starting session switch to %q", targetName)

	// 1. Detach (fire-and-forget, daemon sends MsgDetached back)
	detachMsg, err := NewMessageWithCodec(MsgDetach, nil, c.codec)
	if err != nil {
		return nil, fmt.Errorf("detach encode: %w", err)
	}

	// Register for detach response before sending
	detachResp := make(chan *Message, 1)
	c.pendingResponsesMu.Lock()
	c.pendingResponses[MsgDetached] = detachResp
	c.pendingResponsesMu.Unlock()

	if err := c.send(detachMsg); err != nil {
		return nil, fmt.Errorf("detach send: %w", err)
	}

	debugLog("[SWITCH] Detach sent, waiting for confirmation...")

	// Wait for detach confirmation
	select {
	case <-detachResp:
		debugLog("[SWITCH] Detach confirmed")
	case <-time.After(5 * time.Second):
		return nil, fmt.Errorf("detach timeout")
	case <-c.done:
		return nil, fmt.Errorf("client closed")
	}

	// Clean up pending response registration
	c.pendingResponsesMu.Lock()
	delete(c.pendingResponses, MsgDetached)
	c.pendingResponsesMu.Unlock()

	// 2. Attach to new session using sendAndWaitResponse
	debugLog("[SWITCH] Attaching to session %q (%dx%d)", targetName, width, height)
	state, err := c.attachWhileReading(targetName, true, width, height)
	if err == nil {
		windowCount := 0
		if state != nil {
			windowCount = len(state.Windows)
		}
		debugLog("[SWITCH] Attached to %q (%d windows)", c.sessionName, windowCount)
		return state, nil
	}

	// 3. The detach was taken and the attach was not, so this connection holds
	// no session. Go back before reporting. The return does not create: an empty
	// namesake would be a different session wearing the user's session's name,
	// and saying what happened is better than that.
	rollback := &SwitchRollback{Target: targetName, Prev: prevName, Err: err}
	if prevName == "" {
		rollback.Recovery = errors.New("the client held no named session to return to")
		return nil, rollback
	}
	var refused *attachRefused
	if !errors.As(err, &refused) {
		// The daemon did not answer, so a second round trip would only spend
		// another timeout waiting for an answer that is not coming.
		rollback.Recovery = errors.New("the daemon is not answering, so no return was attempted")
		return nil, rollback
	}
	prevState, rerr := c.attachWhileReading(prevName, false, width, height)
	if rerr != nil {
		rollback.Recovery = rerr
		debugLog("[SWITCH] Return to %q failed: %v", prevName, rerr)
		return nil, rollback
	}
	rollback.State = prevState
	debugLog("[SWITCH] Returned to %q", prevName)
	return nil, rollback
}

// attachWhileReading performs the attach round trip through the read loop, for
// the paths that run with the read loop already started.
func (c *TUIClient) attachWhileReading(name string, createNew bool, width, height int) (*SessionState, error) {
	msg, err := NewMessageWithCodec(MsgAttach, &AttachPayload{
		SessionName: name,
		CreateNew:   createNew,
		Width:       width,
		Height:      height,
	}, c.codec)
	if err != nil {
		return nil, fmt.Errorf("attach encode: %w", err)
	}

	resp, err := c.sendAndWaitResponse(msg, MsgAttached, MsgError)
	if err != nil {
		return nil, fmt.Errorf("attach: %w", err)
	}

	switch resp.Type {
	case MsgAttached:
		var payload AttachedPayload
		if err := resp.ParsePayloadWithCodec(&payload, c.codec); err != nil {
			return nil, err
		}
		c.sessionID = payload.SessionID
		c.sessionName = payload.SessionName
		c.NoteSession(payload.SessionName)
		return payload.State, nil

	case MsgError:
		var errPayload ErrorPayload
		_ = resp.ParsePayloadWithCodec(&errPayload, c.codec)
		return nil, &attachRefused{msg: errPayload.Message}

	default:
		return nil, fmt.Errorf("unexpected response: %d", resp.Type)
	}
}

// CreatePTY creates a new PTY in the session. windowID, if non-empty, is the
// client-side window UUID exported to the shell as TUIOS_WINDOW_ID.
func (c *TUIClient) CreatePTY(title, windowID string, width, height int) (string, error) {
	msg, err := NewMessageWithCodec(MsgCreatePTY, &CreatePTYPayload{
		Title:    title,
		Width:    width,
		Height:   height,
		WindowID: windowID,
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
// The handler receives raw byte streams (MsgPTYOutput). fromSeq is the stream
// position the caller's emulator has been restored to, so the daemon replays
// only what came after it; zero leaves the resume position to the daemon.
func (c *TUIClient) SubscribePTY(ptyID string, fromSeq int64, handler func([]byte)) error {
	c.ptyHandlersMu.Lock()
	c.ptyHandlers[ptyID] = handler
	c.ptyHandlersMu.Unlock()

	msg, err := NewMessageWithCodec(MsgSubscribePTY, &SubscribePTYPayload{PTYID: ptyID, FromSeq: fromSeq}, c.codec)
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

	c.ptyResizeHandlersMu.Lock()
	delete(c.ptyResizeHandlers, ptyID)
	c.ptyResizeHandlersMu.Unlock()

	// Send unsubscribe message to daemon to stop streaming
	msg, err := NewMessageWithCodec(MsgUnsubscribePTY, &UnsubscribePTYPayload{PTYID: ptyID}, c.codec)
	if err != nil {
		return // Silent failure - handler already removed locally
	}
	_ = c.send(msg)
}

// OnPTYResized registers a handler for the size the daemon's emulator took.
// It is called on the read loop, in the same order as the output handler, so a
// caller that applies both in the order it is told them lays out every byte at
// the width the daemon laid it out at.
func (c *TUIClient) OnPTYResized(ptyID string, handler func(width, height int)) {
	c.ptyResizeHandlersMu.Lock()
	c.ptyResizeHandlers[ptyID] = handler
	c.ptyResizeHandlersMu.Unlock()
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

// OnSessionEnded registers a handler invoked when the daemon reports that the
// attached session was terminated. It fires at most once per client.
func (c *TUIClient) OnSessionEnded(handler SessionEndedHandler) {
	c.multiClientMu.Lock()
	c.sessionEndedHandler = handler
	c.multiClientMu.Unlock()
}

// OnDisconnect registers a handler invoked when the daemon connection is torn
// down unexpectedly (crash, reset, or framing desync). It fires at most once and
// runs on the read-loop goroutine, so the handler should only signal the UI
// (e.g. p.Send), never mutate model state directly.
func (c *TUIClient) OnDisconnect(handler DisconnectHandler) {
	c.multiClientMu.Lock()
	c.disconnectHandler = handler
	c.multiClientMu.Unlock()
}

// handleDisconnect tears the connection down and notifies the app exactly once.
// If Close was already called by the app, the disconnect is expected teardown
// and no notification fires.
func (c *TUIClient) handleDisconnect(err error) {
	select {
	case <-c.done:
		// App-initiated Close already ran; stay quiet.
		return
	default:
	}
	_ = c.Close() // closes done + conn, idempotent
	c.disconnectOnce.Do(func() {
		c.multiClientMu.RLock()
		handler := c.disconnectHandler
		c.multiClientMu.RUnlock()
		if handler != nil {
			handler(err)
		}
	})
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

// SendIntent asks the daemon to perform a session mutation on this client's
// behalf. It is the keyboard's route to the same operations the CLI reaches over
// the verb protocol: commandType and args are the verb vocabulary, so a
// keystroke and a `tuios run-command` do not merely agree, they are the same
// call.
//
// It does not wait for a result. The mutation's effect arrives as a state push
// like any other daemon-side change, which is the only channel a client applies
// state from; a second, synchronous answer would be a second way to learn the
// same news. A send error means the socket is gone, which the read loop is
// already reporting as a disconnect.
func (c *TUIClient) SendIntent(commandType string, args ...string) error {
	msg, err := NewMessageWithCodec(MsgExecuteCommand, &ExecuteCommandPayload{
		SessionName: c.sessionName,
		CommandType: commandType,
		Args:        args,
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
	return c.KillSessionByName(c.sessionName)
}

// KillSessionByName terminates a session by name (can be any session, not just
// current). It waits for the daemon's post-kill session list and refreshes the
// cached names the UI reads, so a killed session leaves the switcher and sidebar
// at once instead of lingering until the next unrelated refresh. Waiting for the
// real reply also surfaces a daemon rejection (an unknown name) as an error
// rather than the phantom success the old fire-and-forget send always reported.
func (c *TUIClient) KillSessionByName(name string) error {
	if name == "" {
		return fmt.Errorf("session name cannot be empty")
	}
	msg, err := NewMessageWithCodec(MsgKill, &KillPayload{
		SessionName: name,
	}, c.codec)
	if err != nil {
		return err
	}
	stamp := c.listingStamp()
	resp, err := c.sendAndWaitResponse(msg, MsgSessionList, MsgError)
	if err != nil {
		return err
	}
	if resp.Type == MsgError {
		var errPayload ErrorPayload
		_ = resp.ParsePayloadWithCodec(&errPayload, c.codec)
		return fmt.Errorf("kill session: %s", errPayload.Message)
	}
	var payload SessionListPayload
	if err := resp.ParsePayloadWithCodec(&payload, c.codec); err != nil {
		return err
	}
	c.applySessionListing(payload.Sessions, stamp)
	return nil
}

// GetTerminalState retrieves the terminal state for a PTY. maxScrollback bounds
// the scrollback rows the daemon includes: negative for none, zero for the
// default, or a count. This is used when attaching to restore terminal content.
//
// have is how many scrollback rows this client's emulator already holds for the
// pane. The daemon sends only the rows past it, which is all this client can
// use: an emulator that survived keeps its own history and merges just what
// scrolled off while it was away. Pass zero for a fresh emulator.
func (c *TUIClient) GetTerminalState(ptyID string, maxScrollback, have int) (*TerminalState, error) {
	msg, err := NewMessageWithCodec(MsgGetTerminalState, &GetTerminalStatePayload{
		PTYID:              ptyID,
		IncludeScrollback:  maxScrollback >= 0,
		MaxScrollbackLines: max(maxScrollback, 0),
		HaveScrollback:     max(have, 0),
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
	defer func() {
		if r := recover(); r != nil {
			debugLog("[CLIENT] PANIC in readLoop: %v", r)
			// Don't crash the whole app  - log and try to continue
		}
	}()
	for {
		select {
		case <-c.done:
			return
		default:
		}

		c.readMu.Lock()
		// Short deadline detects the message boundary for done-channel checks;
		// the body then gets a longer deadline so a large payload cannot be cut
		// mid-frame and desync framing.
		msg, _, err := ReadMessageConn(c.conn, 100*time.Millisecond, 30*time.Second)
		c.readMu.Unlock()

		if err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				// Deadline hit at a message boundary; loop to re-check c.done.
				continue
			}
			if errors.Is(err, io.EOF) {
				// Clean daemon-side close.
				c.handleDisconnect(err)
				return
			}
			// Any other error is fatal and non-recoverable: a daemon crash
			// (ECONNRESET) or a framing desync ("message too large" leaves the
			// payload in the stream) makes every subsequent read fail instantly.
			// Continuing would busy-loop at 100% CPU forever, so tear the
			// connection down and surface a clean disconnect instead.
			debugLog("[CLIENT] readLoop fatal error, disconnecting: %v", err)
			c.handleDisconnect(err)
			return
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

	case MsgPTYResized:
		var payload PTYResizedPayload
		if err := msg.ParsePayloadWithCodec(&payload, c.codec); err != nil {
			return
		}
		c.ptyResizeHandlersMu.RLock()
		resized := c.ptyResizeHandlers[payload.PTYID]
		c.ptyResizeHandlersMu.RUnlock()
		if resized != nil {
			resized(payload.Width, payload.Height)
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

		c.ptyResizeHandlersMu.Lock()
		delete(c.ptyResizeHandlers, payload.PTYID)
		c.ptyResizeHandlersMu.Unlock()

		// Call the closed handler to notify window
		if closedHandler != nil {
			closedHandler()
		}

	case MsgSessionEnded:
		// The attached session was destroyed (killed from the CLI, from another
		// client, or over the control plane). Notify once so the app can exit;
		// the connection itself is still usable, so it is not torn down here.
		var payload SessionEndedPayload
		if err := msg.ParsePayloadWithCodec(&payload, c.codec); err != nil {
			debugLog("[CLIENT] Failed to parse session ended: %v", err)
		}
		name := payload.SessionName
		if name == "" {
			name = c.SessionName()
		}
		c.sessionEndedOnce.Do(func() {
			c.multiClientMu.RLock()
			handler := c.sessionEndedHandler
			c.multiClientMu.RUnlock()
			if handler != nil {
				handler(name, payload.Reason)
			}
		})

	case MsgDetached:
		// Session detached  - handled via pendingResponses in SwitchSession.
		// Do NOT close c.done here; it must stay open for subsequent switches.
		debugLog("[CLIENT] Received MsgDetached (no-op in handleMessage)")

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

// Close closes the connection to the daemon. Idempotent: the done channel is
// closed at most once, so concurrent or repeated Close calls cannot panic.
func (c *TUIClient) Close() error {
	c.doneOnce.Do(func() { close(c.done) })

	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// SessionName returns the attached session name.
func (c *TUIClient) SessionName() string {
	return c.sessionName
}

// AvailableSessionNames returns the list of available sessions from the daemon.
func (c *TUIClient) AvailableSessionNames() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	names := make([]string, 0, len(c.availableSessions))
	for _, s := range c.availableSessions {
		names = append(names, s.Name)
	}
	return names
}

// SessionLabel returns the cached display name and accent for the named
// session, both empty when it is unknown or carries neither. The caller falls
// back to the session name, which stays the identity in every case.
func (c *TUIClient) SessionLabel(name string) (display, accent string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, s := range c.availableSessions {
		if s.Name == name {
			return s.DisplayName, s.Accent
		}
	}
	return "", ""
}

// SessionRestored reports whether the named session came back from saved state
// and has not been attached to since, from the cached listing. False for an
// unknown session and for an older daemon that does not send the field, which
// is the same silence a surface had before the field existed.
func (c *TUIClient) SessionRestored(name string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, s := range c.availableSessions {
		if s.Name == name {
			return s.Restored
		}
	}
	return false
}

// SessionCurrentWorkspace is the workspace the named session is showing, from
// the cached listing, or 0 when it is unknown: an older daemon does not send
// the field, and a surface reading zero simply says nothing about where that
// session's panes live rather than saying something wrong.
func (c *TUIClient) SessionCurrentWorkspace(name string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, s := range c.availableSessions {
		if s.Name == name {
			return s.CurrentWorkspace
		}
	}
	return 0
}

// SessionCount returns how many sessions the cache currently knows about. The
// client gates its foreign-session poll on this so a lone-session client makes
// no network round trips at idle.
func (c *TUIClient) SessionCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.availableSessions)
}

// CachedSessions returns the cached listing as it stands, each session with the
// window summaries it was listed with. A reader needing more than one field of
// an entry takes this instead of one accessor per field. The slice is a copy,
// and a refresh replaces entries rather than editing them, so the summaries it
// shares cannot change underneath the caller.
func (c *TUIClient) CachedSessions() []SessionInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]SessionInfo(nil), c.availableSessions...)
}

// SessionWindows returns the cached per-window summaries for the named session,
// or nil if the session is unknown or its windows have not been fetched yet. The
// sidebar reads this to expand a non-attached session's tree without a blocking
// round trip.
func (c *TUIClient) SessionWindows(name string) []WindowSummary {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, s := range c.availableSessions {
		if s.Name == name {
			return append([]WindowSummary(nil), s.Windows...)
		}
	}
	return nil
}

// TryRefreshSessionList refreshes the cache on the caller's goroutine unless a
// refresh is already running, in which case it returns immediately. It is the
// entry point for the periodic background refresh; call it from a tea.Cmd so it
// never runs on the UI goroutine. Errors are swallowed: a failed refresh leaves
// the last good cache in place.
func (c *TUIClient) TryRefreshSessionList() {
	if !c.refreshInFlight.CompareAndSwap(false, true) {
		return
	}
	defer c.refreshInFlight.Store(false)
	_, _ = c.RefreshSessionList()
}

// CreateDetachedSession asks the daemon for a headless session with an initial
// window, the same request `tuios new` makes over a control connection. The
// daemon answers with the refreshed listing, which is applied to the cache so
// the new session is in the rail before the next poll rather than after it.
func (c *TUIClient) CreateDetachedSession(name string, width, height int) error {
	msg, err := NewMessageWithCodec(MsgNew, &NewPayload{
		SessionName: name,
		Width:       width,
		Height:      height,
		Detach:      true,
	}, c.codec)
	if err != nil {
		return err
	}
	stamp := c.listingStamp()
	resp, err := c.sendAndWaitResponse(msg, MsgSessionList, MsgError)
	if err != nil {
		return err
	}
	if resp.Type == MsgError {
		var errPayload ErrorPayload
		_ = resp.ParsePayloadWithCodec(&errPayload, c.codec)
		return fmt.Errorf("create session: %s", errPayload.Message)
	}
	var payload SessionListPayload
	if err := resp.ParsePayloadWithCodec(&payload, c.codec); err != nil {
		return err
	}
	c.applySessionListing(payload.Sessions, stamp)
	return nil
}

// RefreshSessionList queries the daemon for an up-to-date session list and
// updates the cached availableSessionNames. Blocks until response arrives.
// Safe to call while the read loop is running.
func (c *TUIClient) RefreshSessionList() ([]SessionInfo, error) {
	listMsg, err := NewMessageWithCodec(MsgList, nil, c.codec)
	if err != nil {
		return nil, err
	}
	stamp := c.listingStamp()
	resp, err := c.sendAndWaitResponse(listMsg, MsgSessionList, MsgError)
	if err != nil {
		return nil, err
	}
	if resp.Type == MsgError {
		var errPayload ErrorPayload
		_ = resp.ParsePayloadWithCodec(&errPayload, c.codec)
		return nil, fmt.Errorf("list sessions: %s", errPayload.Message)
	}
	var payload SessionListPayload
	if err := resp.ParsePayloadWithCodec(&payload, c.codec); err != nil {
		return nil, err
	}
	c.applySessionListing(payload.Sessions, stamp)
	return payload.Sessions, nil
}

// UpdateSessionCache replaces the cached session listing, including each
// session's window summaries. It is the seam that seeds the cache without a live
// daemon; a listing handed in here is treated as current.
func (c *TUIClient) UpdateSessionCache(sessions []SessionInfo) {
	c.applySessionListing(sessions, c.listingStamp())
}

// listingStamp reads the local-addition counter a listing request is answered
// against. Take it before sending the request, hand it back to
// applySessionListing with the reply.
func (c *TUIClient) listingStamp() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.localGen
}

// applySessionListing installs a daemon listing over the cache. When sinceGen
// still matches, the listing is newer than everything this client knows and
// replaces the cache outright, which is what lets a killed session leave. When
// it does not, a session was created while the request was in flight and the
// reply is an older picture, so the sessions it could not have seen are kept:
// the cache must never regress below what the sidebar is already showing.
func (c *TUIClient) applySessionListing(sessions []SessionInfo, sinceGen uint64) {
	c.mu.Lock()
	if sinceGen == c.localGen {
		c.localSessions = nil
	} else {
		sessions = appendUnlisted(sessions, c.availableSessions, c.localSessions)
	}
	changed := !listingsAgree(c.availableSessions, sessions)
	c.availableSessions = sessions
	c.mu.Unlock()
	if changed {
		c.cacheGen.Add(1)
	}
}

// appendUnlisted appends the cached entry for every locally-known session the
// listing omits, so a reply that predates a just-created session does not drop
// it. Order is preserved: the local session stays last, where creation order
// put it.
func appendUnlisted(listing, cached []SessionInfo, local []string) []SessionInfo {
	for _, name := range local {
		if slices.ContainsFunc(listing, func(s SessionInfo) bool { return s.Name == name }) {
			continue
		}
		info := SessionInfo{Name: name}
		if i := slices.IndexFunc(cached, func(s SessionInfo) bool { return s.Name == name }); i >= 0 {
			info = cached[i]
		}
		listing = append(listing, info)
	}
	return listing
}

// listingsAgree reports whether two listings draw the same thing. Only the
// fields the session surfaces read are compared, so a listing that moves
// nothing on screen does not bump cacheGen and force a rail rebuild.
func listingsAgree(a, b []SessionInfo) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name || a[i].WindowCount != b[i].WindowCount {
			return false
		}
		// A rename moves no window, so without these a renamed session kept the
		// old label until something else happened to bump the generation.
		if a[i].DisplayName != b[i].DisplayName || a[i].Accent != b[i].Accent {
			return false
		}
		// Attaching to a restored session moves no window either, and the tag has
		// to come off the row when it does.
		if a[i].Restored != b[i].Restored {
			return false
		}
		if !slices.Equal(a[i].Windows, b[i].Windows) {
			return false
		}
	}
	return true
}

// NoteSession records a session this client just attached to. Attaching with
// CreateNew is how a session is born, and until the daemon is listed again
// nothing else knows it exists: the sidebar builds foreign rows from this cache,
// so without the note the new session survives only while it is the attached one
// and vanishes the moment the client switches away.
func (c *TUIClient) NoteSession(name string) {
	if name == "" {
		return
	}
	c.mu.Lock()
	if slices.ContainsFunc(c.availableSessions, func(s SessionInfo) bool { return s.Name == name }) {
		c.mu.Unlock()
		return
	}
	c.availableSessions = append(c.availableSessions, SessionInfo{Name: name})
	c.localSessions = append(c.localSessions, name)
	c.localGen++
	c.mu.Unlock()
	c.cacheGen.Add(1)
}

// CacheGen returns a counter that changes whenever the cached session listing
// changes. The sidebar folds it into its render-cache key so foreign-session
// updates rebuild the rail without locking the client mutex every frame.
func (c *TUIClient) CacheGen() uint64 {
	return c.cacheGen.Load()
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
	// Serialize round-trips so no two overlap on a shared response type. The read
	// loop never takes this lock and delivers replies before dispatching handlers,
	// and no handler issues a round-trip, so holding it across the wait cannot
	// deadlock the reader.
	c.roundTripMu.Lock()
	defer c.roundTripMu.Unlock()

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
