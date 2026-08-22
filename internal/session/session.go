package session

import (
	"context"
	"fmt"
	"image/color"

	"log"
	"maps"
	"os"
	"os/exec"
	"reflect"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	xpty "github.com/charmbracelet/x/xpty"
	"github.com/google/uuid"

	"github.com/Gaurav-Gosain/tuios/internal/guestenv"
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
	// Unplaced marks a window the daemon created whose X/Y/Width/Height are a
	// nominal box rather than a position anyone chose. The daemon has no viewport
	// and cannot place a window; a client that receives an unplaced window puts it
	// where it would have put one of its own and clears the flag on its next sync.
	//
	// The default is the safe one: a state without the field (an older client, or
	// resurrection state written before this existed) reads as placed, which is
	// exactly the pre-existing behavior of trusting the geometry as sent.
	Unplaced bool `json:"unplaced,omitempty"`
	// AgentState is the semantic state of an agent running in this pane
	// (working, needs_input, idle, done, errored). It is daemon-owned: a pane
	// reports it through the set-agent-state verb and clients never set it, so it
	// is retained across a client sync the same way Cwd and Options are. The zero
	// value (empty) is AgentStateNone and is omitted from serialized state, so
	// older state and older clients read back as "no agent", the pre-existing
	// behavior.
	AgentState AgentState `json:"agent_state,omitempty"`
	// AgentMessage is an optional short note the pane reported alongside its
	// state, e.g. what it is waiting for. Daemon-owned, like AgentState.
	AgentMessage string `json:"agent_message,omitempty"`
	// AgentStateAt is the unix-nano time AgentState was last set. It is stamped
	// daemon-side and drives the output-stall heuristic (see applyStallHeuristic).
	AgentStateAt int64 `json:"agent_state_at,omitempty"`
	// AgentHarness is the harness id the reporting source named, empty when
	// nothing named one (the foreground detector never does). It is synced
	// because a client-side alert has to be able to say which harness stopped;
	// the ranked source that won stays daemon-side, where get-agent-state reads
	// it from the claim.
	AgentHarness string `json:"agent_harness,omitempty"`
	// ForegroundCmd is the base name of the program running in the pane's
	// foreground, empty while the pane sits at its login shell. It is what lets a
	// row say "nvim" instead of repeating a title every pane in one directory
	// shares. Daemon-owned and refreshed by the agent detector's existing poll,
	// so it costs no extra process reads and may be one poll out of date, which
	// is fine for a label.
	ForegroundCmd string `json:"foreground_cmd,omitempty"`
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
	Name string `json:"name"`
	// DisplayName is an optional user-facing label. Name stays the identity: it
	// keys the session map, names the resurrection file, is exported as
	// TUIOS_SESSION into every pane, and is what every verb's session parameter
	// resolves against. Renaming for display must not disturb any of that, which
	// is why the label is a field of its own rather than a write to Name.
	//
	// Empty means unnamed, and every reader falls back to Name, so a session that
	// was never renamed reads exactly as it did before this field existed.
	DisplayName string `json:"display_name,omitempty"`
	// Accent is an optional accent for the session, recorded verbatim the way
	// Options are: the daemon has no palette and does not interpret it. Clients
	// read it as a colour name from the ANSI sixteen or as a hex literal, and an
	// empty or unreadable value means they pick the session's colour themselves.
	// It lives here rather than in a client-side file because it has to survive a
	// reattach and be the same for every client attached to this session.
	Accent string `json:"accent,omitempty"`
	// Restored marks a session the daemon rebuilt from saved state and that no
	// client has attached to since. Nothing else at session level says so: the
	// layout is back but every shell under it is new, and the only evidence was
	// a per-pane banner that scrolls away. The daemon sets it on restore and
	// clears it on the first attach, so it answers "why is this session here"
	// for exactly as long as the question is unanswered.
	//
	// Daemon-owned: clients never send it, and false is what every older client
	// and every pre-existing state file reads back as.
	Restored         bool           `json:"restored,omitempty"`
	Windows          []WindowState  `json:"windows"`
	FocusedWindowID  string         `json:"focused_window_id,omitempty"`
	CurrentWorkspace int            `json:"current_workspace"`
	WorkspaceFocus   map[int]string `json:"workspace_focus,omitempty"` // workspace -> focused window ID
	MasterRatio      float64        `json:"master_ratio"`
	AutoTiling       bool           `json:"auto_tiling"`
	Width            int            `json:"width"`
	Height           int            `json:"height"`
	// Input mode (window-management vs terminal) is deliberately absent: it is
	// per-viewer, not per-session. It used to live here, which meant one client
	// entering terminal mode flipped the input mode of every other client
	// attached to the same session. Clients own their own mode.
	//
	// BSP tiling state

	// WorkspaceNames maps a workspace number to its optional label. The number
	// stays the workspace's identity and its fallback label: everything that
	// addresses a workspace (the window's Workspace field, WorkspaceFocus,
	// WorkspaceTrees, the verbs, TUIOS_WORKSPACE) keeps using the number, and a
	// workspace with no entry here is unnamed and renders as its number, exactly
	// as every workspace did before this existed. Naming one is a daemon-owned
	// change so it survives a reattach and every attached client sees it.
	WorkspaceNames map[int]string `json:"workspace_names,omitempty"`
	// WorkspaceOrder is the order the workspaces are shown in, and only that.
	// The number stays the identity, so the window's Workspace field,
	// WorkspaceFocus, WorkspaceTrees, the verbs and TUIOS_WORKSPACE all keep
	// addressing by number and none of them moves when this does. It sits beside
	// the names because it is the same kind of thing: a presentation choice the
	// daemon owns so it survives a reattach and every attached client sees the
	// one arrangement. Absent means the plain ascending order, which is what
	// every session had before this existed.
	WorkspaceOrder  []int                      `json:"workspace_order,omitempty"`
	WorkspaceTrees  map[int]*SerializedBSPTree `json:"workspace_trees,omitempty"`  // BSP tree per workspace
	WindowToBSPID   map[string]int             `json:"window_to_bsp_id,omitempty"` // Window UUID -> BSP int ID
	NextBSPWindowID int                        `json:"next_bsp_window_id,omitempty"`
	TilingScheme    int                        `json:"tiling_scheme,omitempty"` // Default auto-insertion scheme
	// LayoutMode is which tiling layout the session uses: "bsp", "master-stack"
	// or "scrolling". It sits beside the BSP topology it selects between, which
	// was already carried here; without it a scrolling session came back as a BSP
	// one on reattach, because the topology survived and the mode that reads it
	// did not.
	//
	// Empty means unstated, and a client that receives it leaves its own mode
	// alone. That is what makes the field additive: state written before it
	// existed, and clients that never send it, behave exactly as they did.
	LayoutMode string `json:"layout_mode,omitempty"`
	// NumWorkspaces is how many workspaces this session has. The daemon-side
	// operations bound workspace indices by it; it used to be a constant 9
	// duplicated here to keep this package free of a config import, which meant
	// the daemon disagreed with a client configured for any other number.
	// Zero means unstated, and the bound falls back to defaultWorkspaces.
	NumWorkspaces int `json:"num_workspaces,omitempty"`
	// ResurrectionVersion tags the on-disk state schema. It is stamped by
	// SaveSessionForResurrection (not by clients) and checked on load so that
	// state written by a newer, incompatible tuios is archived rather than
	// misinterpreted. Absent (0) means pre-versioning state, which is a
	// structural subset of the current schema and loads fine.
	ResurrectionVersion int `json:"resurrection_version,omitempty"`
	// Version counts the daemon-side mutations of this state: every headless
	// window operation bumps it, a client sync does not. It is the daemon's
	// answer to "how much have I changed since you last looked", and it is
	// stamped on every state the daemon hands out.
	Version int `json:"version,omitempty"`
	// BaseVersion is the Version a client last saw, echoed back on the state it
	// pushes. It says which daemon state the client's snapshot was built from, so
	// the daemon can tell a current sync from one that predates its own
	// mutations. Zero means a client that predates state versioning; its syncs
	// are taken at face value, as they were before.
	BaseVersion int `json:"base_version,omitempty"`
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
	terminal vt.Terminal
	// terminalMu guards the daemon-side VT emulator (p.terminal) and the
	// p.width/p.height it is sized to. This is the daemon process; it is a
	// different lock from app.OS.terminalMu and the two never coexist.
	//
	//   LOCK ORDER (within the daemon):
	//       PTY.terminalMu  ->  (nothing)
	//
	//   It is a leaf lock. No holder may take p.outputMu, send on a channel,
	//   or touch p.pty while holding it. Resize deliberately drops it before
	//   calling p.pty.Resize, and vtWriter drops it between queue items, for
	//   exactly that reason: p.pty operations are syscalls that can block, and
	//   the subscriber/capture paths take the read side constantly.
	//
	//   NOT REENTRANT. Size, SetCellSize, Resize, GetTerminalState,
	//   CaptureContent and vtWriter must not call one another
	//   (UpdatePixelDimensions calls SetCellSize and Size in sequence, not
	//   nested, which is why it is written that way).
	terminalMu sync.RWMutex
	width      int
	height     int

	// Output buffer for reconnection (ring buffer) - legacy, kept for raw output.
	// outputSeq is the total number of bytes this PTY has ever produced, so the
	// buffer holds the stream's last outputPos bytes ending at outputSeq. It is
	// what lets a resubscribing client be resumed where it left off instead of
	// being handed the buffer from the top.
	outputMu     sync.RWMutex
	outputBuffer []byte
	outputPos    int
	outputSeq    int64
	// resizeMarks are the stream positions resizes took effect at, oldest
	// first, kept while the ring still holds bytes either side of them plus
	// the newest one behind the ring, which is the width the ring's first
	// byte was laid out at.
	resizeMarks []resizeMark

	// Subscribers for raw output streaming.
	subscribers   map[string]*ptySubscriber
	subscribersMu sync.RWMutex

	// debug mirrors TUIOS_DEBUG_INTERNAL, read once when the PTY is built.
	// broadcast runs per chunk per subscriber, and a debugLog there costs an
	// os.Getenv (which takes the process-wide environment lock) plus a boxed
	// argument slice on every call, whether or not the flag is set. The env var
	// is set from the --debug flag before any PTY exists, so a value read here
	// is the value the run was started with.
	debug bool

	exited   bool
	exitedMu sync.RWMutex
	exitCode int

	// Single-goroutine VT writer channel. Closed by readOutput on exit so
	// vtWriter's range terminates.
	vtWriteChan chan vtChunk

	// streamMu puts a resize at one position in the pane's stream. readOutput
	// holds it across appending a chunk, broadcasting it and queueing it for
	// the emulator, so a resize taken under it lands between the same two
	// bytes on the daemon's emulator and on every subscriber's. Without that
	// the two sides change width at different bytes and disagree for good
	// about where the line in between wrapped.
	streamMu sync.Mutex
	// vtClosed records that readOutput has closed vtWriteChan, so a resize
	// arriving during teardown does not send on a closed channel. Guarded by
	// streamMu, which readOutput also holds to close.
	vtClosed bool

	// vtSeq is the stream position the emulator has consumed, guarded by
	// terminalMu. It trails outputSeq by whatever is still queued.
	vtSeq int64

	// Callback when PTY process exits - used by daemon to notify clients
	onExit func(ptyID string)

	// emit, when set, raises a control-plane event (output activity, bell, mode
	// change, process exit) already tagged with this PTY's window and PTY ID. It
	// is a no-op when the session has no event sink installed.
	emit func(SessionEvent)

	// lastOutput is the unix-nano time this PTY most recently produced output. It
	// is written on the PTY read goroutine and read by the daemon's output-stall
	// heuristic, so it is an atomic rather than guarded by a lock.
	lastOutput atomic.Int64

	// lastAgentProbe is the unix-nano time of the last output-driven agent-exit
	// probe. It throttles the /proc read the probe does so a busy pane does not
	// re-check its foreground on every output chunk.
	lastAgentProbe atomic.Int64

	// lastScreenScan is the unix-nano time of the last output-driven screen scan,
	// throttling it the same way.
	lastScreenScan atomic.Int64

	// screenSettle is the one-shot that scans the screen after a pane goes quiet.
	// It is a timer rather than a ticker so a silent pane costs nothing, which is
	// the rule the whole daemon is built to.
	//
	// The timer is built once and re-armed with Reset. Arming happens on every
	// chunk a pane emits, and a fresh time.AfterFunc there allocated a runtime
	// timer per chunk. screenLook is what it runs, held separately so the caller
	// does not have to build a closure per chunk either.
	screenSettleMu sync.Mutex
	screenSettle   *time.Timer
	screenLook     func()

	// agentProgress parks the most recent OSC 9;4 progress state the emulator
	// saw, as the state plus one so zero means none pending. The VT callback runs
	// on the vtWriter goroutine with the terminal lock held, where mutating
	// session state would re-enter that lock, so it only stores here and the PTY
	// read goroutine applies it on the output event that carried the sequence.
	agentProgress atomic.Int64

	// title is the last title this PTY's application set. The daemon reads every
	// byte of every window, so this is the freshest title anyone holds: a client
	// only sees the windows it is subscribed to, and its copy of the title stops
	// where its subscription did.
	title atomic.Pointer[string]
}

