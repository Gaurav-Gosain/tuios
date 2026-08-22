package session

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
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

	// events is the control-plane event hub backing the subscribe verb's event
	// stream and the wait-for verb's blocking waits.
	events *eventHub

	// Goroutine tracking for clean shutdown
	wg sync.WaitGroup

	// shutdownOnce makes shutdown idempotent (Run and Stop can both call it).
	shutdownOnce sync.Once

	// startLock is the exclusive lock that serialises daemon startup. It is held
	// open for the daemon's life, since releasing it early would let a second
	// starter reach the stale-socket recovery while this one is still listening.
	startLock *os.File

	// Configuration
	version string

	// disableAutoRestore, when true, skips cold-start resurrection of saved
	// sessions on daemon start. Sessions can still be brought back on demand
	// with the resurrect verb.
	disableAutoRestore bool

	// agentStallTimeout is how long a pane may report working while producing no
	// output before the stall heuristic demotes it to idle. Zero disables the
	// heuristic. It is resolved once in NewDaemon from config or the
	// TUIOS_AGENT_STALL_SECONDS environment override.
	agentStallTimeout time.Duration

	// agentDetectInterval is how often the foreground-process auto-detector polls
	// each pane to mark or clear a running agent. Zero disables auto-detection. It
	// is resolved once in NewDaemon from config or the TUIOS_AGENT_DETECT_SECONDS
	// environment override.
	agentDetectInterval time.Duration

	// agentMatcher decides whether a pane's foreground process is a known agent
	// CLI. It merges the built-in agent binary names with any the user added.
	agentMatcher agentMatcher

	// transcriptWatcher is the one filesystem notification the transcript source
	// runs on, shared by every session. Nil when the kernel would not give the
	// daemon one, in which case every join reads on its pane's own output
	// instead and nothing else changes.
	transcriptWatcher *TranscriptWatcher
}

// defaultAgentStallTimeout is the conservative default silence window before a
// pane that reported working but never reported anything after is assumed idle.
// It is long on purpose: the heuristic is a fallback for agents that do not
// report, and demoting a genuinely-busy-but-quiet pane too eagerly is worse than
// leaving it looking busy a little longer.
const defaultAgentStallTimeout = 30 * time.Second

// defaultAgentDetectInterval is how often the foreground-process auto-detector
// polls each pane. It is modest on purpose: agent presence changes on a human
// timescale, and a per-pane /proc read every couple of seconds is cheap.
const defaultAgentDetectInterval = 2 * time.Second

