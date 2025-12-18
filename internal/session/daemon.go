package session

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strconv"
	"sync"
	"time"
)

// Daemon manages the persistent TUIOS server process.
// It owns PTYs and stores session state. Clients run the TUI.
type Daemon struct {
	manager  *Manager
	listener net.Listener
	ctx      context.Context
	cancel   context.CancelFunc

	// Connection tracking
	clients   map[string]*connState
	clientsMu sync.RWMutex

	// Configuration
	version string
}

// connState tracks state for a connected client.
type connState struct {
	conn       net.Conn
	clientID   string
	sessionID  string // Session they're attached to
	hello      *HelloPayload
	done       chan struct{}
	sendMu     sync.Mutex
	lastActive time.Time

	// Codec negotiated for this connection (gob by default)
	codec Codec

	// PTY subscriptions for this client
	ptySubscriptions map[string]struct{}
}

// DaemonConfig holds configuration for starting the daemon.
type DaemonConfig struct {
	Version    string
	SocketPath string
	Foreground bool
	LogFile    string
}

// NewDaemon creates a new daemon instance.
func NewDaemon(cfg *DaemonConfig) *Daemon {
	ctx, cancel := context.WithCancel(context.Background())

	d := &Daemon{
		manager: NewManager(),
		ctx:     ctx,
		cancel:  cancel,
		clients: make(map[string]*connState),
		version: cfg.Version,
	}

	if cfg.SocketPath != "" {
		d.manager.SetSocketPath(cfg.SocketPath)
	}

	return d
}

// Start starts the daemon.
func (d *Daemon) Start() error {
	socketPath := d.manager.SocketPath()

	if _, err := os.Stat(socketPath); err == nil {
		if d.isDaemonRunning(socketPath) {
			return fmt.Errorf("daemon already running at %s", socketPath)
		}
		if err := os.Remove(socketPath); err != nil {
			return fmt.Errorf("failed to remove stale socket: %w", err)
		}
	}

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("failed to listen on socket: %w", err)
	}
	d.listener = listener

	if err := os.Chmod(socketPath, 0700); err != nil {
		_ = listener.Close()
		return fmt.Errorf("failed to set socket permissions: %w", err)
	}

	if err := d.writePidFile(); err != nil {
		_ = listener.Close()
		return fmt.Errorf("failed to write PID file: %w", err)
	}

	log.Printf("TUIOS daemon started on %s (PID %d)", socketPath, os.Getpid())

	go d.handleSignals()
	go d.acceptLoop()
	go d.cleanupLoop()

	return nil
}

// Run starts the daemon and blocks until shutdown.
func (d *Daemon) Run() error {
	if err := d.Start(); err != nil {
		return err
	}
	<-d.ctx.Done()
	return d.shutdown()
}

// Stop signals the daemon to stop.
func (d *Daemon) Stop() {
	d.cancel()
}

func (d *Daemon) shutdown() error {
	log.Println("Shutting down daemon...")

	if d.listener != nil {
		_ = d.listener.Close()
	}

	d.clientsMu.Lock()
	for _, cs := range d.clients {
		select {
		case <-cs.done:
		default:
			close(cs.done)
		}
		_ = cs.conn.Close()
	}
	d.clients = make(map[string]*connState)
	d.clientsMu.Unlock()

	d.manager.Shutdown()

	socketPath := d.manager.SocketPath()
	_ = os.Remove(socketPath)

	pidPath, err := GetPidFilePath()
	if err == nil {
		_ = os.Remove(pidPath)
	}

	log.Println("Daemon shutdown complete")
	return nil
}

// handleSignals is defined in platform-specific files:
// - daemon_unix.go for Unix/Linux/macOS
// - daemon_windows.go for Windows

func (d *Daemon) acceptLoop() {
	for {
		conn, err := d.listener.Accept()
		if err != nil {
			select {
			case <-d.ctx.Done():
				return
			default:
				log.Printf("Accept error: %v", err)
				continue
			}
		}
		go d.handleConnection(conn)
	}
}