// Title returns the last title this PTY's application set, or "" if it has set
// none.
func (p *PTY) Title() string {
	if t := p.title.Load(); t != nil {
		return *t
	}
	return ""
}

// agentExitProbeInterval bounds how often output drives an agent-exit probe, so a
// pane streaming heavy output probes /proc at most a few times a second while a
// quit agent still clears well inside one detection poll.
const agentExitProbeInterval = 250 * time.Millisecond

// LastOutput returns the unix-nano time this PTY most recently produced output,
// or 0 if it has produced none. It backs the daemon's agent-state stall
// heuristic, which demotes a pane that reported working but has gone quiet.
func (p *PTY) LastOutput() int64 {
	return p.lastOutput.Load()
}

// probeAgentExitDue reports whether enough time has passed since the last
// output-driven agent-exit probe to run another, and claims the slot if so. It is
// only ever called from the single PTY read goroutine, so a plain load/store is
// race-free.
func (p *PTY) probeAgentExitDue(now int64) bool {
	if now-p.lastAgentProbe.Load() < int64(agentExitProbeInterval) {
		return false
	}
	p.lastAgentProbe.Store(now)
	return true
}

// storeAgentProgress parks an OSC 9;4 progress state for the read goroutine to
// apply. Called from the VT callback under the terminal lock, so it must stay a
// single atomic store and nothing more.
func (p *PTY) storeAgentProgress(state vt.ProgressState) {
	p.agentProgress.Store(int64(state) + 1)
}

// takeAgentProgress returns the parked OSC 9;4 progress state and clears it,
// reporting whether one was pending. A burst that parked several states between
// two output events collapses to the newest, which is the only one still true.
func (p *PTY) takeAgentProgress() (vt.ProgressState, bool) {
	v := p.agentProgress.Swap(0)
	if v == 0 {
		return 0, false
	}
	return vt.ProgressState(v - 1), true
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

	// stateDirty is set by every change to the session's structure and consumed
	// by the resurrection saver, which is how a new window reaches disk in a
	// couple of seconds instead of at the next blind tick. It is an atomic rather
	// than a field of state because the saver goroutine reads it and holds no
	// lock of this session.
	stateDirty atomic.Bool

	// eventSink, when set, receives control-plane events raised by this session
	// and its PTYs (window lifecycle, output activity, bell, mode changes). The
	// daemon installs it so events reach the event hub; nil for a session with no
	// hub (e.g. bare unit tests).
	eventSink   func(SessionEvent)
	eventSinkMu sync.RWMutex

	// stateSink, when set, receives the canonical state after every daemon-side
	// mutation, so an attached client can be told what the daemon just did. The
	// daemon installs it and broadcasts the snapshot to the session's clients;
	// nil for a session with no clients (bare unit tests, resurrection loads).
	stateSink   func(*SessionState)
	stateSinkMu sync.RWMutex

	// pushMu serializes state-sink deliveries and pushedVersion records the
	// highest version already delivered. Snapshots are taken under stateMu but
	// delivered without it, so two concurrent mutations can reach the sink in
	// either order; this drops the loser rather than letting a client see the
	// daemon go backwards.
	pushMu        sync.Mutex
	pushedVersion int

	// Terminal size
	width  int
	height int
	sizeMu sync.RWMutex

	// Lifecycle
	Created    time.Time
	LastActive time.Time

	// Configuration
	config *SessionConfig

	// agentClaims records, by window ID, who owns the window's agent state: the
	// ranked source that last set it (see AgentSource) and whether the
	// foreground-process detector promoted the window and so must clear it when
	// the agent exits.
	//
	// It used to be a bool holding only the second half. That was enough while the
	// detector was the only thing competing with an explicit report; it cannot
	// express which of several sources should win, so the value carries the source
	// now. Read and written under stateMu, so it needs no lock of its own.
	agentClaims map[string]agentClaim

	// transcripts binds windows to the record files their harnesses write. It is
	// held here rather than in SessionState because none of it is state: a
	// transcript path names a project directory and a session, so it is kept in
	// daemon memory and never serialised, never versioned, and never pushed to a
	// client. Only the AgentState derived from it is.
	transcripts agentTranscriptState

	// agentHolds records, by window ID, a quieter agent state waiting out the
	// anti-flicker window before it is published (see holdQuieterState). It has a
	// lock of its own rather than riding stateMu because it is read and written
	// around ApplyAgentReport, which takes stateMu itself.
	agentHolds map[string]agentHold
	// agentHoldTimer is the one-shot that publishes a hold whose source then
	// went silent. Nil when nothing is waiting. Guarded by agentHoldMu.
	agentHoldTimer *time.Timer
	agentHoldMu    sync.Mutex

	// Graphics capabilities of the attached client's host terminal. The daemon
	// records them on attach so shells spawned afterwards can advertise a
	// terminal identity the guest's image tools recognise.
	graphicsMu    sync.RWMutex
	kittyGraphics bool
	sixelGraphics bool
}