// pendingRequest tracks a routed command awaiting its result, with the time it
// was created so cleanupLoop can expire stale entries.
//
// A request is delivered one of two ways when the TUI replies:
//   - requester != nil: the result is forwarded to that connection as a normal
//     MsgCommandResult (the binary control path).
//   - resultCh != nil: the result is handed to a goroutine blocked in
//     routeToTUISync (the JSON verb path), which then writes the JSON response.
type pendingRequest struct {
	requester *connState
	resultCh  chan *CommandResultPayload
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
	// ptyResume is where each PTY's stream had got to when this client last
	// unsubscribed, so hiding and showing a pane resumes rather than replays.
	// It lives on the connection because that is what owns its lifetime: the
	// positions go away with the client instead of accumulating on the PTY.
	ptyResume map[string]int64

	// Event stream state (JSON verb protocol). eventSub is the hub subscription
	// once this connection has issued a subscribe verb; streaming guards against a
	// second subscribe on the same connection; pendingStream hands the fresh
	// subscription from the subscribe handler to the dispatch loop, which starts
	// the streamer only after the ack response has been written.
	eventSub      *eventSub
	pendingStream *eventSub
	streaming     bool

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
	// AgentStallTimeout overrides how long a pane may report working with no
	// output before the stall heuristic demotes it to idle. Zero falls back to
	// the TUIOS_AGENT_STALL_SECONDS environment override, then to the default; a
	// negative value disables the heuristic.
	AgentStallTimeout time.Duration
	// AgentAutoDetect toggles the foreground-process agent auto-detector. Nil
	// falls back to the TUIOS_AGENT_AUTODETECT environment override, then to
	// enabled. A non-nil false disables it.
	AgentAutoDetect *bool
	// AgentDetectInterval overrides the auto-detector's poll interval. Zero falls
	// back to the TUIOS_AGENT_DETECT_SECONDS environment override, then to the
	// default; a negative value disables auto-detection.
	AgentDetectInterval time.Duration
	// AgentBinaries are extra binary names to treat as agents, merged with the
	// built-in defaults. It also picks up the TUIOS_AGENT_BINARIES environment
	// override (comma-separated).
	AgentBinaries []string
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
		events:             newEventHub(),
		version:            cfg.Version,
		disableAutoRestore: cfg.DisableAutoRestore,
		agentStallTimeout:  resolveAgentStallTimeout(cfg.AgentStallTimeout),
		agentMatcher:       newAgentMatcher(resolveAgentBinaries(cfg.AgentBinaries)),
	}
	d.agentDetectInterval = resolveAgentDetectInterval(cfg.AgentAutoDetect, cfg.AgentDetectInterval)

	// A daemon with no watcher is a working daemon: every transcript join falls
	// back to reading on its pane's own output. So the error is dropped rather
	// than reported, and it is dropped rather than logged because the only thing
	// fsnotify would say is which path it failed on.
	if w, err := NewTranscriptWatcher(); err == nil {
		d.transcriptWatcher = w
	}

	if cfg.SocketPath != "" {
		d.manager.SetSocketPath(cfg.SocketPath)
	}

	// Wire session lifecycle to the event hub: every session created through the
	// manager gets an event sink that publishes to the hub, and creation/deletion
	// raise session lifecycle events.
	d.manager.SetSessionHooks(d.onSessionCreated, d.onSessionDeleted)

	return d
}

// onSessionCreated installs a session's event and state sinks and publishes a
// session-created event. It runs on the manager's create hook.
func (d *Daemon) onSessionCreated(s *Session) {
	name := s.Name
	// Every daemon-side mutation reaches the attached clients from here, so a
	// change the daemon made itself shows up in a live TUI without the verb that
	// made it knowing a client exists. Source is empty because the daemon, not a
	// client, is the origin: every attached client needs to hear it.
	sessionID := s.ID
	if d.transcriptWatcher != nil {
		s.SetTranscriptWatcher(d.transcriptWatcher)
	}
	s.SetStateSink(func(state *SessionState) {
		d.broadcastStateSync(sessionID, state, "update", "")
	})
	s.SetEventSink(func(ev SessionEvent) {
		// A pane's own output is the signal that its agent quit: when the agent
		// leaves the foreground the shell prompt returns as output, so probe that
		// pane and clear an auto-detected glyph at once rather than waiting for the
		// next detection poll. Throttled per PTY so a busy pane pays no cost.
		if ev.Type == EventOutput {
			if pty := s.GetPTY(ev.PTYID); pty != nil {
				// An OSC 9;4 the emulator parked while writing these same bytes. It
				// is applied before the probe and is not throttled: the sequence
				// only arrives when the harness has something to say, and it is a
				// better answer than anything the probe can work out.
				if state, ok := pty.takeAgentProgress(); ok {
					s.applyAgentProgress(ev.Window, state)
				}
				if d.agentDetectInterval > 0 && pty.probeAgentExitDue(time.Now().UnixNano()) {
					s.reconcileAgentOnOutput(ev.PTYID, d.foregroundResolver(s), d.agentMatcher.identify)
				}
				// The screen tier. Throttled like the probe, and armed to run once
				// more after the pane goes quiet: a harness waiting on a human
				// paints the prompt in its last chunk and then says nothing at
				// all, so the scan the throttle swallowed is the only one that
				// would have seen it.
				reg := d.agentMatcher.registry
				if reg != nil {
					ptyID := ev.PTYID
					// The transcript read goes first so the screen gets the last
					// word in a single pass: the file can only say working or
					// done, and a rule that can see a prompt on the pane right
					// now must be able to write over that. Running them the
					// other way round would let a read of a file the agent wrote
					// a moment ago undo a blocker the screen just matched.
					// Installed on first use, not at construction: the registry
					// belongs to the daemon and a PTY is built before the daemon
					// wires this sink. Guarded so a flooding pane does not build
					// an identical closure again on every chunk just to store it;
					// the matcher is fixed for the daemon's life, so the first
					// one stays correct.
					if !pty.hasScreenLook() {
						pty.setScreenLook(func() {
							s.readTranscriptOnOutput(ptyID)
							s.scanScreenForAgent(ptyID, reg)
						})
					}
					if pty.screenScanDue(time.Now().UnixNano()) {
						pty.runScreenLook()
					}
					pty.armScreenSettle()
				}
			}
		}
		d.events.publish(streamEvent{
			Type:      ev.Type,
			Session:   name,
			Window:    ev.Window,
			PTYID:     ev.PTYID,
			Title:     ev.Title,
			Bytes:     ev.Bytes,
			Mode:      ev.Mode,
			Enabled:   ev.Enabled,
			Workspace: ev.Workspace,
		})
	})
	d.events.publish(streamEvent{Type: EventSessionCreated, Session: name})
}

