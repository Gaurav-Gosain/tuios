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

	// Pending requests: maps requestID to the client that made the request
	// Used to route command results back to the original requester
	pendingRequests   map[string]*connState
	pendingRequestsMu sync.RWMutex

	// Goroutine tracking for clean shutdown
	wg sync.WaitGroup

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

	// isTUIClient indicates this is a full TUI client (vs a control client)
	// TUI clients can receive and execute remote commands
	isTUIClient bool

	// Client terminal dimensions (for multi-client size calculation)
	width  int
	height int

	// Client's terminal graphics capabilities (pixel dimensions, etc.)
	// Used to set proper PTY pixel sizes for tools like kitty icat
	pixelWidth    int
	pixelHeight   int
	cellWidth     int
	cellHeight    int
	kittyGraphics bool
	sixelGraphics bool
	terminalName  string
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
		manager:         NewManager(),
		ctx:             ctx,
		cancel:          cancel,
		clients:         make(map[string]*connState),
		pendingRequests: make(map[string]*connState),
		version:         cfg.Version,
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
		if isDaemonRunningAt(socketPath) {
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

// Stop signals the daemon to stop and performs cleanup.
func (d *Daemon) Stop() {
	d.cancel()
	_ = d.shutdown()
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

	// Wait for goroutines with timeout
	done := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		log.Println("All goroutines exited cleanly")
	case <-time.After(5 * time.Second):
		log.Println("Warning: goroutine shutdown timed out after 5s, forcing shutdown")
	}

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
	case MsgUnsubscribePTY:
		return d.handleUnsubscribePTY(cs, msg)
	case MsgGetTerminalState:
		return d.handleGetTerminalState(cs, msg)
	case MsgExecuteCommand:
		return d.handleExecuteCommand(cs, msg)
	case MsgSendKeys:
		return d.handleSendKeys(cs, msg)
	case MsgSetConfig:
		return d.handleSetConfig(cs, msg)
	case MsgCommandResult:
		return d.handleCommandResult(cs, msg)
	case MsgGetLogs:
		return d.handleGetLogs(cs, msg)
	case MsgQueryWindows:
		return d.handleQueryWindows(cs, msg)
	case MsgQuerySession:
		return d.handleQuerySession(cs, msg)
	case MsgWindowList:
		return d.handleWindowListResponse(cs, msg)
	case MsgSessionInfo:
		return d.handleSessionInfoResponse(cs, msg)
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

	// Store client's graphics capabilities for PTY pixel size reporting
	cs.pixelWidth = payload.PixelWidth
	cs.pixelHeight = payload.PixelHeight
	cs.cellWidth = payload.CellWidth
	cs.cellHeight = payload.CellHeight
	cs.kittyGraphics = payload.KittyGraphics
	cs.sixelGraphics = payload.SixelGraphics
	cs.terminalName = payload.TerminalName

	if payload.CellWidth > 0 && payload.CellHeight > 0 {
		LogBasic("Client %s capabilities: cell=%dx%d pixels, kitty=%v, sixel=%v, term=%s",
			cs.clientID, payload.CellWidth, payload.CellHeight, payload.KittyGraphics, payload.SixelGraphics, payload.TerminalName)
	}

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
	// Don't store client dimensions yet - the attach payload has placeholder values (80x24).
	// The real terminal size will be sent via NotifyTerminalSize after Bubble Tea starts.
	// Setting width/height to 0 excludes this client from calculateEffectiveSize until then.
	cs.width = 0
	cs.height = 0
	// Mark as TUI client if they have PTY subscriptions or if they're creating a new session
	// TUI clients are the ones that can receive and execute remote commands
	cs.isTUIClient = true

	clientCount := d.getSessionClientCount(session.ID)
	log.Printf("Client %s attached to session %s (TUI client, %d clients total)", cs.clientID, session.Name, clientCount)

	// Calculate effective size from existing clients (new client excluded since width/height = 0)
	effectiveWidth, effectiveHeight := d.calculateEffectiveSize(session.ID)
	if effectiveWidth == 0 || effectiveHeight == 0 {
		// No existing clients with known size, use placeholder for now
		// Will be updated when this client sends NotifyTerminalSize
		effectiveWidth = payload.Width
		effectiveHeight = payload.Height
	}

	// Update session size if needed
	session.Resize(effectiveWidth, effectiveHeight)

	// Notify other clients that a new client joined (this also broadcasts size change)
	if clientCount > 1 {
		d.notifyClientJoined(session.ID, cs)
	}

	// Get session state to return
	state := session.GetState()
	// Only update state dimensions if we have real client sizes
	// When reattaching after all clients disconnect, preserve the original state dimensions
	// so that window scaling works correctly. The placeholder 80x24 values would cause
	// windows to be scaled incorrectly when the real terminal size is known.
	if effectiveWidth != payload.Width || effectiveHeight != payload.Height {
		// We have real dimensions from other clients, use them
		state.Width = effectiveWidth
		state.Height = effectiveHeight
	}
	// If state dimensions are 0 (new session), use effective/placeholder values
	if state.Width == 0 || state.Height == 0 {
		state.Width = effectiveWidth
		state.Height = effectiveHeight
	}

	debugLog("[DEBUG] Session state: %d windows, %d PTYs", len(state.Windows), session.PTYCount())
	for i, w := range state.Windows {
		if len(w.ID) >= 8 && len(w.PTYID) >= 8 {
			debugLog("[DEBUG]   Window %d: ID=%s, PTYID=%s", i, w.ID[:8], w.PTYID[:8])
		}
	}

	// Sync PTY pixel dimensions from client's terminal capabilities
	// This enables graphics tools like kitty icat to query proper pixel sizes
	if cs.cellWidth > 0 && cs.cellHeight > 0 {
		d.syncPTYPixelDimensions(session, cs.cellWidth, cs.cellHeight)
	}

	return d.sendMessage(cs, MsgAttached, &AttachedPayload{
		SessionName: session.Name,
		SessionID:   session.ID,
		Width:       effectiveWidth,
		Height:      effectiveHeight,
		WindowCount: len(state.Windows),
		State:       state,
	})
}