// SetGraphicsCapabilities records the graphics protocols tuios can forward to
// the attached client's host terminal. It is called on every attach, so the
// most recent client wins; PTYs already running keep the environment they were
// started with.
func (s *Session) SetGraphicsCapabilities(kitty, sixel bool) {
	s.graphicsMu.Lock()
	defer s.graphicsMu.Unlock()
	s.kittyGraphics = kitty
	s.sixelGraphics = sixel
}

// GraphicsCapabilities returns the recorded kitty and sixel support.
func (s *Session) GraphicsCapabilities() (kitty, sixel bool) {
	s.graphicsMu.RLock()
	defer s.graphicsMu.RUnlock()
	return s.kittyGraphics, s.sixelGraphics
}

// SessionConfig holds configuration for a session.
type SessionConfig struct {
	Term      string
	ColorTerm string
	Shell     string
	// SocketPath is the daemon socket a shell spawned in this session reports to.
	// The manager stamps it from the daemon's own socket when a session is
	// created, so it is exported into every pane's environment as TUIOS_SOCKET.
	SocketPath string
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
			// Versions start at 1 so that a BaseVersion of 0 on an incoming sync
			// means one thing only: a client that predates state versioning and
			// cannot say what it saw. A versioned client always echoes back at
			// least 1, even before the daemon has mutated anything.
			Version: 1,
		},
		width:      width,
		height:     height,
		Created:    now,
		LastActive: now,
		config:     cfg,
	}

	// Start periodic resurrection saving
	session.stopResurrection = StartPeriodicSave(
		func() *SessionState { return session.ResurrectionState() },
		func() bool { return session.stateDirty.Swap(false) },
	)

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

// SetStateSink installs the sink that receives the canonical state after every
// daemon-side mutation. It is safe to call concurrently and may be set after the
// session already holds windows.
func (s *Session) SetStateSink(fn func(*SessionState)) {
	s.stateSinkMu.Lock()
	s.stateSink = fn
	s.stateSinkMu.Unlock()
}

// publishState hands a post-mutation snapshot to the state sink, in version
// order and never twice for the same version. It must be called without stateMu
// held: the sink writes to client sockets, and a slow client must be able to
// delay other pushes without also blocking every mutation of the session.
func (s *Session) publishState(snap *SessionState) {
	if snap == nil {
		return
	}
	s.stateSinkMu.RLock()
	fn := s.stateSink
	s.stateSinkMu.RUnlock()
	if fn == nil {
		return
	}

	s.pushMu.Lock()
	defer s.pushMu.Unlock()
	if snap.Version <= s.pushedVersion {
		return
	}
	s.pushedVersion = snap.Version
	fn(snap)
}

// CreatePTY creates a new PTY in this session. windowID, if non-empty, is the
// client-side window UUID exported to the shell as TUIOS_WINDOW_ID. onExit, if
// non-nil, is invoked with the PTY ID when the process exits; it is set before
// the monitor goroutine starts so it is always visible to monitorExit.
func (s *Session) CreatePTY(windowID string, width, height int, onExit func(ptyID string)) (*PTY, error) {
	return s.createPTY(windowID, width, height, "", nil, false, onExit)
}

// RestorePTY creates a fresh PTY for a resurrected window. It behaves like
// CreatePTY but starts the shell in cwd (when that directory still exists) and
// marks the shell as restored: the shell's environment carries TUIOS_RESTORED=1
// and a one-line banner is written to the terminal so the user can see the
// process is a freshly respawned shell, not the original long-lived one.
func (s *Session) RestorePTY(windowID string, width, height int, cwd string, onExit func(ptyID string)) (*PTY, error) {
	return s.createPTY(windowID, width, height, cwd, nil, true, onExit)
}