// onSessionDeleted publishes a session-closed event and tells every client
// attached to the session that it is gone. It runs on the manager's delete hook,
// so every deletion path (the kill-session verb, the legacy kill message, and
// any internal teardown) notifies clients through one place.
//
// Without this a killed session leaves its clients attached to nothing: their
// PTYs are closed and their windows are gone, but the socket stays open, so the
// client sits in a dead session with no way to learn what happened.
func (d *Daemon) onSessionDeleted(s *Session) {
	d.events.publish(streamEvent{Type: EventSessionClosed, Session: s.Name})
	d.broadcastToSession(s.ID, MsgSessionEnded, &SessionEndedPayload{
		SessionName: s.Name,
		Reason:      "the session was terminated",
	}, "")
}

// Start starts the daemon.
func (d *Daemon) Start() error {
	socketPath := d.manager.SocketPath()

	// Held until shutdown so the stale-socket recovery below can never run
	// against a daemon that is itself between bind and listen.
	startLock, err := acquireStartLock()
	if err != nil {
		return err
	}
	d.startLock = startLock
	defer func() {
		if d.listener == nil {
			d.releaseStartLock()
		}
	}()

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

	// Sweep the previous daemon's leftovers before reading the directory. Runs
	// whether or not auto-restore is on, since the residue is there either way.
	CleanResurrectionDir()

	// Restore sessions saved before the previous shutdown/crash before we start
	// accepting clients, so an attach immediately after start finds them. Runs
	// synchronously; a single corrupt file is archived and skipped, never fatal.
	if !d.disableAutoRestore {
		d.restoreAllSessions()
	}

	go d.handleSignals()
	go d.acceptLoop()
	go d.cleanupLoop()
	go d.stallMonitor()
	go d.agentMonitor()

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

		// Closing the watcher ends its goroutine and returns every inotify watch
		// to the kernel, which matters on a machine where several daemons have
		// come and gone.
		_ = d.transcriptWatcher.Close()

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

		// Stopping the manager stops every session, and each session's Stop
		// writes its final resurrection state synchronously. When this returns,
		// everything that will be persisted has been persisted.
		d.manager.Shutdown()

		// Unlinking the socket is deliberately the last thing the daemon does,
		// after the final resurrection saves and after the pid file. It is the
		// signal 'tuios kill-server' waits on, so anything ordered after it
		// would make that signal a lie: a caller could observe the socket gone,
		// start a new daemon, and race a write from the old one. Closing the
		// listener is not a usable signal for the same reason, since it happens
		// at the top of shutdown while state is still unsaved.
		pidPath, err := GetPidFilePath()
		if err == nil {
			_ = os.Remove(pidPath)
		}

		_ = os.Remove(d.manager.SocketPath())

		// After the socket is gone, so the next daemon can only take the lock
		// once there is nothing left of this one to race.
		d.releaseStartLock()

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
		ptyResume:        make(map[string]int64),
	}

	LogBasic("Client %s connected", clientID)

	d.clientsMu.Lock()
	d.clients[clientID] = cs
	d.clientsMu.Unlock()

	defer func() {
		LogBasic("Client %s disconnected", clientID)

		// Wake any per-connection background goroutines (the event streamer) so
		// they observe the disconnect and unwind, and release a lingering event
		// subscription if the streamer never started (e.g. the ack write failed).
		cs.closeDone()
		cs.mu.Lock()
		sub := cs.eventSub
		cs.eventSub = nil
		cs.mu.Unlock()
		if sub != nil {
			d.events.unsubscribe(sub)
		}

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

		// A client that drops without detaching has still left, and the effective
		// session size is the minimum over the clients that are here. Nothing
		// recomputed it on this path, so a browser tab closed on a phone left
		// every other client boxed into the phone's columns for good. Runs after
		// the client is out of d.clients, so the size is taken over the ones that
		// remain; a clean detach cleared sessionID above and does not come here.
		if sessionID != "" {
			d.notifyClientLeft(sessionID, clientID)
		}

		_ = conn.Close()
	}()

	// Wrap the connection so the first byte can be inspected without consuming
	// it from the binary read path. A JSON verb-protocol client sends a line
	// starting with '{' (or leading whitespace); a binary client's first byte is
	// the high byte of a big-endian length prefix, which is 0x00 or 0x01 for any
	// frame under the 16MB cap and so never collides with '{' or whitespace.
	br := bufio.NewReaderSize(conn, 64*1024)
	if d.detectJSONClient(cs, br) {
		d.handleJSONConnection(cs, br)
		return
	}

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
		msg, codecType, err := ReadMessageBuffered(conn, br, 100*time.Millisecond, 30*time.Second)
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
	case MsgCreatePTY:
		return d.handleCreatePTY(cs, msg)
	case MsgClosePTY:
		return d.handleClosePTY(cs, msg)
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
	case MsgCommandResult:
		return d.handleCommandResult(cs, msg)
	case MsgGetLogs:
		return d.handleGetLogs(cs, msg)
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