func (d *Daemon) handleConnection(conn net.Conn) {
	clientID := fmt.Sprintf("client-%d", time.Now().UnixNano())

	cs := &connState{
		conn:             conn,
		clientID:         clientID,
		done:             make(chan struct{}),
		lastActive:       time.Now(),
		codec:            DefaultCodec(), // Default to gob, may be changed in handleHello
		ptySubscriptions: make(map[string]struct{}),
	}

	LogBasic("Client %s connected", clientID)

	d.clientsMu.Lock()
	d.clients[clientID] = cs
	d.clientsMu.Unlock()

	defer func() {
		LogBasic("Client %s disconnected", clientID)

		d.clientsMu.Lock()
		delete(d.clients, clientID)
		d.clientsMu.Unlock()

		// Unsubscribe from all PTYs
		if cs.sessionID != "" {
			if session := d.manager.GetSessionByID(cs.sessionID); session != nil {
				for ptyID := range cs.ptySubscriptions {
					if pty := session.GetPTY(ptyID); pty != nil {
						pty.Unsubscribe(clientID)
					}
				}
			}
		}

		_ = conn.Close()
	}()

	lastHeartbeat := time.Now()
	for {
		select {
		case <-d.ctx.Done():
			return
		case <-cs.done:
			return
		default:
		}

		_ = conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))

		msg, codecType, err := ReadMessageWithCodec(conn)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				// Keep-alive check
				if time.Since(lastHeartbeat) > 2*time.Second {
					lastHeartbeat = time.Now()
				}
				continue
			}
			LogError("Read error from %s: %v", clientID, err)
			return
		}

		// Update codec if message came with a different one (shouldn't happen after handshake)
		_ = codecType // Codec is negotiated at Hello, messages should use that codec

		cs.lastActive = time.Now()

		if err := d.handleMessage(cs, msg); err != nil {
			LogError("Error handling message from %s: %v", clientID, err)
			_ = d.sendError(cs, ErrCodeInternal, err.Error())
		}
	}
}

func (d *Daemon) handleMessage(cs *connState, msg *Message) error {
	switch msg.Type {
	case MsgHello:
		return d.handleHello(cs, msg)
	case MsgAttach:
		return d.handleAttach(cs, msg)
	case MsgDetach:
		return d.handleDetach(cs)
	case MsgNew:
		return d.handleNew(cs, msg)
	case MsgList:
		return d.handleList(cs)
	case MsgKill:
		return d.handleKill(cs, msg)
	case MsgInput:
		return d.handleInput(cs, msg)
	case MsgResize:
		return d.handleResize(cs, msg)
	case MsgPing:
		return d.sendPong(cs)
	case MsgCreatePTY:
		return d.handleCreatePTY(cs, msg)
	case MsgClosePTY:
		return d.handleClosePTY(cs, msg)
	case MsgListPTYs:
		return d.handleListPTYs(cs)
	case MsgGetState:
		return d.handleGetState(cs)
	case MsgUpdateState:
		return d.handleUpdateState(cs, msg)
	case MsgSubscribePTY:
		return d.handleSubscribePTY(cs, msg)
	case MsgGetTerminalState:
		return d.handleGetTerminalState(cs, msg)
	default:
		return fmt.Errorf("unknown message type: %d", msg.Type)
	}
}

func (d *Daemon) handleHello(cs *connState, msg *Message) error {
	var payload HelloPayload
	if err := msg.ParsePayloadWithCodec(&payload, cs.codec); err != nil {
		return fmt.Errorf("invalid hello payload: %w", err)
	}

	cs.hello = &payload

	// Negotiate codec based on client preference
	cs.codec = NegotiateCodec(payload.PreferredCodec)
	LogBasic("Client %s negotiated codec: %s", cs.clientID, cs.codec.Type())

	sessions := d.manager.ListSessions()
	names := make([]string, len(sessions))
	for i, s := range sessions {
		names[i] = s.Name
	}

	return d.sendMessage(cs, MsgWelcome, &WelcomePayload{
		Version:      d.version,
		SessionNames: names,
		Codec:        cs.codec.Type().String(),
	})
}