// command, when non-empty, is an argv exec'd as the PTY's process in place of
// the shell. It is deliberately not persisted: a restored window respawns as a
// shell, because silently rerunning a program the user ran once is not what
// restoration promises.
func (s *Session) createPTY(windowID string, width, height int, cwd string, command []string, restored bool, onExit func(ptyID string)) (*PTY, error) {
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
	var cmd *exec.Cmd
	if len(command) > 0 {
		cmd = exec.Command(command[0], command[1:]...)
	} else {
		cmd = exec.Command(shell)
	}
	cmd.Env = s.buildEnv(windowID, restored)
	// Start the shell in cwd when one was named and still exists; otherwise fall
	// back to the shell's default (inherited) directory.
	//
	// The restored flag used to gate this too, which meant a caller that asked a
	// fresh window for a directory was answered with a shell somewhere else and
	// no indication of it. Restoration and placement are separate questions: the
	// flag still decides the banner, because that is what it is about.
	if cwd != "" {
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
		// Name the process that failed: with a command this is the program the
		// user picked, and "shell" would send them looking at the wrong config.
		return nil, fmt.Errorf("failed to start %s: %w", cmd.Path, err)
	}

	// Create VT emulator for persistent terminal state
	// This maintains scrollback, screen content, cursor position across reconnects
	terminal := vt.New(width, height)
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
		subscribers:  make(map[string]*ptySubscriber),
		vtWriteChan:  make(chan vtChunk, 256),
		onExit:       onExit,
		debug:        debugEnabled(),
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
		Bell: func() { pty.emit(SessionEvent{Type: EventBell}) },
		Title: func(title string) {
			pty.title.Store(&title)
			pty.emit(SessionEvent{Type: EventWindowRetitled, Title: title})
		},
		AltScreen: func(on bool) {
			pty.emit(SessionEvent{Type: EventModeChanged, Mode: "alt-screen", Enabled: on})
		},
		// Parked rather than applied: this fires with the terminal lock held, and
		// applying it mutates session state. The read goroutine picks it up on the
		// output event carrying these same bytes.
		Progress: func(state vt.ProgressState, _ int) {
			pty.storeAgentProgress(state)
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

// hasLivePTY reports whether a PTY with this ID is still open on the session.
// A window whose PTY is gone has been closed; see reconcileStale.
func (s *Session) hasLivePTY(id string) bool {
	s.ptysMu.RLock()
	defer s.ptysMu.RUnlock()
	_, ok := s.ptys[id]
	return ok
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
	state := s.snapshotStateLocked()
	s.stateMu.RUnlock()

	// Retitle from the live emulators. The stored title is only as fresh as the
	// last sync from a client, which knows the title of the windows it is
	// subscribed to and nothing about the rest, so a reattach or a resurrection
	// snapshot built from the stored value alone brings back a title the window
	// stopped having. Taken outside stateMu: the PTY map is a different lock.
	live := s.liveTitles()
	for i := range state.Windows {
		if t := live[state.Windows[i].PTYID]; t != "" {
			state.Windows[i].Title = t
		}
	}
	return state
}

// liveTitles maps PTY ID to the title that PTY's application last set, for every
// PTY that has set one.
func (s *Session) liveTitles() map[string]string {
	s.ptysMu.RLock()
	defer s.ptysMu.RUnlock()

	titles := make(map[string]string, len(s.ptys))
	for id, pty := range s.ptys {
		if t := pty.Title(); t != "" {
			titles[id] = t
		}
	}
	return titles
}

// snapshotStateLocked returns a copy of the canonical state. The caller must
// hold stateMu (for read or write).
func (s *Session) snapshotStateLocked() *SessionState {
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
	if s.state.WorkspaceNames != nil {
		stateCopy.WorkspaceNames = make(map[int]string, len(s.state.WorkspaceNames))
		maps.Copy(stateCopy.WorkspaceNames, s.state.WorkspaceNames)
	}
	if s.state.WorkspaceOrder != nil {
		stateCopy.WorkspaceOrder = slices.Clone(s.state.WorkspaceOrder)
	}
	return &stateCopy
}

// SetDisplayName records the session's optional display label. It runs through
// mutateState, so the new label reaches every attached client on the same push
// every other daemon-side mutation uses, and the periodic resurrection save
// picks it up with the rest of the state. An empty name clears the label; the
// session's identity (Name) is untouched either way.
func (s *Session) SetDisplayName(name string) error {
	return s.mutateState(func(st *SessionState) error {
		st.DisplayName = name
		return nil
	})
}

// SetAccent records the session's optional accent slot, propagated and persisted
// exactly as SetDisplayName is. The value is opaque to the daemon: it is the
// client's palette that knows what a slot name means.
func (s *Session) SetAccent(accent string) error {
	return s.mutateState(func(st *SessionState) error {
		st.Accent = accent
		return nil
	})
}

// MarkRestored records that this session was rebuilt from saved state. It runs
// through mutateState rather than being written into the state the restore
// pushes, because a client sync always takes the daemon's value for this field
// and would otherwise wipe it right back off.
func (s *Session) MarkRestored() {
	_ = s.mutateState(func(st *SessionState) error {
		st.Restored = true
		return nil
	})
}

// ClearRestored drops the restored mark now that a client is looking at the
// session. It is a no-op on a session that was not restored.
//
// Deliberately not published to the session's clients. This runs inside the
// attach handler, which has already recorded the connection's session, so a
// state push here reaches the attaching client on the same socket ahead of the
// attach reply it is blocked waiting for, and that client fails the attach with
// "unexpected response". Nothing needs the push: every surface that shows the
// mark for a session it is not attached to reads it from the session listing,
// which is polled.
func (s *Session) ClearRestored() {
	s.stateMu.RLock()
	restored := s.state.Restored
	s.stateMu.RUnlock()
	if !restored {
		return
	}
	_, _ = s.mutateStateLocked(func(st *SessionState) error {
		st.Restored = false
		return nil
	})
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
	s.stateDirty.Store(true)
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

// OptionKeys returns every option key set on this session, sorted. It backs the
// available-keys hint on a get-option miss, so a caller that guessed a key wrong
// learns which keys exist without a second round trip.
func (s *Session) OptionKeys() []string {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	keys := make([]string, 0, len(s.state.Options))
	for k := range s.state.Options {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
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

// UpdateState converges a client's view of the session onto the daemon's state.
//
// The daemon owns this state, so a client sync does not simply replace it. The
// incoming snapshot carries the daemon Version the client last saw. When that
// version is current the client has seen everything the daemon did and its
// snapshot is taken as sent. When it is behind, the client built its snapshot
// before a daemon-side mutation it has never seen, and the fields the daemon
// owns are restored on top of it rather than being silently undone. Fields no
// client ever sets (Options, Cwd, ResurrectionVersion) are carried over either
// way.
//
// It reports whether the state was applied as the client sent it. False means
// the result differs from what was pushed, and the caller is expected to send
// the merged state back so that client converges instead of pushing the same
// stale view again.
//
// This is where an attached TUI's mutations land, so it is also where the window
// lifecycle events for those mutations are raised: the state that ends up
// canonical is diffed against the state it replaces. See state_events.go.
func (s *Session) UpdateState(state *SessionState) bool {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()

	accepted := true
	prev := s.state
	if prev != nil {
		if state.BaseVersion != 0 && state.BaseVersion < prev.Version {
			reconcileStale(state, prev, s.hasLivePTY)
			accepted = false
		}
		retainDaemonExclusive(state, prev)
		// Version counts daemon-side mutations only, so converging on a client
		// snapshot carries it forward unchanged: the client is not telling the
		// daemon anything the daemon did not already know.
		state.Version = prev.Version
	}
	state.BaseVersion = 0

	before := snapshotLifecycle(prev)
	s.state = state
	s.LastActive = time.Now()
	s.stateDirty.Store(true)
	s.emitLifecycleLocked(before)
	return accepted
}

// mutateState runs fn against the canonical state under the state lock, raises
// the window lifecycle events implied by whatever fn changed, and hands the
// resulting state to the state sink. Daemon-side (headless) window operations go
// through it so they emit through the same diff as a TUI state sync, rather than
// each op emitting for itself.
//
// The sink call is what makes an attached client a subscriber to the daemon
// rather than the daemon's only writer: every mutation the daemon makes itself
// reaches the client's renderer through this one place, so a new daemon-side
// operation is live in the TUI without a line of routing code of its own.
func (s *Session) mutateState(fn func(state *SessionState) error) error {
	snap, err := s.mutateStateLocked(fn)
	if err != nil {
		return err
	}
	// Deliberately outside the state lock: the sink writes to client sockets.
	s.publishState(snap)
	return nil
}

// mutateStateLocked is mutateState's critical section. It returns the snapshot
// to publish once the lock is released.
func (s *Session) mutateStateLocked(fn func(state *SessionState) error) (*SessionState, error) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()

	before := snapshotLifecycle(s.state)
	if err := fn(s.state); err != nil {
		return nil, err
	}
	// A daemon-side mutation is exactly what a client sync must not undo, so it
	// is what advances the version. A client that pushes a snapshot built before
	// this point is reconciled by UpdateState rather than winning by arriving
	// last.
	s.state.Version++
	s.stateDirty.Store(true)
	s.emitLifecycleLocked(before)
	return s.snapshotStateLocked(), nil
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
	// This is the last chance to persist the session, so a failure here is the
	// difference between it coming back and not; it is reported rather than
	// dropped even though Stop cannot act on it.
	if err := SaveSessionForResurrection(s.ResurrectionState()); err != nil {
		LogError("Final resurrection save for session %q failed, it will not come back: %v", s.Name, err)
	}

	// Before the panes go, so a hold cannot publish a state against a session
	// that has already saved and stopped.
	s.stopAgentHoldTimer()

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

// Resize records the session dimensions when the effective size changes (min
// of all connected clients).
//
// It records and nothing more. A pane's winsize belongs to the client layout,
// which announces each pane's own content size over ResizePTY. Resizing every
// PTY to the session's outer box here handed each guest the whole viewport's
// dimensions; the retile that followed re-announced only panes whose tile size
// changed, so a pane whose geometry survived the resize kept a winsize wider
// than the pane and its shell drew prompts the pane's emulator had to wrap.
func (s *Session) Resize(width, height int) {
	s.sizeMu.Lock()
	s.width = width
	s.height = height
	s.sizeMu.Unlock()
}

// Info returns session information.
func (s *Session) Info() SessionInfo {
	s.sizeMu.RLock()
	width, height := s.width, s.height
	s.sizeMu.RUnlock()

	windows := s.windowSummaries()

	s.stateMu.RLock()
	displayName, accent := s.state.DisplayName, s.state.Accent
	currentWorkspace := s.state.CurrentWorkspace
	restored := s.state.Restored
	s.stateMu.RUnlock()

	return SessionInfo{
		Name:             s.Name,
		ID:               s.ID,
		Created:          s.Created.Unix(),
		LastActive:       s.LastActive.Unix(),
		WindowCount:      len(windows),
		Attached:         false, // Will be set by manager
		Width:            width,
		Height:           height,
		Windows:          windows,
		DisplayName:      displayName,
		Accent:           accent,
		CurrentWorkspace: currentWorkspace,
		Restored:         restored,
	}
}

// windowSummaries builds the lightweight per-window listing for Info() from the
// cached window states, titled from the live emulators. It does no PTY work, so
// it stays cheap enough to run on every session list.
func (s *Session) windowSummaries() []WindowSummary {
	live := s.liveTitles()

	s.stateMu.RLock()
	defer s.stateMu.RUnlock()

	if len(s.state.Windows) == 0 {
		return nil
	}
	out := make([]WindowSummary, 0, len(s.state.Windows))
	for i := range s.state.Windows {
		w := &s.state.Windows[i]
		title := w.CustomName
		if title == "" {
			title = live[w.PTYID]
		}
		if title == "" {
			title = w.Title
		}
		if title == "" {
			title = "shell"
		}
		// A named pane has nothing to gain from a command name, and offering one
		// would let it outrank the name the user chose.
		fg := w.ForegroundCmd
		if w.CustomName != "" {
			fg = ""
		}
		out = append(out, WindowSummary{
			ID:            w.ID,
			Title:         title,
			AgentState:    string(w.AgentState),
			AgentStateAt:  w.AgentStateAt,
			AgentHarness:  w.AgentHarness,
			AgentMessage:  w.AgentMessage,
			ForegroundCmd: fg,
			Workspace:     w.Workspace,
		})
	}
	return out
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
	kitty, sixel := s.GraphicsCapabilities()
	env = append(env, "TERM_PROGRAM="+guestenv.TermProgram(kitty, sixel))
	env = append(env, "TERM_PROGRAM_VERSION=0.1.0")
	env = append(env, "TUIOS_SESSION="+s.Name)
	if windowID != "" {
		env = append(env, "TUIOS_WINDOW_ID="+windowID)
		// TUIOS_PANE_ID is an alias a state-reporting shim guards on, mirroring
		// the pane-id contract other multiplexers' agent integrations use.
		env = append(env, "TUIOS_PANE_ID="+windowID)
	}
	// TUIOS_ENV marks a process as running under tuios, and TUIOS_SOCKET tells a
	// shim which daemon socket to report to. Together with the pane id above they
	// are the whole contract the agent-state shim needs; a shim finds nothing to
	// report to when they are unset and no-ops.
	env = append(env, "TUIOS_ENV=1")
	if s.config != nil && s.config.SocketPath != "" {
		env = append(env, "TUIOS_SOCKET="+s.config.SocketPath)
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

// ptySubscriber is one client's output stream. sent is the stream position of
// the last chunk this subscriber was handed, which is where the client is
// resumed if it comes back.
type ptySubscriber struct {
	ch   chan ptyChunk
	sent atomic.Int64
}

// ptyChunk is one item on a subscriber's stream: output bytes, or the size the
// daemon's emulator took at exactly this point. A resize carries no bytes and
// so does not move the stream position.
type ptyChunk struct {
	data          []byte
	width, height int // both > 0 marks a resize rather than output
}

// resizeMark records the stream position a resize took effect at. The ring
// holds bytes only, so without the marks a subscriber resumed across a resize
// was handed the whole span at one width and laid out at that width lines the
// daemon had wrapped at another. Guarded by outputMu and pruned with the ring.
type resizeMark struct {
	seq           int64
	width, height int
}

func (c ptyChunk) isResize() bool { return c.width > 0 && c.height > 0 }

// resyncPrefix homes the cursor and clears the screen and the scrollback. It
// goes in front of a catch-up the client cannot splice onto what it already
// holds.
var resyncPrefix = []byte("\x1b[H\x1b[2J\x1b[3J")

// Subscribe adds a subscriber to receive PTY output, resuming it at fromSeq: the
// stream position a previous subscription for this client reached, as returned
// by Unsubscribe. Zero means the client has seen nothing of this PTY and gets
// the whole catch-up buffer.
//
// Resuming is what makes hiding and showing a pane free. A client subscribes
// again every time the pane's workspace becomes current, and it already holds
// the pane's screen; replaying the buffer from the top painted the pane's whole
// history a second time below the paint already there, which is the stacked
// prompts a workspace switch used to leave behind.
func (p *PTY) Subscribe(clientID string, fromSeq int64) <-chan ptyChunk {
	p.subscribersMu.Lock()
	defer p.subscribersMu.Unlock()

	// Return existing channel if already subscribed
	if existing, ok := p.subscribers[clientID]; ok {
		debugLog("[DEBUG] PTY %s: client %s already subscribed", p.ID[:8], clientID)
		return existing.ch
	}

	sub := &ptySubscriber{ch: make(chan ptyChunk, 16384)} // Large buffer matching client-side outputChan capacity
	p.subscribers[clientID] = sub
	debugLog("[DEBUG] PTY %s: added subscriber %s (total: %d)", p.ID[:8], clientID, len(p.subscribers))

	// Send whatever the client has not seen to catch it up.
	p.outputMu.RLock()
	// A client that fell further behind than the buffer reaches cannot be
	// resumed exactly, so it gets everything still held rather than a gap.
	bufStart := p.outputSeq - int64(p.outputPos)
	start := 0
	rolled := fromSeq > 0 && fromSeq < bufStart
	if fromSeq > bufStart {
		start = min(int(fromSeq-bufStart), p.outputPos)
	}
	if n := p.outputPos - start; n > 0 {
		debugLog("[DEBUG] PTY %s: sending %d buffered bytes to new subscriber", p.ID[:8], n)
		send := func(c ptyChunk) {
			select {
			case sub.ch <- c:
			default:
				debugLog("[DEBUG] PTY %s: failed to send catch-up chunk (channel full)", p.ID[:8])
			}
		}
		// The replay is cut at every resize mark inside it and the resize sent
		// between the two segments, so the client lays each segment out at the
		// width the daemon laid it out at. One flat replay put the whole span
		// at one width, and a catch-up that crossed a resize came back with
		// every line after it wrapped where the daemon had not wrapped it.
		startSeq := bufStart + int64(start)
		// The width the replay begins at. A client resumed here is usually at
		// it already and skips it; a rolled client, whose screen the resync
		// below clears, is not.
		for i := len(p.resizeMarks) - 1; i >= 0; i-- {
			if m := p.resizeMarks[i]; m.seq <= startSeq {
				send(ptyChunk{width: m.width, height: m.height})
				break
			}
		}
		var prefix []byte
		if rolled {
			// The client still holds the screen it drew up to fromSeq, and the
			// bytes between there and the buffer's start are gone. Appending the
			// tail to that screen splices two halves of the stream that never
			// met: the missing bytes carried the cursor moves and the modes the
			// tail is written against, so the guest's output lands wherever the
			// old screen had left off. Clear first, so the tail repaints from a
			// known state instead of over a stale one.
			prefix = resyncPrefix
		}
		segStart := start
		cut := func(end int) {
			if end == segStart && prefix == nil {
				return
			}
			seg := make([]byte, 0, len(prefix)+end-segStart)
			seg = append(seg, prefix...)
			prefix = nil
			seg = append(seg, p.outputBuffer[segStart:end]...)
			send(ptyChunk{data: seg})
			segStart = end
		}
		for _, m := range p.resizeMarks {
			if m.seq <= startSeq {
				continue
			}
			cut(int(m.seq - bufStart))
			send(ptyChunk{width: m.width, height: m.height})
		}
		cut(p.outputPos)
	} else {
		debugLog("[DEBUG] PTY %s: no buffered output to send", p.ID[:8])
	}
	sub.sent.Store(p.outputSeq)
	p.outputMu.RUnlock()

	// The size the emulator is at now, behind the catch-up. A resize is only
	// broadcast to the subscribers of the moment, so one that landed between a
	// client's snapshot and its subscribe reaches nobody; this states the
	// answer on every subscribe instead of leaving the pane at whatever width
	// the client last heard about. It is a no-op whenever nothing was missed.
	p.terminalMu.RLock()
	if p.terminal != nil {
		w, h := p.terminal.Width(), p.terminal.Height()
		select {
		case sub.ch <- ptyChunk{width: w, height: h}:
		default:
		}
	}
	p.terminalMu.RUnlock()

	return sub.ch
}

// Unsubscribe removes a subscriber and returns the stream position it reached,
// to hand back to Subscribe when the client returns.
func (p *PTY) Unsubscribe(clientID string) int64 {
	p.subscribersMu.Lock()
	defer p.subscribersMu.Unlock()

	sub, ok := p.subscribers[clientID]
	if !ok {
		return 0
	}
	// Closing lets the streaming goroutine drain what is still queued, so every
	// chunk broadcast up to here does reach the client.
	close(sub.ch)
	delete(p.subscribers, clientID)
	return sub.sent.Load()
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
//
// The emulator is not resized here. A resize is a point in the pane's output
// stream, not an event outside it: the guest has already produced bytes this
// pane has not laid out yet, and which width they are laid out at decides where
// they wrap. Queueing the resize behind them puts it at one byte, tells every
// subscriber the same byte, and leaves the daemon and its clients agreeing on
// the line at the seam. Resizing the emulator here instead let a client that
// had already resized itself lay out everything produced between asking and
// being heard one width narrower than the daemon did, and a line that wrapped
// differently is in the scrollback for good.
func (p *PTY) Resize(width, height int) error {
	// The pane's size is what a client announced for it, so it is recorded as
	// soon as it is announced. Only the emulator's grid waits for the stream.
	p.terminalMu.Lock()
	p.width, p.height = width, height
	p.terminalMu.Unlock()

	p.streamMu.Lock()
	if !p.vtClosed {
		// Recorded against the ring before it is broadcast, so a catch-up cut
		// from the ring later replays it between the same two bytes every
		// subscriber of this moment saw it between.
		p.outputMu.Lock()
		p.resizeMarks = append(p.resizeMarks, resizeMark{seq: p.outputSeq, width: width, height: height})
		p.outputMu.Unlock()
		p.broadcast(ptyChunk{width: width, height: height}, 0)
		select {
		case p.vtWriteChan <- vtChunk{width: width, height: height}:
		case <-p.ctx.Done():
		}
	}
	p.streamMu.Unlock()

	// The real PTY is resized now regardless, so the guest gets its SIGWINCH
	// without waiting for the emulator to catch up with the backlog.
	if p.pty != nil {
		return p.pty.Resize(width, height)
	}
	return nil
}

// DefaultStateScrollback is how many scrollback lines a state request carries
// when it does not ask for a number.
const DefaultStateScrollback = 1000

// GetTerminalState returns the current terminal screen state for restore.
// Returns the visible screen content as a 2D array of cells.
//
// maxScrollback bounds the scrollback rows included: zero means
// DefaultStateScrollback and a negative number means none. The rows returned
// are the newest ones. Taking them from the front instead, which is what this
// did, handed a pane with a long history its most ancient screenfuls and
// dropped everything the user had actually been looking at.
//
// have is how many rows the caller's own emulator already holds, and bounds the
// reply the same way maxScrollback does: only the rows past it can be used, so
// only those are sent. ScrollbackLen in the reply is always the true length, so
// the caller's own arithmetic against it is unaffected by either bound.
func (p *PTY) GetTerminalState(maxScrollback, have int) *TerminalState {
	p.terminalMu.RLock()
	defer p.terminalMu.RUnlock()

	if p.terminal == nil {
		return nil
	}

	// The emulator's own size, not the pane's announced one. They differ only
	// while a resize is still behind output in the stream, and a snapshot has
	// to describe the grid it is serializing: reporting the size the pane is
	// about to be handed one row of cells short.
	state := TerminalStateOf(p.terminal, p.terminal.Width(), p.terminal.Height(), maxScrollback, have)
	state.Seq = p.vtSeq
	return state
}

// TerminalStateOf serializes everything a client needs to arrive at the picture
// this emulator holds. It is one half of the wire contract; ApplyTerminalState
// is the other, and the two are kept in this file so a field added to one is
// read by the other. docs/REHYDRATION.md states the contract.
//
// Width and height are the pane's, which the caller knows; the emulator's own
// size lags a resize the shell has not acknowledged yet.
//
// have is how many scrollback rows the receiving emulator already holds; only
// the rows past it are serialized, because only those can be used. See
// GetTerminalState.
func TerminalStateOf(t vt.Terminal, width, height, maxScrollback, have int) *TerminalState {
	state := &TerminalState{
		Width:         width,
		Height:        height,
		CursorX:       t.CursorPosition().X,
		CursorY:       t.CursorPosition().Y,
		ScrollbackLen: t.ScrollbackLen(),
		IsAltScreen:   t.IsAltScreen(),        // Capture alt screen state for mouse event forwarding
		Modes:         t.GetModes(),           // Capture terminal modes (mouse tracking, bracketed paste, etc.)
		KittyKbdStack: t.KittyKeyboardStack(), // Capture kitty keyboard protocol flag stack
		Screen:        make([][]CellState, height),
		Scrollback:    make([][]CellState, 0),
	}

	// None of these is recoverable from the cells. They are what the guest set
	// and has not reset, and they decide how the output that has not arrived
	// yet is painted, where it lands, and which glyphs it draws.
	pen, link := t.CursorPen()
	ps := styleToWire(pen, link)
	state.Pen = &ps

	// Carried only when the guest set one. A region that is simply the whole
	// screen says nothing, and sending it pins a client that has been resized
	// since to whatever size this pane was when the snapshot was taken.
	if m := t.ScrollRegion(); m != t.Bounds() {
		state.Margins = []int{m.Min.X, m.Min.Y, m.Dx(), m.Dy()}
	}

	ids, gl, gr := t.Charsets()
	state.Charsets = []int{int(ids[0]), int(ids[1]), int(ids[2]), int(ids[3]), gl, gr}

	// The cursor shape is set once, by a shell's prompt or by an editor
	// changing mode, and is long out of the output buffer's reach by the time
	// anyone reattaches. Without it here a pane that asked for a bar comes back
	// as a block, because that is what a fresh emulator is.
	state.CursorShape = decscusrParam(t.CursorStyle())

	// Capture visible screen with full styling
	for y := range height {
		state.Screen[y] = make([]CellState, width)
		for x := range width {
			cell := t.CellAt(x, y)
			if cell != nil {
				state.Screen[y][x] = CellStateOf(cell)
			}
		}
	}

	if state.IsAltScreen {
		state.MainScreen = make([][]CellState, height)
		for y := range height {
			state.MainScreen[y] = make([]CellState, width)
			for x := range width {
				if cell := t.MainCellAt(x, y); cell != nil {
					state.MainScreen[y][x] = CellStateOf(cell)
				}
			}
		}
	}

	if maxScrollback == 0 {
		maxScrollback = DefaultStateScrollback
	}
	scrollbackLen := t.ScrollbackLen()
	// Rows the caller already holds are rows it will discard on arrival: it
	// keeps its own history and merges only what scrolled off while it was
	// away. Sending them anyway is what made a workspace switch move megabytes
	// per pane to be thrown away at the far end. state.ScrollbackLen below is
	// still the true length, which is what the caller subtracts against.
	want := scrollbackLen - have
	if want < 0 {
		want = 0
	}
	if maxScrollback >= 0 && want < maxScrollback {
		maxScrollback = want
	}
	first := 0
	if maxScrollback < 0 {
		first = scrollbackLen
	} else if scrollbackLen > maxScrollback {
		first = scrollbackLen - maxScrollback
	}

	for i := first; i < scrollbackLen; i++ {
		line := t.ScrollbackLine(i)
		if line != nil {
			row := make([]CellState, len(line))
			for x, cell := range line {
				row[x] = CellStateOf(&cell)
			}
			state.Scrollback = append(state.Scrollback, row)
		}
	}

	return state
}

// ApplyTerminalState brings an emulator to the state a snapshot describes. It
// is the reading half of the wire contract TerminalStateOf writes, and it is
// the whole of what rehydration does to an emulator: the caller owns the
// locking, the window-level flags and the stream that resumes afterwards.
//
// The emulator may be fresh or may be one that survived a workspace switch and
// already holds most of this, so every step is written to be idempotent.
func ApplyTerminalState(t vt.Terminal, state *TerminalState) {
	if t == nil || state == nil {
		return
	}

	// A snapshot too big for the emulator it is going into used to be taken
	// silently, because writing a cell outside the buffer is a no-op: every row
	// past the client's own height was dropped and the pane came back with its
	// bottom blank. An editor came back without the last line of the file or
	// its status line, which is the shape this was reported in, and it stayed
	// that way: the alternate screen keeps no scrollback to recover those rows
	// from, and the guest does not redraw a size it was never told changed.
	//
	// Grown to fit and never shrunk. How much room a pane has is the client's
	// layout to decide and it resizes this emulator on the next pass either
	// way, so growing is transient; dropping the content is not.
	if state.Width > t.Width() || state.Height > t.Height() {
		t.Resize(max(state.Width, t.Width()), max(state.Height, t.Height()))
	}

	// Sending ESC[?1049h instead would clear the buffer it is switching to.
	//
	// Applied in both directions. Only entering was applied, so an emulator
	// that survived a workspace switch and whose guest had quit its full-screen
	// program while the pane was hidden stayed pointed at the alternate buffer,
	// and the shell's screen was blitted into the wrong one.
	t.RestoreAltScreenMode(state.IsAltScreen)

	// Modes come after the screen switch so the map lands on top of it. They
	// are what apps like vim and htop need to receive mouse events at all, and
	// the guest set them once, long out of the output buffer's reach.
	if len(state.Modes) > 0 {
		t.RestoreModes(state.Modes)
	}

	// Kitty keyboard flags travel outside the DEC mode map and are set once by
	// the guest (CSI > u / CSI = u), so like the modes above they cannot be
	// recovered from the bounded output buffer. Without this a reattached
	// client encodes keys in legacy form for a pane that negotiated the
	// protocol.
	t.RestoreKittyKeyboardState(state.KittyKbdStack)

	// The rendition the guest left in force, which paints everything that
	// arrives after this snapshot. Without it the stream resuming on top of a
	// restore was written in whatever colour this emulator happened to be left
	// in: default on a pane rebuilt from nothing, and stale on one that
	// survived. That is corruption on new output rather than on restored
	// content, which is why it looked random.
	if state.Pen != nil {
		t.RestoreCursorPen(styleFromWire(t, *state.Pen))
	}
	if len(state.Margins) == 4 {
		t.RestoreScrollRegion(uv.Rect(state.Margins[0], state.Margins[1], state.Margins[2], state.Margins[3]))
	} else {
		// No margins on the wire means the guest set none, so this emulator
		// scrolls its whole screen. Left alone, a pane that had margins before
		// the route would keep them after it.
		t.ResetScrollRegion()
	}
	if len(state.Charsets) == 6 {
		ids := [4]byte{
			byte(state.Charsets[0]), byte(state.Charsets[1]),
			byte(state.Charsets[2]), byte(state.Charsets[3]),
		}
		t.RestoreCharsets(ids, state.Charsets[4], state.Charsets[5])
	}
	if state.CursorShape > 0 {
		t.RestoreCursorStyle(decscusrStyle(state.CursorShape))
	}

	// The scrollback goes back first, and it is the main screen's either way:
	// the alternate screen keeps none, and both sides read the same buffer.
	// Seeding it is what makes a pane's history survive a route that builds it
	// on a new emulator.
	//
	// A pane whose emulator survived keeps the history it already holds and is
	// only handed the lines that scrolled off while it was away. The daemon
	// sends a bounded window of its scrollback and a client keeps far more than
	// that, so replacing the whole buffer would cut a long history down to the
	// size of the window on every workspace switch.
	if have := t.ScrollbackLen(); have == 0 {
		for _, row := range state.Scrollback {
			t.PushScrollbackLine(stateToLine(t, row))
		}
	} else if missing := state.ScrollbackLen - have; missing > 0 {
		rows := state.Scrollback
		if missing < len(rows) {
			rows = rows[len(rows)-missing:]
		}
		for _, row := range rows {
			t.PushScrollbackLine(stateToLine(t, row))
		}
	}

	// The alternate screen is restored the same way as the normal one. It used
	// to be skipped, on the grounds that a resize would make vim or htop
	// repaint itself, which asks the guest to do the client's job: a program
	// that does not redraw on SIGWINCH, or one that is between frames, leaves
	// the pane blank.
	if len(state.Screen) > 0 {
		for y := 0; y < len(state.Screen) && y < state.Height; y++ {
			if state.Screen[y] == nil {
				continue
			}
			for x := 0; x < len(state.Screen[y]) && x < state.Width; x++ {
				cellState := state.Screen[y][x]
				// A wide rune's continuation column is empty and is written by
				// SetCell from the lead cell's width, so skipping it is right.
				if cellState.Content == "" {
					continue
				}
				t.SetCell(x, y, stateToCell(t, cellState))
			}
		}
		// The cursor was serialized and thrown away. Whatever came next was
		// written from wherever this client's emulator happened to be left,
		// which on a pane rebuilt from nothing is the top left corner.
		t.RestoreCursorPosition(state.CursorX, state.CursorY)
	}

	// The shell's screen under a running full-screen program. Quitting the
	// program reveals it, and the client had never been sent it: a pane where
	// vim was open across a switch came back correct and went blank the moment
	// vim exited, because the buffer underneath had nothing in it.
	for y := 0; y < len(state.MainScreen) && y < state.Height; y++ {
		for x := 0; x < len(state.MainScreen[y]) && x < state.Width; x++ {
			cs := state.MainScreen[y][x]
			if cs.Content == "" {
				continue
			}
			t.SetMainCell(x, y, stateToCell(t, cs))
		}
	}
}

// stateToLine converts one serialized scrollback row to a line for t.
func stateToLine(t vt.Terminal, row []CellState) uv.Line {
	line := make(uv.Line, len(row))
	for x, cs := range row {
		line[x] = *stateToCell(t, cs)
	}
	return line
}

// CaptureContent renders the PTY's current screen (and optionally its
// scrollback) to text from the daemon-side VT emulator. When ansi is true the
// output keeps SGR escape sequences; otherwise it is plain text.
//
// This is how capture-pane is answered, attached or not. It used to be answered
// here only when nothing was attached and routed to the client otherwise, which
// made the result of a read depend on whether someone happened to be watching.
// The client's OS.capturePane is the same rendering of a VT emulator fed by the
// same PTY, so there was nothing the round trip could add; it survives only as
// the local scrollback browser's own reader.
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
			for i := range scrollbackLen {
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

// decscusrParam encodes a cursor shape as the DECSCUSR parameter a guest would
// have sent to ask for it, which is how it travels. Never zero: zero on the
// wire means the snapshot did not carry a shape.
func decscusrParam(style vt.CursorStyle, steady bool) int {
	n := int(style)*2 + 1
	if steady {
		n++
	}
	return n
}

// decscusrStyle is the inverse, for a parameter a snapshot actually carried.
func decscusrStyle(n int) (vt.CursorStyle, bool) {
	return vt.CursorStyle((n - 1) / 2), n%2 == 0
}

// TerminalState represents the serializable state of a terminal.
type TerminalState struct {
	// Seq is the stream position this snapshot was taken at: the emulator here
	// has consumed exactly the first Seq bytes the pane ever produced. A client
	// restoring this state subscribes from Seq, so it receives what came after
	// the snapshot and not what the snapshot already shows.
	Seq           int64       `json:"seq,omitempty"`
	Width         int         `json:"width"`
	Height        int         `json:"height"`
	CursorX       int         `json:"cursor_x"`
	CursorY       int         `json:"cursor_y"`
	ScrollbackLen int         `json:"scrollback_len"`
	IsAltScreen   bool        `json:"is_alt_screen,omitempty"` // Alternate screen buffer active (for mouse event forwarding)
	Pen           *StyleState `json:"pen,omitempty"`           // Graphic rendition in force: what the guest's next output is painted with
	// Margins is the scroll region, as x, y, width, height. A guest sets it
	// once to hold a header or a status line out of the scrolling part of the
	// screen.
	Margins []int `json:"margins,omitempty"`
	// Charsets names the character set selected into each of G0 to G3 by its
	// designator byte, followed by the GL and GR slots. A program that draws
	// boxes selects the DEC line-drawing set once and then sends the box
	// characters as plain letters.
	Charsets []int `json:"charsets,omitempty"`
	// CursorShape is the shape the guest asked for, as the DECSCUSR parameter
	// that asks for it: 1 blinking block, 2 steady block, and so on up to 6 for
	// a steady bar. The guest's own spelling rather than a pair of fields
	// because zero then means "this snapshot does not say", which is what a
	// client restoring from an older daemon gets, and it leaves the pane on the
	// default instead of forcing a blinking block onto it.
	CursorShape   int           `json:"cursor_shape,omitempty"`
	Modes         map[int]bool  `json:"modes,omitempty"`           // Terminal modes (mouse tracking, bracketed paste, etc.)
	KittyKbdStack []int         `json:"kitty_kbd_stack,omitempty"` // Kitty keyboard protocol flag stack, base entry first
	Screen        [][]CellState `json:"screen"`
	Scrollback    [][]CellState `json:"scrollback,omitempty"`
	// MainScreen is the normal screen, carried only while the alternate one is
	// active. It is the shell's screen underneath a full-screen program, which
	// quitting that program puts back on display. The alternate screen needs no
	// such treatment: entering it clears it, so what it held before is never
	// seen again.
	MainScreen [][]CellState `json:"main_screen,omitempty"`
}

// CellState represents a single terminal cell with full styling information.
//
// The attributes travel as the emulator's own bitmask rather than as a bool per
// attribute. Spelling them out one at a time is what left blink, conceal and
// strikethrough off the wire entirely, and collapsed the five underline styles
// into one.
type CellState struct {
	Content string `json:"c,omitempty"` // Cell content (character or grapheme)
	Width   int    `json:"w,omitempty"` // Cell width (1 for normal, 2 for wide chars, 0 for continuation)
	StyleState
}

// StyleState is a graphic rendition on the wire: how something is painted,
// separate from what it holds. It describes both a cell and the pen, which are
// the same rendition seen at two moments.
type StyleState struct {
	FgColor    string `json:"fg,omitempty"` // Foreground color, encoded by colorToWire
	BgColor    string `json:"bg,omitempty"` // Background color
	UlColor    string `json:"uc,omitempty"` // Underline color (SGR 58)
	Attrs      uint8  `json:"a,omitempty"`  // uv.Attr* bitmask: bold, faint, italic, blink, reverse, conceal, strikethrough
	Underline  uint8  `json:"u,omitempty"`  // ansi.Underline style: none, single, double, curly, dotted, dashed
	LinkURL    string `json:"l,omitempty"`  // OSC 8 hyperlink target
	LinkParams string `json:"lp,omitempty"` // OSC 8 hyperlink parameters
}

// styleToWire encodes a graphic rendition and the hyperlink that travels with it.
func styleToWire(s uv.Style, link uv.Link) StyleState {
	return StyleState{
		FgColor:    colorToWire(s.Fg),
		BgColor:    colorToWire(s.Bg),
		UlColor:    colorToWire(s.UnderlineColor),
		Attrs:      s.Attrs,
		Underline:  uint8(s.Underline),
		LinkURL:    link.URL,
		LinkParams: link.Params,
	}
}

// styleFromWire is styleToWire read back into the emulator that will hold it.
func styleFromWire(t vt.Terminal, ss StyleState) (uv.Style, uv.Link) {
	return uv.Style{
			Fg:             colorFromWire(t, ss.FgColor),
			Bg:             colorFromWire(t, ss.BgColor),
			UnderlineColor: colorFromWire(t, ss.UlColor),
			Underline:      ansi.Underline(ss.Underline),
			Attrs:          ss.Attrs,
		}, uv.Link{
			URL:    ss.LinkURL,
			Params: ss.LinkParams,
		}
}

// colorToWire encodes a cell color so the client gets back the kind of color the
// guest asked for, not merely the shade it resolves to.
//
// A palette entry follows the user's terminal theme and the RGB it happens to
// resolve to does not. Flattening every color to hex meant a pane came back
// repainted in whichever shades the default palette gives: `31m` red became a
// fixed maroon, and a theme's own red was gone. That is the whole of the
// "colours randomly changing after a switch" report, and it was invisible to a
// comparison that read both sides through RGBA().
func colorToWire(c color.Color) string {
	switch v := c.(type) {
	case nil:
		return ""
	case ansi.BasicColor:
		return "a" + strconv.Itoa(int(v))
	case ansi.IndexedColor:
		return "i" + strconv.Itoa(int(v))
	}
	// A color.Color interface can hold a typed-nil pointer such as
	// (*color.RGBA)(nil). The case nil above matches only an untyped nil, and
	// RGBA has a value receiver, so reading it through a wrapped nil panics the
	// daemon. Encode it as the absence of color, the same as an untyped nil.
	if rv := reflect.ValueOf(c); rv.Kind() == reflect.Pointer && rv.IsNil() {
		return ""
	}
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8)
}

// colorFromWire is colorToWire read back. Palette entries are resolved through
// the emulator that will hold them, so a restored cell is colored by the same
// rule as a cell the guest writes live into that emulator.
func colorFromWire(t vt.Terminal, s string) color.Color {
	if s == "" {
		return nil
	}
	if s[0] == '#' {
		var r, g, b uint8
		if _, err := fmt.Sscanf(s, "#%02x%02x%02x", &r, &g, &b); err != nil {
			return nil
		}
		return color.RGBA{R: r, G: g, B: b, A: 0xff}
	}
	n, err := strconv.Atoi(s[1:])
	if err != nil {
		return nil
	}
	switch s[0] {
	case 'a':
		return t.PaletteColor(n)
	case 'i':
		return t.IndexedColor(n)
	}
	return nil
}

// CellStateOf converts a VT cell to a serializable CellState.
func CellStateOf(cell *uv.Cell) CellState {
	if cell == nil {
		return CellState{}
	}

	return CellState{
		Content:    cell.Content,
		Width:      cell.Width,
		StyleState: styleToWire(cell.Style, cell.Link),
	}
}

// stateToCell converts a CellState back to a VT cell for restoration into t.
func stateToCell(t vt.Terminal, cs CellState) *uv.Cell {
	style, link := styleFromWire(t, cs.StyleState)
	return &uv.Cell{Content: cs.Content, Width: cs.Width, Style: style, Link: link}
}

// Close terminates the PTY.
func (p *PTY) Close() error {
	p.cancel()

	// Before anything else, so a settle scan already armed cannot fire against a
	// pane whose emulator is about to be closed.
	p.stopScreenSettle()

	// Close all subscriber channels
	p.subscribersMu.Lock()
	for id, sub := range p.subscribers {
		close(sub.ch)
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

// ShellPID returns the process id of the PTY's shell child, or 0 when the process
// is not running. It is the anchor the agent auto-detector uses to resolve the
// pane's foreground process group.
func (p *PTY) ShellPID() int {
	if p.cmd == nil || p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

// IsExited returns true if the shell process has exited.
func (p *PTY) IsExited() bool {
	p.exitedMu.RLock()
	defer p.exitedMu.RUnlock()
	return p.exited
}

func (p *PTY) readOutput() {
	// Closing vtWriteChan lets vtWriter's range terminate when the read loop
	// exits. Resize sends on it too, so the close is taken under the lock it
	// sends under and leaves the flag that stops it trying.
	defer func() {
		p.streamMu.Lock()
		p.vtClosed = true
		close(p.vtWriteChan)
		p.streamMu.Unlock()
	}()

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

			// Held across all three steps so a resize taken under the same
			// lock cannot land between two of them: the daemon's emulator and
			// every subscriber must change width at the same byte.
			p.streamMu.Lock()

			// Store in ring buffer for reconnection
			p.outputMu.Lock()
			seq := p.appendToBuffer(data)
			p.outputMu.Unlock()

			// Broadcast to subscribers
			p.broadcast(ptyChunk{data: data}, seq)

			// VT emulator: feed via a dedicated single goroutine to
			// avoid unbounded goroutine growth at high FPS.
			//
			// This send blocks rather than dropping. Falling behind is fine;
			// skipping a chunk is not. Every route a client takes into a pane
			// rehydrates it from this emulator, so a chunk dropped here is
			// output that no client will ever see again: the screen it is
			// handed is missing the cursor moves and modes the rest of the
			// stream is written against. Blocking applies backpressure to the
			// shell, which is what a terminal does when a program outputs
			// faster than the terminal can take it. vtWriter only ever holds
			// the leaf terminal lock and never waits on this loop, so there is
			// nothing here to deadlock against.
			select {
			case p.vtWriteChan <- vtChunk{data: data, seq: seq}:
			case <-p.ctx.Done():
				p.streamMu.Unlock()
				return
			}
			p.streamMu.Unlock()

			// Record the activity time for the agent-state stall heuristic before
			// anything that can block, so a demotion decision is made against when
			// output actually arrived.
			p.lastOutput.Store(time.Now().UnixNano())

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

// vtChunk is one chunk of PTY output and the stream position it ends at, or a
// resize to apply between the chunks either side of it.
type vtChunk struct {
	data          []byte
	seq           int64
	width, height int // both > 0 marks a resize rather than output
}

// vtWriter is a single persistent goroutine that feeds the daemon's VT
// emulator. Using a dedicated goroutine (instead of spawning one per PTY
// read) prevents unbounded goroutine growth at high FPS.
func (p *PTY) vtWriter() {
	for chunk := range p.vtWriteChan {
		if chunk.width > 0 && chunk.height > 0 {
			// The pane's size and its emulator move together, so a snapshot
			// can never report a width the grid it serializes is not at.
			//
			// Skipped at the size the emulator already has, exactly as the
			// client skips it (applyStreamResize): a same-size resize resets
			// the scroll region and the tab stops, and one side doing that
			// while the other declines is a divergence the guest never asked
			// for.
			p.terminalMu.Lock()
			if p.terminal != nil && (p.terminal.Width() != chunk.width || p.terminal.Height() != chunk.height) {
				p.terminal.Resize(chunk.width, chunk.height)
			}
			p.terminalMu.Unlock()
			continue
		}
		p.terminalMu.Lock()
		if p.terminal != nil {
			_, _ = p.terminal.Write(chunk.data)
		}
		// Recorded under the same lock the emulator is written and read under,
		// so a state snapshot and the position it was taken at can never
		// disagree. That pairing is what lets a client be resumed exactly where
		// the snapshot it was handed ends.
		p.vtSeq = chunk.seq
		p.terminalMu.Unlock()
	}
}

// appendToBuffer records a chunk in the catch-up buffer and returns the stream
// position it ends at.
func (p *PTY) appendToBuffer(data []byte) int64 {
	p.outputSeq += int64(len(data))
	// Marks the ring has rolled past stop being split points, but the newest
	// of them is still the width the ring's first byte was laid out at, so a
	// catch-up can start a rolled client at it.
	bufStart := p.outputSeq - int64(len(p.outputBuffer))
	for len(p.resizeMarks) > 1 && p.resizeMarks[1].seq <= bufStart {
		p.resizeMarks = p.resizeMarks[1:]
	}
	bufLen := len(p.outputBuffer)
	// If data is bigger than the buffer, keep only the tail
	if len(data) >= bufLen {
		copy(p.outputBuffer, data[len(data)-bufLen:])
		p.outputPos = bufLen
		return p.outputSeq
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
	return p.outputSeq
}

// broadcast hands a chunk ending at stream position seq to every subscriber.
func (p *PTY) broadcast(chunk ptyChunk, seq int64) {
	p.subscribersMu.RLock()
	defer p.subscribersMu.RUnlock()

	if p.debug {
		debugLog("[DEBUG] PTY %s: BROADCAST called with %d bytes, %d subscribers", p.ID[:8], len(chunk.data), len(p.subscribers))
	}
	for clientID, sub := range p.subscribers {
		// A chunk appended between a subscriber's catch-up being copied and this
		// broadcast running is in both, because Subscribe blocks the broadcast
		// rather than the append. Delivering it again paints it twice at the
		// seam, which is one duplicated line every time a pane is shown while it
		// is producing.
		//
		// A resize carries no bytes, so there is no position for it to be
		// behind and nothing to skip it against: it goes to every subscriber.
		if !chunk.isResize() && sub.sent.Load() >= seq {
			continue
		}
		select {
		case sub.ch <- chunk:
			// Only a chunk that was taken counts as reached: a client dropped
			// here resumes from the gap rather than past it.
			sub.sent.Store(seq)
			if p.debug {
				debugLog("[DEBUG] PTY %s: sent to %s", p.ID[:8], clientID)
			}
		default:
			if p.debug {
				debugLog("[DEBUG] PTY %s: channel full for %s, dropped", p.ID[:8], clientID)
			}
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