// resolveAgentStallTimeout picks the stall heuristic's silence window: an
// explicit positive config wins, a negative config disables the heuristic, and a
// zero config falls back to the TUIOS_AGENT_STALL_SECONDS environment override
// (0 or less there disables it) and finally to the default.
func resolveAgentStallTimeout(cfg time.Duration) time.Duration {
	if cfg > 0 {
		return cfg
	}
	if cfg < 0 {
		return 0
	}
	if s := os.Getenv("TUIOS_AGENT_STALL_SECONDS"); s != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
			if n <= 0 {
				return 0
			}
			return time.Duration(n) * time.Second
		}
	}
	return defaultAgentStallTimeout
}

// resolveAgentBinaries returns the extra agent binary names to merge with the
// built-in defaults: the config list, plus the comma-separated
// TUIOS_AGENT_BINARIES environment override.
func resolveAgentBinaries(cfg []string) []string {
	extra := append([]string(nil), cfg...)
	if s := os.Getenv("TUIOS_AGENT_BINARIES"); s != "" {
		for name := range strings.SplitSeq(s, ",") {
			if name = strings.TrimSpace(name); name != "" {
				extra = append(extra, name)
			}
		}
	}
	return extra
}

// resolveAgentDetectInterval picks the auto-detector's poll interval and, with
// it, whether auto-detection runs at all. An explicit enable/disable from config
// wins; then an explicit positive interval wins; a negative interval disables it;
// a zero interval falls back to the TUIOS_AGENT_DETECT_SECONDS environment
// override (0 or less there disables it), and finally to the default. A returned
// zero means auto-detection is off.
func resolveAgentDetectInterval(enabled *bool, cfg time.Duration) time.Duration {
	if enabled != nil && !*enabled {
		return 0
	}
	if enabled == nil {
		if v := strings.TrimSpace(os.Getenv("TUIOS_AGENT_AUTODETECT")); v != "" {
			switch strings.ToLower(v) {
			case "0", "false", "no", "off":
				return 0
			}
		}
	}
	if cfg > 0 {
		return cfg
	}
	if cfg < 0 {
		return 0
	}
	if s := os.Getenv("TUIOS_AGENT_DETECT_SECONDS"); s != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
			if n <= 0 {
				return 0
			}
			return time.Duration(n) * time.Second
		}
	}
	return defaultAgentDetectInterval
}

