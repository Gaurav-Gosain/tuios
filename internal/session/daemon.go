package session

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"runtime/debug"
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
	pendingRequests   map[string]*pendingRequest
	pendingRequestsMu sync.RWMutex

	// Goroutine tracking for clean shutdown
	wg sync.WaitGroup

	// shutdownOnce makes shutdown idempotent (Run and Stop can both call it).
	shutdownOnce sync.Once

	// Configuration
	version string

	// disableAutoRestore, when true, skips cold-start resurrection of saved
	// sessions on daemon start. Sessions can still be brought back on demand
	// with the resurrect verb.
	disableAutoRestore bool
}

// pendingRequest tracks a routed command awaiting its result, with the time it
// was created so cleanupLoop can expire stale entries.
type pendingRequest struct {
	requester *connState
	created   time.Time
}

// connState tracks state for a connected client.
type connState struct {
	conn     net.Conn
	clientID string
	hello    *HelloPayload
	done     chan struct{}
	doneOnce sync.Once // gates close(done) so shutdown is safe to call twice
	sendMu   sync.Mutex

	// Codec negotiated for this connection (gob by default)
	codec Codec

	// mu guards the mutable per-connection fields below (sessionID, width,
	// height, isTUIClient, ptySubscriptions). These are written on this
	// connection's own goroutine and read from other goroutines (PTY exit
	// callbacks, size recalculation, command routing). Lock ordering: readers
	// that also hold d.clientsMu always take d.clientsMu first, then cs.mu; no
	// path takes cs.mu then d.clientsMu.
	mu               sync.Mutex
	sessionID        string // Session they're attached to
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
	// DisableAutoRestore skips restoring saved sessions on daemon start.
	DisableAutoRestore bool
}

// NewDaemon creates a new daemon instance.
func NewDaemon(cfg *DaemonConfig) *Daemon {
	ctx, cancel := context.WithCancel(context.Background())

	d := &Daemon{
		manager:            NewManager(),
		ctx:                ctx,
		cancel:             cancel,
		clients:            make(map[string]*connState),
		pendingRequests:    make(map[string]*pendingRequest),
		version:            cfg.Version,
		disableAutoRestore: cfg.DisableAutoRestore,
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

	// Restore sessions saved before the previous shutdown/crash before we start
	// accepting clients, so an attach immediately after start finds them. Runs
	// synchronously; a single corrupt file is archived and skipped, never fatal.
	if !d.disableAutoRestore {
		d.restoreAllSessions()
	}

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

// closeDone closes cs.done exactly once, even if shutdown races the connection
// goroutine.
func (cs *connState) closeDone() {
	cs.doneOnce.Do(func() { close(cs.done) })
}

// drop tears a client down after an unrecoverable send failure. A write that
// fails mid-frame (e.g. a slow client hitting the write deadline) leaves a
// partial frame on the wire and permanently desyncs framing, so the only
// coherent recovery is to close done and the connection: that unblocks the read
// loop, whose deferred cleanup then unsubscribes every PTY, removes the client,
// and purges its pending requests. Safe to call from any goroutine and more than
// once (closeDone is once-guarded and Close is idempotent).
func (cs *connState) drop() {
	cs.closeDone()
	_ = cs.conn.Close()
}

func (d *Daemon) shutdown() error {
	d.shutdownOnce.Do(func() {
		log.Println("Shutting down daemon...")

		if d.listener != nil {
			_ = d.listener.Close()
		}

		d.clientsMu.Lock()
		for _, cs := range d.clients {
			cs.closeDone()
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
	})
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

// shortID returns the first 8 bytes of s, or all of s if it is shorter. IDs
// reaching the daemon can be client-controlled and arbitrarily short, so a
// plain s[:8] slice would panic; this makes ID truncation for logs safe.
func shortID(s string) string {
	if len(s) < 8 {
		return s
	}
	return s[:8]
}

func (d *Daemon) handleConnection(conn net.Conn) {
	// A panic on the untrusted client-parsed message surface must not take down
	// the daemon and every other session. Recover, log, and drop just this
	// client. Registered before the cleanup defer below so cleanup (which closes
	// the connection and unsubscribes) runs first on unwind; conn.Close here is
	// a defensive backstop for a panic before that defer is installed.
	defer func() {
		if r := recover(); r != nil {
			log.Printf("PANIC in handleConnection: %v\n%s", r, debug.Stack())
			_ = conn.Close()
		}
	}()

	clientID := fmt.Sprintf("client-%d", time.Now().UnixNano())

	cs := &connState{
		conn:             conn,
		clientID:         clientID,
		done:             make(chan struct{}),
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

		// Snapshot subscriptions and session under cs.mu before unsubscribing.
		cs.mu.Lock()
		sessionID := cs.sessionID
		subs := make([]string, 0, len(cs.ptySubscriptions))
		for ptyID := range cs.ptySubscriptions {
			subs = append(subs, ptyID)
		}
		cs.mu.Unlock()

		// Unsubscribe from all PTYs
		if sessionID != "" {
			if session := d.manager.GetSessionByID(sessionID); session != nil {
				for _, ptyID := range subs {
					if pty := session.GetPTY(ptyID); pty != nil {
						pty.Unsubscribe(clientID)
					}
				}
			}
		}

		// Purge any pending requests this client was waiting on so its
		// connState is not pinned forever.
		d.pendingRequestsMu.Lock()
		for id, pr := range d.pendingRequests {
			if pr.requester == cs {
				delete(d.pendingRequests, id)
			}
		}
		d.pendingRequestsMu.Unlock()

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

		// Short deadline only detects the message boundary (for done/ctx
		// checks); the body gets a longer deadline so a large payload cannot be
		// cut mid-frame and desync framing.
		msg, codecType, err := ReadMessageConn(conn, 100*time.Millisecond, 30*time.Second)
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
	case MsgResurrect:
		return d.handleResurrect(cs, msg)
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
	case MsgCapturePane:
		return d.handleCapturePane(cs, msg)
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

func (d *Daemon) cleanupLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	const pendingRequestTTL = 2 * time.Minute

	for {
		select {
		case <-d.ctx.Done():
			return
		case <-ticker.C:
			// Expire stale pending requests whose TUI result never arrived so
			// they do not pin the requester's connState forever.
			now := time.Now()
			d.pendingRequestsMu.Lock()
			for id, pr := range d.pendingRequests {
				if now.Sub(pr.created) > pendingRequestTTL {
					delete(d.pendingRequests, id)
				}
			}
			d.pendingRequestsMu.Unlock()
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