func (d *Daemon) handleAttach(cs *connState, msg *Message) error {
	var payload AttachPayload
	if err := msg.ParsePayloadWithCodec(&payload, cs.codec); err != nil {
		return fmt.Errorf("invalid attach payload: %w", err)
	}

	cfg := &SessionConfig{}
	if cs.hello != nil {
		cfg.Term = cs.hello.Term
		cfg.ColorTerm = cs.hello.ColorTerm
		cfg.Shell = cs.hello.Shell
	}

	var session *Session
	var err error

	if payload.SessionName == "" {
		session, err = d.manager.GetDefaultSession(cfg, payload.Width, payload.Height)
	} else if payload.CreateNew {
		session, _, err = d.manager.GetOrCreateSession(payload.SessionName, cfg, payload.Width, payload.Height)
	} else {
		session = d.manager.GetSession(payload.SessionName)
		if session == nil {
			return d.sendError(cs, ErrCodeSessionNotFound, fmt.Sprintf("session '%s' not found", payload.SessionName))
		}
	}

	if err != nil {
		return fmt.Errorf("failed to get/create session: %w", err)
	}

	cs.sessionID = session.ID
	log.Printf("Client %s attached to session %s", cs.clientID, session.Name)

	// Get session state to return
	state := session.GetState()
	debugLog("[DEBUG] Session state: %d windows, %d PTYs", len(state.Windows), session.PTYCount())
	for i, w := range state.Windows {
		debugLog("[DEBUG]   Window %d: ID=%s, PTYID=%s", i, w.ID[:8], w.PTYID[:8])
	}

	return d.sendMessage(cs, MsgAttached, &AttachedPayload{
		SessionName: session.Name,
		SessionID:   session.ID,
		Width:       payload.Width,
		Height:      payload.Height,
		WindowCount: len(state.Windows),
		State:       state,
	})
}

func (d *Daemon) handleDetach(cs *connState) error {
	if cs.sessionID == "" {
		return d.sendError(cs, ErrCodeNotAttached, "not attached to any session")
	}

	// Unsubscribe from all PTYs
	if session := d.manager.GetSessionByID(cs.sessionID); session != nil {
		for ptyID := range cs.ptySubscriptions {
			if pty := session.GetPTY(ptyID); pty != nil {
				pty.Unsubscribe(cs.clientID)
			}
		}
	}
	cs.ptySubscriptions = make(map[string]struct{})
	cs.sessionID = ""

	return d.sendMessage(cs, MsgDetached, nil)
}

func (d *Daemon) handleNew(cs *connState, msg *Message) error {
	var payload NewPayload
	if err := msg.ParsePayloadWithCodec(&payload, cs.codec); err != nil {
		return fmt.Errorf("invalid new payload: %w", err)
	}

	cfg := &SessionConfig{}
	if cs.hello != nil {
		cfg.Term = cs.hello.Term
		cfg.ColorTerm = cs.hello.ColorTerm
		cfg.Shell = cs.hello.Shell
	}

	name := payload.SessionName
	if name == "" {
		name = d.manager.GenerateSessionName()
	}

	_, err := d.manager.CreateSession(name, cfg, payload.Width, payload.Height)
	if err != nil {
		if err.Error() == fmt.Sprintf("session '%s' already exists", name) {
			return d.sendError(cs, ErrCodeSessionExists, err.Error())
		}
		return fmt.Errorf("failed to create session: %w", err)
	}

	return d.handleList(cs)
}

func (d *Daemon) handleList(cs *connState) error {
	sessions := d.manager.ListSessions()
	return d.sendMessage(cs, MsgSessionList, &SessionListPayload{
		Sessions: sessions,
	})
}

func (d *Daemon) handleKill(cs *connState, msg *Message) error {
	var payload KillPayload
	if err := msg.ParsePayloadWithCodec(&payload, cs.codec); err != nil {
		return fmt.Errorf("invalid kill payload: %w", err)
	}

	if err := d.manager.DeleteSession(payload.SessionName); err != nil {
		return d.sendError(cs, ErrCodeSessionNotFound, err.Error())
	}

	return d.handleList(cs)
}

