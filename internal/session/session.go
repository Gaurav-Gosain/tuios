package session

import (
	"context"
	"fmt"

	"log"
	"maps"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	xpty "github.com/charmbracelet/x/xpty"
	"github.com/google/uuid"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// debugEnabled returns true if debug logging is enabled via TUIOS_DEBUG_INTERNAL env var
func debugEnabled() bool {
	return os.Getenv("TUIOS_DEBUG_INTERNAL") == "1"
}

// debugLog logs a message only if debug mode is enabled
func debugLog(format string, args ...any) {
	if debugEnabled() {
		log.Printf(format, args...)
	}
}

// WindowState represents the serializable state of a window.
type WindowState struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	CustomName   string `json:"custom_name,omitempty"`
	X            int    `json:"x"`
	Y            int    `json:"y"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	Z            int    `json:"z"`
	Workspace    int    `json:"workspace"`
	Minimized    bool   `json:"minimized,omitempty"`
	PreMinimizeX int    `json:"pre_minimize_x,omitempty"`
	PreMinimizeY int    `json:"pre_minimize_y,omitempty"`
	PreMinimizeW int    `json:"pre_minimize_w,omitempty"`
	PreMinimizeH int    `json:"pre_minimize_h,omitempty"`
	PTYID        string `json:"pty_id"`                  // Reference to daemon-managed PTY
	IsAltScreen  bool   `json:"is_alt_screen,omitempty"` // Alternate screen buffer active (for mouse forwarding)
	// Cwd is the working directory of the window's shell process, captured on the
	// daemon side when saving resurrection state. On cold-start restore a fresh
	// shell is respawned here. Empty for live state syncs (clients do not set it).
	Cwd string `json:"cwd,omitempty"`
}

// SerializedBSPNode represents a BSP tree node for serialization
type SerializedBSPNode struct {
	WindowID   int                `json:"window_id"`
	SplitType  int                `json:"split_type"`
	SplitRatio float64            `json:"split_ratio"`
	Left       *SerializedBSPNode `json:"left,omitempty"`
	Right      *SerializedBSPNode `json:"right,omitempty"`
}

// SerializedBSPTree represents a BSP tree for serialization
type SerializedBSPTree struct {
	Root         *SerializedBSPNode `json:"root,omitempty"`
	AutoScheme   int                `json:"auto_scheme"`
	DefaultRatio float64            `json:"default_ratio"`
}

// SessionState represents the complete serializable state of a session.
type SessionState struct {
	Name             string         `json:"name"`
	Windows          []WindowState  `json:"windows"`
	FocusedWindowID  string         `json:"focused_window_id,omitempty"`
	CurrentWorkspace int            `json:"current_workspace"`
	WorkspaceFocus   map[int]string `json:"workspace_focus,omitempty"` // workspace -> focused window ID
	MasterRatio      float64        `json:"master_ratio"`
	AutoTiling       bool           `json:"auto_tiling"`
	Width            int            `json:"width"`
	Height           int            `json:"height"`
	// Mode: 0 = WindowManagementMode, 1 = TerminalMode
	Mode int `json:"mode"`
	// BSP tiling state
	WorkspaceTrees  map[int]*SerializedBSPTree `json:"workspace_trees,omitempty"`  // BSP tree per workspace
	WindowToBSPID   map[string]int             `json:"window_to_bsp_id,omitempty"` // Window UUID -> BSP int ID
	NextBSPWindowID int                        `json:"next_bsp_window_id,omitempty"`
	TilingScheme    int                        `json:"tiling_scheme,omitempty"` // Default auto-insertion scheme
	// ResurrectionVersion tags the on-disk state schema. It is stamped by
	// SaveSessionForResurrection (not by clients) and checked on load so that
	// state written by a newer, incompatible tuios is archived rather than
	// misinterpreted. Absent (0) means pre-versioning state, which is a
	// structural subset of the current schema and loads fine.
	ResurrectionVersion int `json:"resurrection_version,omitempty"`
	// Options is a daemon-owned key/value store for session options set through
	// the JSON verb protocol (set-option / get-option). It is additive: older
	// clients and older on-disk state simply omit it. Keys are advisory names;
	// the daemon records them verbatim so a later get-option can read them back
	// and an attached TUI can apply the ones it understands.
	Options map[string]string `json:"options,omitempty"`
}

// PTY represents a daemon-managed pseudo-terminal.
type PTY struct {
	ID     string
	pty    xpty.Pty
	cmd    *exec.Cmd
	ctx    context.Context
	cancel context.CancelFunc

	// Terminal emulator - maintains scrollback, screen state, cursor position
	// This persists across client disconnect/reconnect
	terminal   *vt.Emulator
	terminalMu sync.RWMutex
	width      int
	height     int

	// Output buffer for reconnection (ring buffer) - legacy, kept for raw output
	outputMu     sync.RWMutex
	outputBuffer []byte
	outputPos    int

	// Subscribers for raw output streaming.
	subscribers   map[string]chan []byte
	subscribersMu sync.RWMutex

	exited   bool
	exitedMu sync.RWMutex
	exitCode int

	// Single-goroutine VT writer channel. Closed by readOutput on exit so
	// vtWriter's range terminates.
	vtWriteChan chan []byte

	// Callback when PTY process exits - used by daemon to notify clients
	onExit func(ptyID string)

	// emit, when set, raises a control-plane event (output activity, bell, mode
	// change, process exit) already tagged with this PTY's window and PTY ID. It
	// is a no-op when the session has no event sink installed.
	emit func(SessionEvent)
}

// Session represents a persistent TUIOS session.
// The daemon manages PTYs and stores state; the client runs the TUI.
type Session struct {
	// Identity
	ID   string
	Name string

	// PTYs managed by this session
	ptys   map[string]*PTY
	ptysMu sync.RWMutex

	// Session state (serializable)
	state            *SessionState
	stopResurrection func() // Stops periodic resurrection saving
	stateMu          sync.RWMutex

	// eventSink, when set, receives control-plane events raised by this session
	// and its PTYs (window lifecycle, output activity, bell, mode changes). The
	// daemon installs it so events reach the event hub; nil for a session with no
	// hub (e.g. bare unit tests).
	eventSink   func(SessionEvent)
	eventSinkMu sync.RWMutex

	// Terminal size
	width  int
	height int
	sizeMu sync.RWMutex

	// Lifecycle
	Created    time.Time
	LastActive time.Time

	// Configuration
	config *SessionConfig
}

// SessionConfig holds configuration for a session.
type SessionConfig struct {
	Term      string
	ColorTerm string
	Shell     string
}

// NewSession creates a new persistent session.
func NewSession(name string, cfg *SessionConfig, width, height int) (*Session, error) {
	id := uuid.New().String()
	if name == "" {
		name = fmt.Sprintf("session-%s", id[:8])
	}

	now := time.Now()

	session := &Session{
		ID:   id,
		Name: name,
		ptys: make(map[string]*PTY),
		state: &SessionState{
			Name:             name,
			Windows:          []WindowState{},
			CurrentWorkspace: 1,
			WorkspaceFocus:   make(map[int]string),
			MasterRatio:      0.5,
			Width:            width,
			Height:           height,
		},
		width:      width,
		height:     height,
		Created:    now,
		LastActive: now,
		config:     cfg,
	}

	// Start periodic resurrection saving
	session.stopResurrection = StartPeriodicSave(func() *SessionState {
		return session.ResurrectionState()
	})

	return session, nil
}

// SetEventSink installs the control-plane event sink for this session. It is
// safe to call concurrently and may be set after windows already exist; the
// per-PTY emitters read it dynamically.
func (s *Session) SetEventSink(fn func(SessionEvent)) {
	s.eventSinkMu.Lock()
	s.eventSink = fn
	s.eventSinkMu.Unlock()
}

// emit forwards a control-plane event to the installed sink, if any.
func (s *Session) emit(ev SessionEvent) {
	s.eventSinkMu.RLock()
	fn := s.eventSink
	s.eventSinkMu.RUnlock()
	if fn != nil {
		fn(ev)
	}
}

// CreatePTY creates a new PTY in this session. windowID, if non-empty, is the
// client-side window UUID exported to the shell as TUIOS_WINDOW_ID. onExit, if
// non-nil, is invoked with the PTY ID when the process exits; it is set before
// the monitor goroutine starts so it is always visible to monitorExit.
func (s *Session) CreatePTY(windowID string, width, height int, onExit func(ptyID string)) (*PTY, error) {
	return s.createPTY(windowID, width, height, "", false, onExit)
}

// RestorePTY creates a fresh PTY for a resurrected window. It behaves like
// CreatePTY but starts the shell in cwd (when that directory still exists) and
// marks the shell as restored: the shell's environment carries TUIOS_RESTORED=1
// and a one-line banner is written to the terminal so the user can see the
// process is a freshly respawned shell, not the original long-lived one.
func (s *Session) RestorePTY(windowID string, width, height int, cwd string, onExit func(ptyID string)) (*PTY, error) {
	return s.createPTY(windowID, width, height, cwd, true, onExit)
}

func (s *Session) createPTY(windowID string, width, height int, cwd string, restored bool, onExit func(ptyID string)) (*PTY, error) {
	s.ptysMu.Lock()
	defer s.ptysMu.Unlock()

	id := uuid.New().String()
	ctx, cancel := context.WithCancel(context.Background())

	shell := s.getShell()

	// Create PTY
	ptyInstance, err := xpty.NewPty(width, height)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create PTY: %w", err)
	}

	// Create command
	cmd := exec.Command(shell)
	cmd.Env = s.buildEnv(windowID, restored)
	// Start a restored shell in its saved working directory when it still
	// exists; otherwise fall back to the shell's default (inherited) directory.
	if restored && cwd != "" {
		if info, statErr := os.Stat(cwd); statErr == nil && info.IsDir() {
			cmd.Dir = cwd
		}
	}

	// Set up the command to use the PTY as controlling terminal
	// This is required for interactive shells to work properly
	// Platform-specific setup is in pty_unix.go and pty_windows.go
	configurePTYCommand(cmd)

	// Start command in PTY
	if err := ptyInstance.Start(cmd); err != nil {
		_ = ptyInstance.Close()
		cancel()
		return nil, fmt.Errorf("failed to start shell: %w", err)
	}

	// Create VT emulator for persistent terminal state
	// This maintains scrollback, screen content, cursor position across reconnects
	terminal := vt.NewEmulator(width, height)
	terminal.SetScrollbackMaxLines(10000) // Match default scrollback

	// For a restored shell, seed the emulator with a one-line banner so the
	// respawned process is clearly marked. This is written directly (before the
	// reader/writer goroutines start) so it lands at the top of the screen ahead
	// of the shell's first prompt; it only touches the daemon-side emulator and
	// never the real PTY, so the shell is unaffected.
	if restored {
		_, _ = terminal.Write([]byte(restoredBanner(cwd)))
	}

	pty := &PTY{
		ID:           id,
		pty:          ptyInstance,
		cmd:          cmd,
		ctx:          ctx,
		cancel:       cancel,
		terminal:     terminal,
		width:        width,
		height:       height,
		outputBuffer: make([]byte, 64*1024), // 64KB ring buffer
		subscribers:  make(map[string]chan []byte),
		vtWriteChan:  make(chan []byte, 256),
		onExit:       onExit,
	}

	// Per-PTY control-plane event emitter, pre-tagged with this window and PTY
	// ID. It routes through the session's event sink so events reach the daemon's
	// event hub; when no sink is installed it is a cheap no-op.
	pty.emit = func(ev SessionEvent) {
		ev.Window = windowID
		ev.PTYID = id
		s.emit(ev)
	}

	// Raise control-plane events from the daemon-side VT emulator: bell, an
	// app-driven title change, and alt-screen mode toggles. These fire from the
	// single vtWriter goroutine; the emitter only does a non-blocking hub publish,
	// so it never re-enters the terminal lock held during Write.
	terminal.SetCallbacks(vt.Callbacks{
		Bell:  func() { pty.emit(SessionEvent{Type: EventBell}) },
		Title: func(title string) { pty.emit(SessionEvent{Type: EventWindowRetitled, Title: title}) },
		AltScreen: func(on bool) {
			pty.emit(SessionEvent{Type: EventModeChanged, Mode: "alt-screen", Enabled: on})
		},
	})

	// Handle kitty graphics queries on the daemon side for low-latency
	// responses. All other commands flow through the raw PTY broadcast.
	terminal.SetKittyPassthroughFunc(func(cmd *vt.KittyCommand, rawData []byte) {
		if cmd.Action == vt.KittyActionQuery {
			response := vt.BuildKittyResponse(true, cmd.ImageID, "")
			terminal.WriteResponse(response)
			return
		}
	})

	s.ptys[id] = pty

	// Start VT writer goroutine (single, persistent)
	go pty.vtWriter()

	// Start output reader
	go pty.readOutput()

	// Start terminal response forwarder - the daemon's emulator generates query responses
	// (DA, CPR, etc.) which must be sent to the PTY for applications to receive.
	// Client emulators DRAIN their responses to prevent duplicates.
	go pty.forwardTerminalResponses()

	// Monitor process exit
	go pty.monitorExit()

	s.LastActive = time.Now()
	return pty, nil
}

// GetPTY returns a PTY by ID.
func (s *Session) GetPTY(id string) *PTY {
	s.ptysMu.RLock()
	defer s.ptysMu.RUnlock()
	return s.ptys[id]
}

// ClosePTY closes and removes a PTY.
func (s *Session) ClosePTY(id string) error {
	s.ptysMu.Lock()
	defer s.ptysMu.Unlock()

	pty, exists := s.ptys[id]
	if !exists {
		return fmt.Errorf("PTY %s not found", id)
	}

	delete(s.ptys, id)
	return pty.Close()
}

// ListPTYIDs returns all PTY IDs in this session.
func (s *Session) ListPTYIDs() []string {
	s.ptysMu.RLock()
	defer s.ptysMu.RUnlock()

	ids := make([]string, 0, len(s.ptys))
	for id := range s.ptys {
		ids = append(ids, id)
	}
	return ids
}

// PTYCount returns the number of PTYs.
func (s *Session) PTYCount() int {
	s.ptysMu.RLock()
	defer s.ptysMu.RUnlock()
	return len(s.ptys)
}

// GetState returns the current session state.
func (s *Session) GetState() *SessionState {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()

	// Return a copy
	stateCopy := *s.state
	stateCopy.Windows = make([]WindowState, len(s.state.Windows))
	copy(stateCopy.Windows, s.state.Windows)
	if s.state.WorkspaceFocus != nil {
		stateCopy.WorkspaceFocus = make(map[int]string)
		maps.Copy(stateCopy.WorkspaceFocus, s.state.WorkspaceFocus)
	}
	if s.state.Options != nil {
		stateCopy.Options = make(map[string]string, len(s.state.Options))
		maps.Copy(stateCopy.Options, s.state.Options)
	}
	return &stateCopy
}

// SetOption records a daemon-owned session option under stateMu. It is the write
// side of the JSON verb protocol's set-option and is safe for concurrent use.
func (s *Session) SetOption(key, value string) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.state.Options == nil {
		s.state.Options = make(map[string]string)
	}
	s.state.Options[key] = value
}

// GetOption reads a daemon-owned session option under stateMu, returning the
// value and whether the key was set. It is the read side of get-option.
func (s *Session) GetOption(key string) (string, bool) {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	if s.state.Options == nil {
		return "", false
	}
	v, ok := s.state.Options[key]
	return v, ok
}

// AllOptions returns a copy of every daemon-owned session option.
func (s *Session) AllOptions() map[string]string {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	out := make(map[string]string, len(s.state.Options))
	maps.Copy(out, s.state.Options)
	return out
}

// ResurrectionState returns a copy of the session state enriched for on-disk
// resurrection: each window's Cwd is filled from its live PTY process so a
// cold-start restore can respawn the shell in the same directory. Clients never
// send Cwd, so this daemon-side capture is the only source of it.
func (s *Session) ResurrectionState() *SessionState {
	state := s.GetState()
	for i := range state.Windows {
		ptyID := state.Windows[i].PTYID
		if ptyID == "" {
			continue
		}
		if pty := s.GetPTY(ptyID); pty != nil {
			if cwd, ok := pty.ProcessCwd(); ok {
				state.Windows[i].Cwd = cwd
			}
		}
	}
	return state
}

// UpdateState updates the session state. Daemon-owned options (set through the
// JSON verb protocol) are carried over when the incoming state does not include
// them, so a TUI state sync - which never populates Options - does not wipe them.
//
// This is where an attached TUI's mutations land, so it is also where the window
// lifecycle events for those mutations are raised: the incoming state is diffed
// against the state it replaces. See state_events.go.
func (s *Session) UpdateState(state *SessionState) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if state.Options == nil && s.state != nil {
		state.Options = s.state.Options
	}
	before := snapshotLifecycle(s.state)
	s.state = state
	s.LastActive = time.Now()
	s.emitLifecycleLocked(before)
}

// mutateState runs fn against the canonical state under the state lock and
// raises the window lifecycle events implied by whatever fn changed. Daemon-side
// (headless) window operations go through it so they emit through the same diff
// as a TUI state sync, rather than each op emitting for itself.
func (s *Session) mutateState(fn func(state *SessionState) error) error {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()

	before := snapshotLifecycle(s.state)
	if err := fn(s.state); err != nil {
		return err
	}
	s.emitLifecycleLocked(before)
	return nil
}

// emitLifecycleLocked diffs the current state against before and emits the
// resulting events. It is called with the state lock held, deliberately: holding
// it across the emit is what keeps a session's events in the same order as the
// mutations that caused them when several callers mutate concurrently. It is
// safe because the sink only stamps a sequence number and does non-blocking
// channel sends; it never re-enters the session.
func (s *Session) emitLifecycleLocked(before lifecycleSnapshot) {
	events := diffLifecycle(before, snapshotLifecycle(s.state))
	for _, ev := range events {
		s.emit(ev)
	}
}

// Stop closes all PTYs and cleans up.
func (s *Session) Stop() {
	// Stop resurrection saving
	if s.stopResurrection != nil {
		s.stopResurrection()
	}
	// Final save before stopping. Capture cwds while the shells are still alive.
	_ = SaveSessionForResurrection(s.ResurrectionState())

	s.ptysMu.Lock()
	defer s.ptysMu.Unlock()

	for id, pty := range s.ptys {
		_ = pty.Close()
		delete(s.ptys, id)
	}
}

// WindowCount returns the number of windows in state.
func (s *Session) WindowCount() int {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return len(s.state.Windows)
}

// Size returns the current session dimensions.
func (s *Session) Size() (width, height int) {
	s.sizeMu.RLock()
	defer s.sizeMu.RUnlock()
	return s.width, s.height
}

// Resize updates the session dimensions.
// This is called when the effective size changes (min of all connected clients).
func (s *Session) Resize(width, height int) {
	s.sizeMu.Lock()
	s.width = width
	s.height = height
	s.sizeMu.Unlock()

	// Resize all PTYs to match the new session size
	s.ptysMu.RLock()
	defer s.ptysMu.RUnlock()
	for _, pty := range s.ptys {
		_ = pty.Resize(width, height) // Best effort resize
	}
}

// Info returns session information.
func (s *Session) Info() SessionInfo {
	s.sizeMu.RLock()
	width, height := s.width, s.height
	s.sizeMu.RUnlock()

	return SessionInfo{
		Name:        s.Name,
		ID:          s.ID,
		Created:     s.Created.Unix(),
		LastActive:  s.LastActive.Unix(),
		WindowCount: s.WindowCount(),
		Attached:    false, // Will be set by manager
		Width:       width,
		Height:      height,
	}
}

func (s *Session) getShell() string {
	if s.config != nil && s.config.Shell != "" {
		return s.config.Shell
	}
	if shell := os.Getenv("SHELL"); shell != "" {
		return shell
	}
	if runtime.GOOS == "windows" {
		return "cmd.exe"
	}
	return "/bin/sh"
}

func (s *Session) buildEnv(windowID string, restored bool) []string {
	env := os.Environ()

	term := "xterm-256color"
	if s.config != nil && s.config.Term != "" {
		term = s.config.Term
	}
	env = append(env, "TERM="+term)

	colorTerm := "truecolor"
	if s.config != nil && s.config.ColorTerm != "" {
		colorTerm = s.config.ColorTerm
	}
	env = append(env, "COLORTERM="+colorTerm)
	env = append(env, "TERM_PROGRAM=TUIOS")
	env = append(env, "TERM_PROGRAM_VERSION=0.1.0")
	env = append(env, "TUIOS_SESSION="+s.Name)
	if windowID != "" {
		env = append(env, "TUIOS_WINDOW_ID="+windowID)
	}
	// Mark restored shells so the user's shell rc (and scripts) can react, and
	// so the restore is observable without relying on the visual banner.
	if restored {
		env = append(env, "TUIOS_RESTORED=1")
	}

	return env
}

// restoredBanner returns the dim one-line notice written to a restored shell's
// terminal emulator. cwd, when set, is included so the user sees where the
// fresh shell was spawned.
func restoredBanner(cwd string) string {
	msg := "-- tuios: session restored, fresh shell"
	if cwd != "" {
		msg += " in " + cwd
	}
	msg += " --"
	return "\x1b[2m" + msg + "\x1b[0m\r\n"
}

// PTY methods

// Subscribe adds a subscriber to receive PTY output.
func (p *PTY) Subscribe(clientID string) <-chan []byte {
	p.subscribersMu.Lock()
	defer p.subscribersMu.Unlock()

	// Return existing channel if already subscribed
	if existing, ok := p.subscribers[clientID]; ok {
		debugLog("[DEBUG] PTY %s: client %s already subscribed", p.ID[:8], clientID)
		return existing
	}

	ch := make(chan []byte, 16384) // Large buffer matching client-side outputChan capacity
	p.subscribers[clientID] = ch
	debugLog("[DEBUG] PTY %s: added subscriber %s (total: %d)", p.ID[:8], clientID, len(p.subscribers))

	// Send buffered output to catch up
	p.outputMu.RLock()
	if p.outputPos > 0 {
		debugLog("[DEBUG] PTY %s: sending %d buffered bytes to new subscriber", p.ID[:8], p.outputPos)
		bufCopy := make([]byte, p.outputPos)
		copy(bufCopy, p.outputBuffer[:p.outputPos])
		select {
		case ch <- bufCopy:
			debugLog("[DEBUG] PTY %s: buffered output sent", p.ID[:8])
		default:
			debugLog("[DEBUG] PTY %s: failed to send buffered output (channel full)", p.ID[:8])
		}
	} else {
		debugLog("[DEBUG] PTY %s: no buffered output to send", p.ID[:8])
	}
	p.outputMu.RUnlock()

	return ch
}

// Unsubscribe removes a subscriber.
func (p *PTY) Unsubscribe(clientID string) {
	p.subscribersMu.Lock()
	defer p.subscribersMu.Unlock()

	if ch, ok := p.subscribers[clientID]; ok {
		close(ch)
		delete(p.subscribers, clientID)
	}
}

// Write sends input to the PTY.
func (p *PTY) Write(data []byte) (int, error) {
	if p.pty == nil {
		return 0, fmt.Errorf("PTY not available")
	}
	return p.pty.Write(data)
}

// Size returns the current PTY dimensions.
func (p *PTY) Size() (width, height int) {
	p.terminalMu.RLock()
	defer p.terminalMu.RUnlock()
	return p.width, p.height
}

// SetCellSize sets the cell dimensions in pixels for the PTY's VT emulator.
// This enables proper XTWINOPS responses (CSI 14t, CSI 16t) for applications
// that query terminal pixel dimensions.
func (p *PTY) SetCellSize(cellWidth, cellHeight int) {
	p.terminalMu.Lock()
	defer p.terminalMu.Unlock()
	if p.terminal != nil && cellWidth > 0 && cellHeight > 0 {
		p.terminal.SetCellSize(cellWidth, cellHeight)
	}
}

// UpdatePixelDimensions sets the cell size on the VT emulator and updates the PTY's
// pixel dimensions based on the current terminal size and the given cell dimensions.
// This is a convenience method that combines SetCellSize and SetPixelSize.
func (p *PTY) UpdatePixelDimensions(cellWidth, cellHeight int) error {
	if cellWidth <= 0 || cellHeight <= 0 {
		return nil
	}
	p.SetCellSize(cellWidth, cellHeight)
	width, height := p.Size()
	return p.SetPixelSize(width, height, width*cellWidth, height*cellHeight)
}

// Resize changes the PTY and terminal emulator size.
func (p *PTY) Resize(width, height int) error {
	// Resize VT emulator
	p.terminalMu.Lock()
	if p.terminal != nil {
		p.terminal.Resize(width, height)
	}
	p.width = width
	p.height = height
	p.terminalMu.Unlock()

	// Resize PTY
	if p.pty != nil {
		return p.pty.Resize(width, height)
	}
	return nil
}

// GetTerminalState returns the current terminal screen state for restore.
// Returns the visible screen content as a 2D array of cells.
func (p *PTY) GetTerminalState() *TerminalState {
	p.terminalMu.RLock()
	defer p.terminalMu.RUnlock()

	if p.terminal == nil {
		return nil
	}

	state := &TerminalState{
		Width:         p.width,
		Height:        p.height,
		CursorX:       p.terminal.CursorPosition().X,
		CursorY:       p.terminal.CursorPosition().Y,
		ScrollbackLen: p.terminal.ScrollbackLen(),
		IsAltScreen:   p.terminal.IsAltScreen(), // Capture alt screen state for mouse event forwarding
		Modes:         p.terminal.GetModes(),    // Capture terminal modes (mouse tracking, bracketed paste, etc.)
		Screen:        make([][]CellState, p.height),
		Scrollback:    make([][]CellState, 0),
	}

	// Capture visible screen with full styling
	for y := 0; y < p.height; y++ {
		state.Screen[y] = make([]CellState, p.width)
		for x := 0; x < p.width; x++ {
			cell := p.terminal.CellAt(x, y)
			if cell != nil {
				state.Screen[y][x] = cellToState(cell)
			}
		}
	}

	// Capture scrollback (up to a reasonable limit)
	scrollbackLen := p.terminal.ScrollbackLen()
	maxScrollback := 1000 // Limit for initial sync
	if scrollbackLen > maxScrollback {
		scrollbackLen = maxScrollback
	}

	for i := 0; i < scrollbackLen; i++ {
		line := p.terminal.ScrollbackLine(i)
		if line != nil {
			row := make([]CellState, len(line))
			for x, cell := range line {
				row[x] = cellToState(&cell)
			}
			state.Scrollback = append(state.Scrollback, row)
		}
	}

	return state
}

// CaptureContent renders the PTY's current screen (and optionally its
// scrollback) to text from the daemon-side VT emulator. When ansi is true the
// output keeps SGR escape sequences; otherwise it is plain text. This lets
// capture-pane answer from daemon state with no TUI client attached, mirroring
// the client-side OS.capturePane rendering.
func (p *PTY) CaptureContent(scrollback, ansi bool) string {
	p.terminalMu.RLock()
	defer p.terminalMu.RUnlock()

	if p.terminal == nil {
		return ""
	}

	var content string
	if ansi {
		content = p.terminal.Render()
	} else {
		content = p.terminal.String()
	}

	if scrollback {
		scrollbackLen := p.terminal.ScrollbackLen()
		if scrollbackLen > 0 {
			var sb strings.Builder
			for i := 0; i < scrollbackLen; i++ {
				line := p.terminal.ScrollbackLine(i)
				if ansi {
					sb.WriteString(line.Render())
				} else {
					sb.WriteString(line.String())
				}
				sb.WriteByte('\n')
			}
			sb.WriteString(content)
			content = sb.String()
		}
	}

	return content
}

// TerminalState represents the serializable state of a terminal.
type TerminalState struct {
	Width         int           `json:"width"`
	Height        int           `json:"height"`
	CursorX       int           `json:"cursor_x"`
	CursorY       int           `json:"cursor_y"`
	ScrollbackLen int           `json:"scrollback_len"`
	IsAltScreen   bool          `json:"is_alt_screen,omitempty"` // Alternate screen buffer active (for mouse event forwarding)
	Modes         map[int]bool  `json:"modes,omitempty"`         // Terminal modes (mouse tracking, bracketed paste, etc.)
	Screen        [][]CellState `json:"screen"`
	Scrollback    [][]CellState `json:"scrollback,omitempty"`
}

// CellState represents a single terminal cell with full styling information.
type CellState struct {
	Content   string `json:"c,omitempty"`  // Cell content (character or grapheme)
	Width     int    `json:"w,omitempty"`  // Cell width (1 for normal, 2 for wide chars, 0 for continuation)
	FgColor   string `json:"fg,omitempty"` // Foreground color (hex format like "#ff0000" or empty for default)
	BgColor   string `json:"bg,omitempty"` // Background color (hex format or empty)
	Bold      bool   `json:"b,omitempty"`  // Bold attribute
	Italic    bool   `json:"i,omitempty"`  // Italic attribute
	Underline bool   `json:"u,omitempty"`  // Underline attribute
	Reverse   bool   `json:"r,omitempty"`  // Reverse video attribute
	Blink     bool   `json:"bl,omitempty"` // Blink attribute
	Faint     bool   `json:"f,omitempty"`  // Faint/dim attribute
}

// cellToState converts a VT cell to a serializable CellState.
func cellToState(cell *uv.Cell) CellState {
	if cell == nil {
		return CellState{}
	}

	cs := CellState{
		Content: cell.Content,
		Width:   cell.Width,
	}

	// Convert colors to hex strings for JSON serialization
	if cell.Style.Fg != nil {
		r, g, b, _ := cell.Style.Fg.RGBA()
		cs.FgColor = fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8)
	}
	if cell.Style.Bg != nil {
		r, g, b, _ := cell.Style.Bg.RGBA()
		cs.BgColor = fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8)
	}

	// Copy style attributes from bitmask
	// Attrs bitmask using uv.Attr* constants
	cs.Bold = cell.Style.Attrs&uv.AttrBold != 0
	cs.Faint = cell.Style.Attrs&uv.AttrFaint != 0
	cs.Italic = cell.Style.Attrs&uv.AttrItalic != 0
	cs.Reverse = cell.Style.Attrs&uv.AttrReverse != 0
	cs.Underline = cell.Style.Underline != ansi.UnderlineNone // Any underline style (single, double, curly, etc.)
	// Note: Blink not commonly used in modern terminals, omitting for now

	return cs
}

// StateToCell converts a CellState back to a VT cell for restoration.
func StateToCell(cs CellState) *uv.Cell {
	cell := &uv.Cell{
		Content: cs.Content,
		Width:   cs.Width,
	}

	// Parse color strings back to color.Color using ansi.RGBColor
	if cs.FgColor != "" {
		var r, g, b uint8
		if _, err := fmt.Sscanf(cs.FgColor, "#%02x%02x%02x", &r, &g, &b); err == nil {
			cell.Style.Fg = ansi.RGBColor{R: r, G: g, B: b}
		}
	}
	if cs.BgColor != "" {
		var r, g, b uint8
		if _, err := fmt.Sscanf(cs.BgColor, "#%02x%02x%02x", &r, &g, &b); err == nil {
			cell.Style.Bg = ansi.RGBColor{R: r, G: g, B: b}
		}
	}

	// Restore style attributes using direct field assignment
	if cs.Bold {
		cell.Style.Attrs |= uv.AttrBold
	}
	if cs.Faint {
		cell.Style.Attrs |= uv.AttrFaint
	}
	if cs.Italic {
		cell.Style.Attrs |= uv.AttrItalic
	}
	if cs.Reverse {
		cell.Style.Attrs |= uv.AttrReverse
	}
	if cs.Underline {
		cell.Style.Underline = ansi.UnderlineSingle
	}

	return cell
}

// Close terminates the PTY.
func (p *PTY) Close() error {
	p.cancel()

	// Close all subscriber channels
	p.subscribersMu.Lock()
	for id, ch := range p.subscribers {
		close(ch)
		delete(p.subscribers, id)
	}
	p.subscribersMu.Unlock()

	// Kill process
	if p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}

	// Mark the VT emulator closed so forwardTerminalResponses returns EOF on
	// its next read. (A read already blocked in the response pipe is only
	// unblocked once Emulator.Close CloseWrites the pipe.)
	if p.terminal != nil {
		_ = p.terminal.Close()
	}

	// Close PTY. This unblocks readOutput's pending Read, which then closes
	// vtWriteChan so vtWriter exits.
	if p.pty != nil {
		return p.pty.Close()
	}
	return nil
}

// ProcessCwd returns the current working directory of the PTY's shell process.
// The second return is false when it cannot be determined (process gone, or an
// unsupported platform). Used to capture cwd for session resurrection.
func (p *PTY) ProcessCwd() (string, bool) {
	if p.cmd == nil || p.cmd.Process == nil {
		return "", false
	}
	return processCwd(p.cmd.Process.Pid)
}

// IsExited returns true if the shell process has exited.
func (p *PTY) IsExited() bool {
	p.exitedMu.RLock()
	defer p.exitedMu.RUnlock()
	return p.exited
}

func (p *PTY) readOutput() {
	// readOutput is the sole sender on vtWriteChan; closing it here lets
	// vtWriter's range terminate when the read loop exits.
	defer close(p.vtWriteChan)

	buf := make([]byte, 16*1024) // 16KB: matches typical PTY pipe buffer
	for {
		select {
		case <-p.ctx.Done():
			return
		default:
		}

		n, err := p.pty.Read(buf)
		if err != nil {
			return
		}

		if n > 0 {
			data := make([]byte, n)
			copy(data, buf[:n])

			// VT emulator: feed via a dedicated single goroutine to
			// avoid unbounded goroutine growth at high FPS. The VT is
			// only used for state queries (GetTerminalState) and kitty
			// query responses, so it's OK if it falls slightly behind.
			select {
			case p.vtWriteChan <- data:
			case <-p.ctx.Done():
				return
			default:
				// VT writer can't keep up  - acceptable for state tracking.
				// The client's own VT is the rendering source of truth.
			}

			// Store in ring buffer for reconnection
			p.outputMu.Lock()
			p.appendToBuffer(data)
			p.outputMu.Unlock()

			// Broadcast to subscribers
			p.broadcast(data)

			// Raise a control-plane output-activity event. This is a lightweight
			// signal (byte count only, no content) that drives wait-for-output and
			// window-idle waits and lets a subscriber know the pane is active; the
			// raw bytes still flow only through the binary subscriber stream.
			if p.emit != nil {
				p.emit(SessionEvent{Type: EventOutput, Bytes: n})
			}
		}
	}
}

// vtWriter is a single persistent goroutine that feeds the daemon's VT
// emulator. Using a dedicated goroutine (instead of spawning one per PTY
// read) prevents unbounded goroutine growth at high FPS.
func (p *PTY) vtWriter() {
	for data := range p.vtWriteChan {
		p.terminalMu.Lock()
		if p.terminal != nil {
			_, _ = p.terminal.Write(data)
		}
		p.terminalMu.Unlock()
	}
}

func (p *PTY) appendToBuffer(data []byte) {
	bufLen := len(p.outputBuffer)
	// If data is bigger than the buffer, keep only the tail
	if len(data) >= bufLen {
		copy(p.outputBuffer, data[len(data)-bufLen:])
		p.outputPos = bufLen
		return
	}
	// Shift in half-buffer steps until there is room. A single half-shift is
	// not always enough when len(data) exceeds bufLen/2, so loop until the
	// remaining space fits or the buffer is empty.
	for bufLen-p.outputPos < len(data) && p.outputPos > 0 {
		half := min(bufLen/2, p.outputPos)
		copy(p.outputBuffer, p.outputBuffer[half:p.outputPos])
		p.outputPos -= half
	}
	// Advance by bytes actually copied so outputPos can never exceed bufLen.
	n := copy(p.outputBuffer[p.outputPos:], data)
	p.outputPos += n
}

func (p *PTY) broadcast(data []byte) {
	p.subscribersMu.RLock()
	defer p.subscribersMu.RUnlock()

	debugLog("[DEBUG] PTY %s: BROADCAST called with %d bytes, %d subscribers", p.ID[:8], len(data), len(p.subscribers))
	for clientID, ch := range p.subscribers {
		select {
		case ch <- data:
			debugLog("[DEBUG] PTY %s: sent to %s", p.ID[:8], clientID)
		default:
			debugLog("[DEBUG] PTY %s: channel full for %s, dropped", p.ID[:8], clientID)
		}
	}
}

func (p *PTY) monitorExit() {
	if p.cmd == nil {
		return
	}

	_ = p.cmd.Wait()

	p.exitedMu.Lock()
	p.exited = true
	if p.cmd.ProcessState != nil {
		p.exitCode = p.cmd.ProcessState.ExitCode()
	}
	p.exitedMu.Unlock()

	debugLog("[DEBUG] PTY %s: process exited with code %d", p.ID[:8], p.exitCode)

	// Notify callback (used by daemon to inform clients)
	if p.onExit != nil {
		p.onExit(p.ID)
	}

	// Raise a control-plane window-exit event so wait-for window-exit resolves.
	if p.emit != nil {
		p.emit(SessionEvent{Type: EventWindowExit})
	}
}

// forwardTerminalResponses reads responses from the daemon's terminal emulator and
// forwards them to the PTY as input for applications to receive.
// The emulator writes responses (like DA1, CPR) to its pipe. If nothing reads from the pipe,
// Write() will block forever (io.Pipe is synchronous).
// Client emulators DRAIN their responses to prevent duplicates.
func (p *PTY) forwardTerminalResponses() {
	if p.terminal == nil {
		return
	}

	buf := make([]byte, 4096)
	for {
		select {
		case <-p.ctx.Done():
			return
		default:
			n, err := p.terminal.Read(buf)
			if err != nil {
				return
			}
			if n > 0 && p.pty != nil {
				// Forward response to PTY as input
				_, _ = p.pty.Write(buf[:n])
			}
		}
	}
}