// agentMonitor periodically resolves the foreground process of every pane and
// marks or clears a running agent, so the status glyph appears without the user
// running set-agent-state. It is strictly subordinate to explicit reports and to
// the stall heuristic (see Session.applyAgentDetection). It exits when the daemon
// context is cancelled, and does nothing at all when auto-detection is disabled.
func (d *Daemon) agentMonitor() {
	if d.agentDetectInterval <= 0 {
		return
	}
	ticker := time.NewTicker(d.agentDetectInterval)
	defer ticker.Stop()

	for {
		select {
		case <-d.ctx.Done():
			return
		case <-ticker.C:
			reg := d.agentMatcher.registry
			for _, sess := range d.manager.AllSessions() {
				sess.applyAgentDetection(d.foregroundResolver(sess), d.agentMatcher.identify)
				// The transcript joins ride this tick rather than one of their
				// own. It runs only for a pane already known to be running a
				// harness that has a transcript and that has no join yet, so a
				// session that is fully joined, or that runs no agent at all,
				// does nothing here.
				sess.maintainAgentTranscripts(reg, d.paneAgentIdentifier(sess))
			}
		}
	}
}

// paneAgentIdentifier reports the working directory and build version of the
// agent in a pane, which is what a searched transcript candidate is checked
// against before it is believed.
func (d *Daemon) paneAgentIdentifier(sess *Session) func(ptyID string) (string, string) {
	resolve := d.foregroundResolver(sess)
	return func(ptyID string) (string, string) {
		info, running := resolve(ptyID)
		if !running {
			return "", ""
		}
		return paneAgentIdentity(info)
	}
}

// foregroundResolver returns the resolve function the agent detector and the
// output-driven exit probe share: the foreground process of a pane's controlling
// terminal, or not-running when the PTY is gone or has exited.
func (d *Daemon) foregroundResolver(sess *Session) func(ptyID string) (foregroundInfo, bool) {
	return func(ptyID string) (foregroundInfo, bool) {
		pty := sess.GetPTY(ptyID)
		if pty == nil || pty.IsExited() {
			return foregroundInfo{}, false
		}
		return foregroundProcess(pty.ShellPID())
	}
}

// stallMonitor periodically applies the agent-state output-stall heuristic to
// every live session, demoting panes that reported working but have gone quiet
// to idle after the screen tier has had a last look at them. It is the fallback
// for agents that never report their own state and is strictly secondary to
// explicit reports (see Session.applyStallHeuristic). It exits when the daemon
// context is cancelled, and does nothing at all when the heuristic is disabled.
func (d *Daemon) stallMonitor() {
	if d.agentStallTimeout <= 0 {
		return
	}
	// Tick often enough to demote within a fraction of the timeout, but never
	// spin: at least once a second, at most once every ten.
	interval := min(max(d.agentStallTimeout/4, time.Second), 10*time.Second)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-d.ctx.Done():
			return
		case <-ticker.C:
			now := time.Now()
			reg := d.agentMatcher.registry
			for _, sess := range d.manager.AllSessions() {
				sess.applyStallHeuristic(now, d.agentStallTimeout, func(ptyID string) int64 {
					if pty := sess.GetPTY(ptyID); pty != nil {
						return pty.LastOutput()
					}
					return 0
				}, func(ptyID string) bool {
					// The last look before the pane is called idle. A stalled pane
					// emits nothing, so the scan the output path would have run is
					// the one that never happens.
					return sess.scanScreenForAgent(ptyID, reg)
				})
			}
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