func (d *Daemon) handleInput(cs *connState, msg *Message) error {
	if cs.sessionID == "" {
		return nil
	}

	session := d.manager.GetSessionByID(cs.sessionID)
	if session == nil {
		return nil
	}

	// Try binary format first (36-byte PTY ID + data)
	ptyID, data, err := ParseBinaryPTYMessage(msg.Payload)
	if err != nil {
		// Fall back to codec format
		var payload InputPayload
		if err := msg.ParsePayloadWithCodec(&payload, cs.codec); err != nil {
			debugLog("[DEBUG] handleInput: failed to parse payload: %v", err)
			return nil
		}
		ptyID = payload.PTYID
		data = payload.Data
	}

	if ptyID != "" {
		if pty := session.GetPTY(ptyID); pty != nil {
			debugLog("[DEBUG] Writing %d bytes to PTY %s", len(data), ptyID[:8])
			_, _ = pty.Write(data)
		} else {
			debugLog("[DEBUG] PTY %s not found for input", ptyID[:8])
		}
	}

	return nil
}

func (d *Daemon) handleResize(cs *connState, msg *Message) error {
	var payload ResizePTYPayload
	if err := msg.ParsePayloadWithCodec(&payload, cs.codec); err != nil {
		return fmt.Errorf("invalid resize payload: %w", err)
	}

	if cs.sessionID == "" {
		return nil
	}

	session := d.manager.GetSessionByID(cs.sessionID)
	if session == nil {
		return nil
	}

	if payload.PTYID != "" {
		if pty := session.GetPTY(payload.PTYID); pty != nil {
			_ = pty.Resize(payload.Width, payload.Height)
		}
	}

	return nil
}

func (d *Daemon) handleCreatePTY(cs *connState, msg *Message) error {
	debugLog("[DEBUG] handleCreatePTY called for client %s", cs.clientID)

	if cs.sessionID == "" {
		debugLog("[DEBUG] handleCreatePTY: client not attached")
		return d.sendError(cs, ErrCodeNotAttached, "not attached to any session")
	}

	session := d.manager.GetSessionByID(cs.sessionID)
	if session == nil {
		debugLog("[DEBUG] handleCreatePTY: session not found")
		return d.sendError(cs, ErrCodeSessionNotFound, "session not found")
	}

	var payload CreatePTYPayload
	if err := msg.ParsePayloadWithCodec(&payload, cs.codec); err != nil {
		debugLog("[DEBUG] handleCreatePTY: invalid payload: %v", err)
		return fmt.Errorf("invalid create PTY payload: %w", err)
	}

	width := payload.Width
	height := payload.Height
	if width == 0 {
		width = 80
	}
	if height == 0 {
		height = 24
	}

	debugLog("[DEBUG] Creating PTY %dx%d for session %s", width, height, session.Name)
	pty, err := session.CreatePTY(width, height)
	if err != nil {
		debugLog("[DEBUG] handleCreatePTY: failed to create PTY: %v", err)
		return d.sendError(cs, ErrCodeInternal, fmt.Sprintf("failed to create PTY: %v", err))
	}

	// Set up exit callback to notify subscribed clients when PTY process exits
	sessionID := cs.sessionID
	pty.SetOnExit(func(ptyID string) {
		d.notifyPTYClosed(sessionID, ptyID)
	})

	debugLog("[DEBUG] PTY created: %s", pty.ID)
	return d.sendMessage(cs, MsgPTYCreated, &PTYCreatedPayload{
		ID:    pty.ID,
		Title: payload.Title,
	})
}

func (d *Daemon) handleClosePTY(cs *connState, msg *Message) error {
	if cs.sessionID == "" {
		return d.sendError(cs, ErrCodeNotAttached, "not attached to any session")
	}

	session := d.manager.GetSessionByID(cs.sessionID)
	if session == nil {
		return d.sendError(cs, ErrCodeSessionNotFound, "session not found")
	}

	var payload ClosePTYPayload
	if err := msg.ParsePayloadWithCodec(&payload, cs.codec); err != nil {
		return fmt.Errorf("invalid close PTY payload: %w", err)
	}

	// Unsubscribe first
	delete(cs.ptySubscriptions, payload.PTYID)

	if err := session.ClosePTY(payload.PTYID); err != nil {
		return d.sendError(cs, ErrCodePTYNotFound, err.Error())
	}

	return d.sendMessage(cs, MsgPTYClosed, &ClosePTYPayload{PTYID: payload.PTYID})
}