func (d *Daemon) handleDetach(cs *connState) error {
	if cs.sessionID == "" {
		return d.sendError(cs, ErrCodeNotAttached, "not attached to any session")
	}

	sessionID := cs.sessionID
	clientID := cs.clientID

	// Unsubscribe from all PTYs
	if session := d.manager.GetSessionByID(sessionID); session != nil {
		for ptyID := range cs.ptySubscriptions {
			if pty := session.GetPTY(ptyID); pty != nil {
				pty.Unsubscribe(clientID)
			}
		}
	}
	cs.ptySubscriptions = make(map[string]struct{})
	cs.sessionID = ""
	cs.width = 0
	cs.height = 0

	// Notify other clients that this client left
	d.notifyClientLeft(sessionID, clientID)

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

	// Update client dimensions for multi-client size calculation
	if payload.PTYID == "" {
		// This is a client resize, not a PTY-specific resize
		cs.width = payload.Width
		cs.height = payload.Height
		// Recalculate effective session size
		d.recalculateAndBroadcastSize(cs.sessionID)
	} else {
		// PTY-specific resize
		if pty := session.GetPTY(payload.PTYID); pty != nil {
			_ = pty.Resize(payload.Width, payload.Height)
			_ = pty.UpdatePixelDimensions(cs.cellWidth, cs.cellHeight)
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

	// Set pixel dimensions from client's terminal capabilities
	if err := pty.UpdatePixelDimensions(cs.cellWidth, cs.cellHeight); err != nil {
		debugLog("[DEBUG] handleCreatePTY: failed to set pixel size: %v", err)
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

	// Broadcast state change to other clients in the session
	clientCount := d.getSessionClientCount(cs.sessionID)
	if clientCount > 1 {
		d.broadcastStateSync(cs.sessionID, &state, "update", cs.clientID)
	}

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

func (d *Daemon) handleUnsubscribePTY(cs *connState, msg *Message) error {
	debugLog("[DEBUG] handleUnsubscribePTY called for client %s", cs.clientID)

	if cs.sessionID == "" {
		return d.sendError(cs, ErrCodeNotAttached, "not attached to any session")
	}

	session := d.manager.GetSessionByID(cs.sessionID)
	if session == nil {
		return d.sendError(cs, ErrCodeSessionNotFound, "session not found")
	}

	var payload UnsubscribePTYPayload
	if err := msg.ParsePayloadWithCodec(&payload, cs.codec); err != nil {
		return fmt.Errorf("invalid unsubscribe PTY payload: %w", err)
	}

	debugLog("[DEBUG] Unsubscribing from PTY %s", payload.PTYID)

	// Remove from subscriptions
	delete(cs.ptySubscriptions, payload.PTYID)

	// Unsubscribe from the PTY output channel
	pty := session.GetPTY(payload.PTYID)
	if pty != nil {
		pty.Unsubscribe(cs.clientID)
		debugLog("[DEBUG] Successfully unsubscribed client %s from PTY %s", cs.clientID, payload.PTYID[:8])
	}

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
		d.wg.Add(1)
		go func(client *connState) {
			defer d.wg.Done()
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

// broadcastToSession sends a message to all TUI clients attached to a session.
// If excludeClientID is non-empty, that client is excluded from the broadcast.
func (d *Daemon) broadcastToSession(sessionID string, msgType MessageType, payload any, excludeClientID string) {
	d.clientsMu.RLock()
	defer d.clientsMu.RUnlock()

	for _, cs := range d.clients {
		if cs.sessionID != sessionID || !cs.isTUIClient {
			continue
		}
		if cs.clientID == excludeClientID {
			continue
		}
		// Send in a goroutine to avoid blocking if client is slow
		d.wg.Add(1)
		go func(client *connState) {
			defer d.wg.Done()
			if err := d.sendMessage(client, msgType, payload); err != nil {
				debugLog("[DEBUG] broadcastToSession: failed to send to client %s: %v", client.clientID, err)
			}
		}(cs)
	}
}

// getSessionClientCount returns the number of TUI clients attached to a session.
func (d *Daemon) getSessionClientCount(sessionID string) int {
	d.clientsMu.RLock()
	defer d.clientsMu.RUnlock()

	count := 0
	for _, cs := range d.clients {
		if cs.sessionID == sessionID && cs.isTUIClient {
			count++
		}
	}
	return count
}

// calculateEffectiveSize returns the minimum dimensions across all clients in a session.
// This is used for multi-client rendering where all clients need to see the same content.
func (d *Daemon) calculateEffectiveSize(sessionID string) (width, height int) {
	d.clientsMu.RLock()
	defer d.clientsMu.RUnlock()

	width, height = 0, 0
	first := true

	for _, cs := range d.clients {
		if cs.sessionID != sessionID || !cs.isTUIClient {
			continue
		}
		if cs.width == 0 || cs.height == 0 {
			continue
		}
		if first {
			width, height = cs.width, cs.height
			first = false
		} else {
			if cs.width < width {
				width = cs.width
			}
			if cs.height < height {
				height = cs.height
			}
		}
	}
	return width, height
}

// notifyClientJoined broadcasts a client join event to all other clients in the session.
func (d *Daemon) notifyClientJoined(sessionID string, joiningClient *connState) {
	clientCount := d.getSessionClientCount(sessionID)

	payload := &ClientJoinedPayload{
		ClientID:    joiningClient.clientID,
		ClientCount: clientCount,
		Width:       joiningClient.width,
		Height:      joiningClient.height,
	}

	d.broadcastToSession(sessionID, MsgClientJoined, payload, joiningClient.clientID)

	// Recalculate effective size and broadcast if changed
	d.recalculateAndBroadcastSize(sessionID)
}

// notifyClientLeft broadcasts a client leave event to all other clients in the session.
func (d *Daemon) notifyClientLeft(sessionID string, leavingClientID string) {
	clientCount := d.getSessionClientCount(sessionID)

	payload := &ClientLeftPayload{
		ClientID:    leavingClientID,
		ClientCount: clientCount,
	}

	d.broadcastToSession(sessionID, MsgClientLeft, payload, leavingClientID)

	// Recalculate effective size and broadcast if changed
	if clientCount > 0 {
		d.recalculateAndBroadcastSize(sessionID)
	}
}

// recalculateAndBroadcastSize recalculates the effective session size and broadcasts if changed.
func (d *Daemon) recalculateAndBroadcastSize(sessionID string) {
	session := d.manager.GetSessionByID(sessionID)
	if session == nil {
		return
	}

	newWidth, newHeight := d.calculateEffectiveSize(sessionID)
	if newWidth == 0 || newHeight == 0 {
		return
	}

	oldWidth, oldHeight := session.Size()
	if newWidth != oldWidth || newHeight != oldHeight {
		session.Resize(newWidth, newHeight)

		payload := &SessionResizePayload{
			Width:       newWidth,
			Height:      newHeight,
			ClientCount: d.getSessionClientCount(sessionID),
		}
		d.broadcastToSession(sessionID, MsgSessionResize, payload, "")
		LogBasic("Session %s resized to %dx%d (min of %d clients)", session.Name, newWidth, newHeight, payload.ClientCount)
	}
}

// broadcastStateSync broadcasts a state update to all clients in a session.
func (d *Daemon) broadcastStateSync(sessionID string, state *SessionState, triggerType string, sourceClientID string) {
	payload := &StateSyncPayload{
		State:       state,
		TriggerType: triggerType,
		SourceID:    sourceClientID,
	}
	d.broadcastToSession(sessionID, MsgStateSync, payload, sourceClientID)
}

// syncPTYPixelDimensions sets pixel dimensions on all PTYs in a session.
// This is called when a client attaches with terminal graphics capabilities.
func (d *Daemon) syncPTYPixelDimensions(session *Session, cellWidth, cellHeight int) {
	if session == nil || cellWidth <= 0 || cellHeight <= 0 {
		return
	}

	for _, ptyID := range session.ListPTYIDs() {
		if pty := session.GetPTY(ptyID); pty != nil {
			if err := pty.UpdatePixelDimensions(cellWidth, cellHeight); err != nil {
				LogBasic("Failed to set PTY %s pixel size: %v", ptyID[:8], err)
			}
		}
	}
}

// handleExecuteCommand routes a tape command to the TUI client attached to the session.
func (d *Daemon) handleExecuteCommand(cs *connState, msg *Message) error {
	var payload ExecuteCommandPayload
	if err := msg.ParsePayloadWithCodec(&payload, cs.codec); err != nil {
		return fmt.Errorf("invalid execute command payload: %w", err)
	}

	LogBasic("Received execute command: %s (session=%s, args=%v)", payload.CommandType, payload.SessionName, payload.Args)

	// Find the target session
	session := d.findTargetSession(payload.SessionName)
	if session == nil {
		LogBasic("Execute command: session not found")
		return d.sendCommandResult(cs, payload.RequestID, false, "session not found")
	}
	LogBasic("Execute command: found session %s (ID=%s)", session.Name, session.ID)

	// Find the TUI client attached to this session
	tuiClient := d.findTUIClient(session.ID)
	if tuiClient == nil {
		LogBasic("Execute command: no TUI client found for session %s", session.ID)
		return d.sendCommandResult(cs, payload.RequestID, false, "no TUI client attached to session")
	}
	LogBasic("Execute command: found TUI client %s", tuiClient.clientID)

	// Forward the command to the TUI client
	var remoteCmd *RemoteCommandPayload
	if payload.TapeScript != "" {
		// Execute a full tape script
		remoteCmd = &RemoteCommandPayload{
			RequestID:   payload.RequestID,
			CommandType: "tape_script",
			TapeScript:  payload.TapeScript,
		}
	} else {
		// Execute a single tape command
		remoteCmd = &RemoteCommandPayload{
			RequestID:   payload.RequestID,
			CommandType: "tape_command",
			TapeCommand: payload.CommandType,
			TapeArgs:    payload.Args,
		}
	}

	if err := d.sendMessage(tuiClient, MsgRemoteCommand, remoteCmd); err != nil {
		return d.sendCommandResult(cs, payload.RequestID, false, fmt.Sprintf("failed to send to TUI: %v", err))
	}

	// Track this request so we can route the result back to the original client
	if cs.clientID != tuiClient.clientID {
		d.pendingRequestsMu.Lock()
		d.pendingRequests[payload.RequestID] = cs
		d.pendingRequestsMu.Unlock()
	}

	// Don't send response here - wait for TUI to send result via handleCommandResult
	return nil
}

// handleSendKeys routes keystrokes to the TUI client attached to the session.
func (d *Daemon) handleSendKeys(cs *connState, msg *Message) error {
	var payload SendKeysPayload
	if err := msg.ParsePayloadWithCodec(&payload, cs.codec); err != nil {
		return fmt.Errorf("invalid send keys payload: %w", err)
	}

	// Find the target session
	session := d.findTargetSession(payload.SessionName)
	if session == nil {
		return d.sendCommandResult(cs, payload.RequestID, false, "session not found")
	}

	// Find the TUI client attached to this session
	tuiClient := d.findTUIClient(session.ID)
	if tuiClient == nil {
		return d.sendCommandResult(cs, payload.RequestID, false, "no TUI client attached to session")
	}

	// Forward the command to the TUI client
	remoteCmd := &RemoteCommandPayload{
		RequestID:   payload.RequestID,
		CommandType: "send_keys",
		Keys:        payload.Keys,
		Literal:     payload.Literal,
		Raw:         payload.Raw,
	}

	if err := d.sendMessage(tuiClient, MsgRemoteCommand, remoteCmd); err != nil {
		return d.sendCommandResult(cs, payload.RequestID, false, fmt.Sprintf("failed to send to TUI: %v", err))
	}

	// Track this request so we can route the result back to the original client
	if cs.clientID != tuiClient.clientID {
		d.pendingRequestsMu.Lock()
		d.pendingRequests[payload.RequestID] = cs
		d.pendingRequestsMu.Unlock()
	}

	// Don't send response here - wait for TUI to send result via handleCommandResult
	return nil
}

// handleSetConfig routes a config change to the TUI client attached to the session.
func (d *Daemon) handleSetConfig(cs *connState, msg *Message) error {
	var payload SetConfigPayload
	if err := msg.ParsePayloadWithCodec(&payload, cs.codec); err != nil {
		return fmt.Errorf("invalid set config payload: %w", err)
	}

	// Find the target session
	session := d.findTargetSession(payload.SessionName)
	if session == nil {
		return d.sendCommandResult(cs, payload.RequestID, false, "session not found")
	}

	// Find the TUI client attached to this session
	tuiClient := d.findTUIClient(session.ID)
	if tuiClient == nil {
		return d.sendCommandResult(cs, payload.RequestID, false, "no TUI client attached to session")
	}

	// Forward the command to the TUI client
	remoteCmd := &RemoteCommandPayload{
		RequestID:   payload.RequestID,
		CommandType: "set_config",
		ConfigPath:  payload.Path,
		ConfigValue: payload.Value,
	}

	if err := d.sendMessage(tuiClient, MsgRemoteCommand, remoteCmd); err != nil {
		return d.sendCommandResult(cs, payload.RequestID, false, fmt.Sprintf("failed to send to TUI: %v", err))
	}

	// Track this request so we can route the result back to the original client
	if cs.clientID != tuiClient.clientID {
		d.pendingRequestsMu.Lock()
		d.pendingRequests[payload.RequestID] = cs
		d.pendingRequestsMu.Unlock()
	}

	// Don't send response here - wait for TUI to send result via handleCommandResult
	return nil
}

// handleCommandResult handles command results from TUI clients.
// Forwards results back to the original requester if there's a pending request.
func (d *Daemon) handleCommandResult(cs *connState, msg *Message) error {
	var payload CommandResultPayload
	if err := msg.ParsePayloadWithCodec(&payload, cs.codec); err != nil {
		return fmt.Errorf("invalid command result payload: %w", err)
	}

	if payload.Success {
		LogBasic("Command %s succeeded: %s (data keys: %d)", payload.RequestID, payload.Message, len(payload.Data))
		for k, v := range payload.Data {
			LogBasic("  Data[%s] = %v", k, v)
		}
	} else {
		LogBasic("Command %s failed: %s", payload.RequestID, payload.Message)
	}

	// Check if there's a pending request from another client waiting for this result
	d.pendingRequestsMu.Lock()
	requester, found := d.pendingRequests[payload.RequestID]
	if found {
		delete(d.pendingRequests, payload.RequestID)
	}
	d.pendingRequestsMu.Unlock()

	// Forward the result to the original requester
	if found && requester != nil {
		LogBasic("Forwarding result to original requester %s", requester.clientID)
		return d.sendMessage(requester, MsgCommandResult, &payload)
	}

	return nil
}

// findTargetSession finds a session by name, or returns the most recently active session.
func (d *Daemon) findTargetSession(sessionName string) *Session {
	if sessionName != "" {
		return d.manager.GetSession(sessionName)
	}

	// Find the most recently active session
	sessions := d.manager.ListSessions()
	if len(sessions) == 0 {
		return nil
	}

	var mostRecent *Session
	var mostRecentTime int64 = 0

	for _, info := range sessions {
		if info.LastActive > mostRecentTime {
			mostRecentTime = info.LastActive
			mostRecent = d.manager.GetSession(info.Name)
		}
	}

	return mostRecent
}

// findTUIClient finds the TUI client attached to a session.
func (d *Daemon) findTUIClient(sessionID string) *connState {
	d.clientsMu.RLock()
	defer d.clientsMu.RUnlock()

	for _, cs := range d.clients {
		if cs.sessionID == sessionID && cs.isTUIClient {
			return cs
		}
	}

	return nil
}

// sendCommandResult sends a command result to a client.
func (d *Daemon) sendCommandResult(cs *connState, requestID string, success bool, message string) error {
	return d.sendMessage(cs, MsgCommandResult, &CommandResultPayload{
		RequestID: requestID,
		Success:   success,
		Message:   message,
	})
}

// handleGetLogs retrieves recent log entries from the daemon's log buffer.
func (d *Daemon) handleGetLogs(cs *connState, msg *Message) error {
	var payload GetLogsPayload
	if err := msg.ParsePayloadWithCodec(&payload, cs.codec); err != nil {
		return fmt.Errorf("invalid get logs payload: %w", err)
	}

	entries := GetLogEntries(payload.Count)

	if payload.Clear {
		ClearLogBuffer()
	}

	return d.sendMessage(cs, MsgLogsData, &LogsDataPayload{
		Entries: entries,
	})
}

// handleQueryWindows returns window list from session state.
// Works even without a TUI client attached by using the daemon's stored state.
func (d *Daemon) handleQueryWindows(cs *connState, msg *Message) error {
	var payload QueryWindowsPayload
	if err := msg.ParsePayloadWithCodec(&payload, cs.codec); err != nil {
		return fmt.Errorf("invalid query windows payload: %w", err)
	}

	// Find the target session
	session := d.findTargetSession(payload.SessionName)
	if session == nil {
		return d.sendError(cs, ErrCodeSessionNotFound, "session not found")
	}

	// Get state from session (daemon stores this)
	state := session.GetState()

	// Build window list from state
	windows := make([]map[string]any, 0, len(state.Windows))
	for i, w := range state.Windows {
		displayName := w.Title
		if w.CustomName != "" {
			displayName = w.CustomName
		}

		winInfo := map[string]any{
			"window_id":    w.ID,
			"index":        i,
			"title":        w.Title,
			"display_name": displayName,
			"workspace":    w.Workspace,
			"minimized":    w.Minimized,
			"focused":      w.ID == state.FocusedWindowID,
			"x":            w.X,
			"y":            w.Y,
			"width":        w.Width,
			"height":       w.Height,
		}
		if w.CustomName != "" {
			winInfo["custom_name"] = w.CustomName
		}
		windows = append(windows, winInfo)
	}

	// Count windows per workspace
	workspaceWindows := make([]int, 9) // Assume 9 workspaces
	for _, w := range state.Windows {
		if w.Workspace >= 1 && w.Workspace <= 9 {
			workspaceWindows[w.Workspace-1]++
		}
	}

	// Find focused index
	focusedIndex := -1
	for i, w := range state.Windows {
		if w.ID == state.FocusedWindowID {
			focusedIndex = i
			break
		}
	}

	resultData := map[string]any{
		"windows":           windows,
		"total":             len(state.Windows),
		"focused_index":     focusedIndex,
		"focused_window_id": state.FocusedWindowID,
		"current_workspace": state.CurrentWorkspace,
		"workspace_windows": workspaceWindows,
	}

	return d.sendMessage(cs, MsgCommandResult, &CommandResultPayload{
		RequestID: payload.RequestID,
		Success:   true,
		Message:   "command executed",
		Data:      resultData,
	})
}

// handleQuerySession returns session info from daemon's stored state.
// Works even without a TUI client attached.
func (d *Daemon) handleQuerySession(cs *connState, msg *Message) error {
	var payload QuerySessionPayload
	if err := msg.ParsePayloadWithCodec(&payload, cs.codec); err != nil {
		return fmt.Errorf("invalid query session payload: %w", err)
	}

	// Find the target session
	session := d.findTargetSession(payload.SessionName)
	if session == nil {
		return d.sendError(cs, ErrCodeSessionNotFound, "session not found")
	}

	// Get state from session
	state := session.GetState()

	// Check if TUI is attached
	tuiClient := d.findTUIClient(session.ID)
	hasClient := tuiClient != nil

	// Build session info from state
	tilingMode := "floating"
	if state.AutoTiling {
		tilingMode = "tiling"
	}

	resultData := map[string]any{
		"session_name":      state.Name,
		"session_id":        session.ID,
		"mode":              "unknown", // Can't know mode without TUI
		"current_workspace": state.CurrentWorkspace,
		"num_workspaces":    9, // Default
		"window_count":      len(state.Windows),
		"tiling_mode":       tilingMode,
		"master_ratio":      state.MasterRatio,
		"width":             state.Width,
		"height":            state.Height,
		"tui_attached":      hasClient,
	}

	return d.sendMessage(cs, MsgCommandResult, &CommandResultPayload{
		RequestID: payload.RequestID,
		Success:   true,
		Message:   "command executed",
		Data:      resultData,
	})
}

// handleWindowListResponse handles window list from TUI and forwards to requesting client.
func (d *Daemon) handleWindowListResponse(cs *connState, msg *Message) error {
	var payload WindowListPayload
	if err := msg.ParsePayloadWithCodec(&payload, cs.codec); err != nil {
		return fmt.Errorf("invalid window list payload: %w", err)
	}

	// For now, the daemon just logs this. In a full implementation,
	// we would route this back to the original requesting client.
	LogBasic("Received window list: %d windows", payload.Total)

	return nil
}

// handleSessionInfoResponse handles session info from TUI.
func (d *Daemon) handleSessionInfoResponse(cs *connState, msg *Message) error {
	var payload SessionInfoPayload
	if err := msg.ParsePayloadWithCodec(&payload, cs.codec); err != nil {
		return fmt.Errorf("invalid session info payload: %w", err)
	}

	LogBasic("Received session info: %s, %d windows", payload.SessionName, payload.TotalWindows)

	return nil
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

// isDaemonRunningAt checks if a daemon is listening on the given socket path.
func isDaemonRunningAt(socketPath string) bool {
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
	return isDaemonRunningAt(socketPath)
}

// GetDaemonPID is defined in platform-specific files:
// - daemon_unix.go for Unix/Linux/macOS
// - daemon_windows.go for Windows