func (d *Daemon) handleListPTYs(cs *connState) error {
	if cs.sessionID == "" {
		return d.sendError(cs, ErrCodeNotAttached, "not attached to any session")
	}

	session := d.manager.GetSessionByID(cs.sessionID)
	if session == nil {
		return d.sendError(cs, ErrCodeSessionNotFound, "session not found")
	}

	ptyIDs := session.ListPTYIDs()
	ptys := make([]PTYInfo, 0, len(ptyIDs))

	for _, id := range ptyIDs {
		pty := session.GetPTY(id)
		if pty != nil {
			ptys = append(ptys, PTYInfo{
				ID:     pty.ID,
				Exited: pty.IsExited(),
			})
		}
	}

	return d.sendMessage(cs, MsgPTYList, &PTYListPayload{PTYs: ptys})
}

func (d *Daemon) handleGetState(cs *connState) error {
	if cs.sessionID == "" {
		return d.sendError(cs, ErrCodeNotAttached, "not attached to any session")
	}

	session := d.manager.GetSessionByID(cs.sessionID)
	if session == nil {
		return d.sendError(cs, ErrCodeSessionNotFound, "session not found")
	}

	state := session.GetState()
	return d.sendMessage(cs, MsgStateData, state)
}

func (d *Daemon) handleUpdateState(cs *connState, msg *Message) error {
	if cs.sessionID == "" {
		return d.sendError(cs, ErrCodeNotAttached, "not attached to any session")
	}

	session := d.manager.GetSessionByID(cs.sessionID)
	if session == nil {
		return d.sendError(cs, ErrCodeSessionNotFound, "session not found")
	}

	var state SessionState
	if err := msg.ParsePayloadWithCodec(&state, cs.codec); err != nil {
		return fmt.Errorf("invalid state payload: %w", err)
	}

	session.UpdateState(&state)
	return nil
}

func (d *Daemon) handleSubscribePTY(cs *connState, msg *Message) error {
	debugLog("[DEBUG] handleSubscribePTY called for client %s", cs.clientID)

	if cs.sessionID == "" {
		return d.sendError(cs, ErrCodeNotAttached, "not attached to any session")
	}

	session := d.manager.GetSessionByID(cs.sessionID)
	if session == nil {
		return d.sendError(cs, ErrCodeSessionNotFound, "session not found")
	}

	var payload SubscribePTYPayload
	if err := msg.ParsePayloadWithCodec(&payload, cs.codec); err != nil {
		return fmt.Errorf("invalid subscribe PTY payload: %w", err)
	}

	debugLog("[DEBUG] Subscribing to PTY %s", payload.PTYID)
	pty := session.GetPTY(payload.PTYID)
	if pty == nil {
		debugLog("[DEBUG] PTY %s not found", payload.PTYID)
		return d.sendError(cs, ErrCodePTYNotFound, fmt.Sprintf("PTY %s not found", payload.PTYID))
	}

	// Subscribe and start streaming
	cs.ptySubscriptions[payload.PTYID] = struct{}{}
	debugLog("[DEBUG] Starting PTY output stream for %s", payload.PTYID)
	go d.streamPTYOutput(cs, pty)

	return nil
}

func (d *Daemon) handleGetTerminalState(cs *connState, msg *Message) error {
	if cs.sessionID == "" {
		return d.sendError(cs, ErrCodeNotAttached, "not attached to any session")
	}

	session := d.manager.GetSessionByID(cs.sessionID)
	if session == nil {
		return d.sendError(cs, ErrCodeSessionNotFound, "session not found")
	}

	var payload GetTerminalStatePayload
	if err := msg.ParsePayloadWithCodec(&payload, cs.codec); err != nil {
		return fmt.Errorf("invalid get terminal state payload: %w", err)
	}

	pty := session.GetPTY(payload.PTYID)
	if pty == nil {
		return d.sendError(cs, ErrCodePTYNotFound, fmt.Sprintf("PTY %s not found", payload.PTYID))
	}

	state := pty.GetTerminalState()
	return d.sendMessage(cs, MsgTerminalState, &TerminalStatePayload{
		PTYID: payload.PTYID,
		State: state,
	})
}

func (d *Daemon) streamPTYOutput(cs *connState, pty *PTY) {
	debugLog("[DEBUG] streamPTYOutput started for PTY %s, client %s", pty.ID[:8], cs.clientID)
	outputCh := pty.Subscribe(cs.clientID)
	debugLog("[DEBUG] Subscribed to PTY output channel")

	for {
		select {
		case <-cs.done:
			debugLog("[DEBUG] streamPTYOutput: client done, unsubscribing")
			pty.Unsubscribe(cs.clientID)
			return
		case <-d.ctx.Done():
			debugLog("[DEBUG] streamPTYOutput: daemon context done")
			return
		case data, ok := <-outputCh:
			if !ok {
				debugLog("[DEBUG] streamPTYOutput: output channel closed")
				return
			}

			debugLog("[DEBUG] streamPTYOutput: got %d bytes from PTY %s", len(data), pty.ID[:8])

			// Use optimized binary format for PTY output (bypasses codec for performance)
			cs.sendMu.Lock()
			_ = cs.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			err := WritePTYOutput(cs.conn, pty.ID, data)
			cs.sendMu.Unlock()

			if err != nil {
				debugLog("[DEBUG] streamPTYOutput: failed to send: %v", err)
				pty.Unsubscribe(cs.clientID)
				return
			}
			debugLog("[DEBUG] streamPTYOutput: sent %d bytes to client", len(data))
		}
	}
}

// notifyPTYClosed sends MsgPTYClosed to all clients subscribed to the given PTY.
// This is called when the PTY process exits (e.g., user types exit or Ctrl+D).
func (d *Daemon) notifyPTYClosed(sessionID, ptyID string) {
	debugLog("[DEBUG] notifyPTYClosed: sessionID=%s, ptyID=%s", sessionID[:8], ptyID[:8])

	d.clientsMu.RLock()
	defer d.clientsMu.RUnlock()

	for _, cs := range d.clients {
		// Only notify clients attached to this session and subscribed to this PTY
		if cs.sessionID != sessionID {
			continue
		}
		if _, subscribed := cs.ptySubscriptions[ptyID]; !subscribed {
			continue
		}

		debugLog("[DEBUG] notifyPTYClosed: sending to client %s", cs.clientID)
		// Send in a goroutine to avoid blocking if client is slow
		go func(client *connState) {
			if err := d.sendMessage(client, MsgPTYClosed, &ClosePTYPayload{PTYID: ptyID}); err != nil {
				debugLog("[DEBUG] notifyPTYClosed: failed to send to client: %v", err)
			}
		}(cs)
	}
}

func (d *Daemon) sendMessage(cs *connState, msgType MessageType, payload any) error {
	msg, err := NewMessageWithCodec(msgType, payload, cs.codec)
	if err != nil {
		return err
	}

	cs.sendMu.Lock()
	defer cs.sendMu.Unlock()

	_ = cs.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return WriteMessageWithCodec(cs.conn, msg, cs.codec)
}

func (d *Daemon) sendError(cs *connState, code int, message string) error {
	return d.sendMessage(cs, MsgError, &ErrorPayload{
		Code:    code,
		Message: message,
	})
}

func (d *Daemon) sendPong(cs *connState) error {
	return d.sendMessage(cs, MsgPong, nil)
}

func (d *Daemon) cleanupLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-d.ctx.Done():
			return
		case <-ticker.C:
			// Could implement session cleanup here
		}
	}
}

func (d *Daemon) isDaemonRunning(socketPath string) bool {
	conn, err := net.DialTimeout("unix", socketPath, time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func (d *Daemon) writePidFile() error {
	pidPath, err := GetPidFilePath()
	if err != nil {
		return err
	}
	return os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0600)
}

// IsDaemonRunning checks if a daemon is already running.
func IsDaemonRunning() bool {
	socketPath, err := GetSocketPath()
	if err != nil {
		return false
	}

	conn, err := net.DialTimeout("unix", socketPath, time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// GetDaemonPID is defined in platform-specific files:
// - daemon_unix.go for Unix/Linux/macOS
// - daemon_windows.go for Windows
