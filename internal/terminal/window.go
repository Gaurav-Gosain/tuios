// Package terminal provides terminal window management and PTY abstraction.
package terminal

import (
	"context"
	"fmt"
	"image/color"
	"log"
	"os"
	"os/exec"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"tuios.dev/vt/dnd"

	uv "github.com/charmbracelet/ultraviolet"

	"charm.land/lipgloss/v2"
	xpty "github.com/charmbracelet/x/xpty"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/debuglog"
	"github.com/Gaurav-Gosain/tuios/internal/guestenv"
	"github.com/Gaurav-Gosain/tuios/internal/ptyspawn"
	"github.com/Gaurav-Gosain/tuios/internal/scrollclear"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/shellinteg"
	"github.com/Gaurav-Gosain/tuios/internal/textedit"
	"tuios.dev/vt"
)

// LockIO takes the exclusive lock for PTY writes, which mutate the cell buffer.
func (w *Window) LockIO() { w.ioMu.Lock() }

// UnlockIO releases the lock LockIO took.
func (w *Window) UnlockIO() { w.ioMu.Unlock() }

// RLockIO takes the shared lock for rendering, which reads the cell buffer.
func (w *Window) RLockIO() { w.ioMu.RLock() }

// RUnlockIO releases the lock RLockIO took.
func (w *Window) RUnlockIO() { w.ioMu.RUnlock() }

// TryRLockIO takes the read side only if it is free, and reports whether it
// did. The compositor uses it so that a pane which is mid-VT-write cannot hold
// up the frame: a pane under a heavy burst has the exclusive lock taken and
// retaken continuously, and a blocking RLockIO there stalls the whole screen,
// including the pane the user is typing into. A caller that fails to acquire
// must fall back to the pane's last rendered frame and leave it dirty, so the
// pane repaints as soon as the lock is free. Dropping an intermediate frame
// from a pane producing thousands of lines a second loses nothing a user could
// have read; stalling the frame that carries their keystroke does.
func (w *Window) TryRLockIO() bool { return w.ioMu.TryRLock() }

// SetTiled updates the tiled flag and re-syncs the emulator/PTY size. Resize
// deducts border cells based on Tiled (0 when tiled/borderless, 2 when
// bordered), so flipping the flag without a resize leaves the terminal one
// border off in each axis. Callers that toggle tiling (shared-borders changes,
// tiling enable/disable) must go through here. No-op when unchanged.
func (w *Window) SetTiled(tiled bool) {
	if w.Tiled == tiled {
		return
	}
	w.Tiled = tiled
	w.Resize(w.Width, w.Height)
	w.InvalidateCache()
}

// ProcessExited reports whether the window's process has exited.
func (w *Window) ProcessExited() bool { return w.processExited.Load() }

// SetProcessExited records whether the window's process has exited.
func (w *Window) SetProcessExited(exited bool) { w.processExited.Store(exited) }

// Title returns the current window title.
func (w *Window) Title() string {
	if p := w.title.Load(); p != nil {
		return *p
	}
	return ""
}

// SetTitle records the current window title.
func (w *Window) SetTitle(t string) { w.title.Store(&t) }

// IconName returns the name the window's program gave with OSC 1, the label
// of an unnamed workspace while the window is its active one. OSC 0 does not
// set it. Only a standalone window records it: a daemon window's comes with
// the daemon's state (app.OS.paneIconName).
func (w *Window) IconName() string {
	if p := w.iconName.Load(); p != nil {
		return *p
	}
	return ""
}

// SetIconName records the name OSC 1 gave.
func (w *Window) SetIconName(n string) { w.iconName.Store(&n) }

// IsAltScreen reports whether the application is using the alternate screen buffer.
func (w *Window) IsAltScreen() bool { return w.isAltScreen.Load() }

// SetAltScreen records whether the application is using the alternate screen buffer.
func (w *Window) SetAltScreen(v bool) { w.isAltScreen.Store(v) }

var (
	localTermType  string
	localColorTerm string
	localEnvOnce   sync.Once
)

// Window represents a terminal window with its own shell process.
// Each window maintains its own virtual terminal, PTY, and rendering cache.
// Scrollback buffer support is provided by the vendored vt library.
type Window struct {
	modesOnly modesFilter

	title         atomic.Pointer[string]
	iconName      atomic.Pointer[string]
	geomSnap      atomic.Pointer[GeometrySnapshot]
	CustomName    string // User-defined window name
	Width         int
	Height        int
	X             int
	Y             int
	Z             int
	ID            string
	Terminal      vt.Terminal
	Pty           xpty.Pty
	Cmd           *exec.Cmd // Write-once; the monitor goroutine reads it unlocked, see waitForCmd
	ShellPgid     int       // Process group ID of the shell
	cwd           cwdCache
	LastUpdate    time.Time
	Dirty         bool
	ContentDirty  bool
	PositionDirty bool
	CachedContent string
	CachedLayer   *lipgloss.Layer
	// CachedLayerBorder is the border colour CachedLayer was drawn with. The
	// border colour depends on things outside the pane (focus, the input mode,
	// the session tint), so the renderer reuses the layer only while the colour
	// it wants matches this one. Issue #642: entering terminal mode on an idle
	// pane kept the window mode border until the guest drew something.
	CachedLayerBorder color.Color
	reportKey         string
	// RenderedCols and RenderedRows are the display geometry of the pane body
	// the renderer produced last: the column count every one of its lines
	// fills, and its line count. Zero means the renderer cannot vouch for the
	// frame, which is how one it did not lay out over the whole grid is
	// reported. The border box reads them to decide whether it still has to
	// re-flow the body to the pane's rectangle, which costs a wrap and three
	// width scans over a frame that is already exactly that shape.
	RenderedCols, RenderedRows int
	// CachedContentCols and CachedContentRows are the same geometry for
	// CachedContent. They are written only where CachedContent is, so a
	// rectangle can never be read against a frame it does not describe.
	CachedContentCols, CachedContentRows int
	cachedContentDim                     int
	// CachedCursor is what this window's cursor was the last time the render
	// loop could read it outside a synchronized update: where it is, whether it
	// is hidden, and the shape the guest asked for. Reading the live one needs
	// the I/O lock, which output bursts can hold almost continuously. Reusing
	// this sample keeps those panes from delaying the user's keystroke echo.
	CachedCursor       uv.Position
	CachedCursorHidden bool
	CachedCursorStyle  vt.CursorStyle
	CachedCursorSteady bool
	// CachedCursorColor is the cursor colour the guest set with OSC 12, nil
	// when it set none, so the host keeps its own.
	CachedCursorColor color.Color
	// SyncHoldContent is the last frame this window rendered from a guest that
	// was not mid-update, kept solely so the synchronized-output hold (DEC 2026)
	// has something complete to present. Every other cache here is invalidated
	// by a layout action, which is precisely when the hold needs one: a retile
	// or a scroll landing while the guest is between 2026h and 2026l used to
	// leave the renderer nothing to hold and it composed the half-drawn buffer.
	// Replaced by the next complete frame and dropped on close; deliberately
	// untouched by MarkContentDirty, MarkPositionDirty and InvalidateCache.
	SyncHoldContent        string
	LastTerminalSeq        int
	IsBeingManipulated     bool // True when being dragged or resized
	announcedW, announcedH int
	toldW, toldH           int
	announceHolds          int
	UpdateCounter          int // Counter for throttling background updates
	cancelFunc             context.CancelFunc
	ioMu                   sync.RWMutex
	Minimized              bool      // True when window is minimized to dock
	MinimizedAt            int64     // When the daemon saw it minimized (Unix ns), for the dock's order; 0 outside a daemon session
	MinimizeHighlightUntil time.Time // Highlight dock tab until this time
	PreMinimizeX           int       // Store position before minimizing
	PreMinimizeY           int       // Store position before minimizing
	PreMinimizeWidth       int       // Store size before minimizing
	PreMinimizeHeight      int       // Store size before minimizing
	Workspace              int       // Workspace this window belongs to
	Zoomed                 bool      // True when window is zoomed (fullscreen)
	PreZoomX               int       // Store position before zooming
	PreZoomY               int       // Store position before zooming
	PreZoomWidth           int       // Store size before zooming
	PreZoomHeight          int       // Store size before zooming
	processExited          atomic.Bool
	// Multi-click tracking. What a press selects is decided by how many clicks
	// it makes; the selection itself is copy mode's, see CopyMode below.
	LastClickTime time.Time
	LastClickX    int
	LastClickY    int
	ClickCount    int
	// ScrollbackOffset mirrors CopyMode.ScrollOffset for rendering
	ScrollbackOffset int // Number of lines scrolled back (0 = at bottom, viewing live output)
	scrollAnchorLine int
	scrollAnchored   bool
	scrollOffsetSeen int
	isAltScreen      atomic.Bool
	// Opening marks a pane that has just been created and has not yet been
	// placed by the tiling layout. The layout consumes it to decide where the
	// pane's open animation starts from, and clears it, so it is true for
	// exactly one placement. Client-local and never synced: whether a pane is
	// new is a property of the client that is about to draw it appearing, not
	// of the session.
	Opening bool
	// Floating pane support
	IsFloating bool // True when window is floating (not in BSP tiling)
	IsPinned   bool // True when floating pane persists across workspace switches
	// IsPopup marks a transient floating pane that runs one command and closes
	// when the command exits. It rides with IsFloating rather than replacing it,
	// because everything that skips a float has to skip a popup too.
	//
	// PopupWidth and PopupHeight are the size the caller asked for, as written:
	// "60" for cells, "60%" for a share of the pane region. The request is kept
	// rather than the box it resolves to, because the box is this client's and
	// the request is the session's. See session.WindowState.PopupWidth.
	IsPopup bool
	// IsScratch marks a pane of a scratch group, an ordinary window on the
	// group's own workspace. It is the session's, like IsPopup. See
	// session.WindowState.Scratch.
	IsScratch bool
	// Stack names the stack the pane is in, empty when it is in none, and
	// StackIndex is its place in that stack. The stack's members share one
	// tile: the one that is not minimized has it, and every other shows as a
	// title row. See app/stack.go.
	Stack      string
	StackIndex int
	// Kind and URI say what draws the pane and what it shows: empty for a
	// terminal, or "view", "web" or "app" for a pane a native renderer draws
	// itself. The daemon sets them. See session.WindowState.Kind.
	Kind string
	URI  string
	// ScratchName keys a scratch pane: "scratch" (or empty, from before
	// names) for the built-in one, the entry's name for a command entry.
	ScratchName string
	// Rail marks the session's rail pane, the pane a client draws in its
	// rail's pane section, with the command line it runs. It is the
	// session's, like IsScratch. See session.WindowState.Rail.
	Rail string

	PopupWidth  string
	PopupHeight string
	// PopupDim is nil to inherit the popup or modal dim, or an explicit override.
	PopupDim *config.PopupDim
	// Cell dimensions in pixels (for TIOCGWINSZ pixel reporting to child processes)
	CellPixelWidth  int
	CellPixelHeight int
	// Vim-style copy mode
	CopyMode *CopyMode // Copy mode state (nil when not active)
	// Daemon session support
	PTYID             string                   // ID of daemon-managed PTY (empty for local PTYs)
	DaemonMode        bool                     // True when PTY is managed by daemon
	DaemonWriteFunc   func([]byte) error       // Callback for sending input to daemon PTY
	DaemonResizeFunc  func(w, h int) error     // Callback for resizing daemon PTY
	DaemonCloseFunc   func()                   // Callback when window is closed (to notify daemon)
	OnProcessExit     func()                   // Callback when PTY process exits (to close window)
	ClipboardSetFunc  func(string)             // Callback to propagate clipboard to host
	NotifyFunc        func(title, body string) // Callback for guest desktop notifications (OSC 9/777/99)
	BellFunc          func()                   // Callback for guest bell (BEL)
	CwdFunc           func(cwd string)         // Callback for the shell's working directory changing (OSC 7)
	NvimNavFunc       func(direction string)   // Callback for nvim navigation requests (OSC 7777)
	NvimNavStateFunc  func(active bool)        // Callback for nvim navigation state (OSC 7777)
	NvimNavResetFunc  func()                   // Callback when the alternate screen exits
	DnDFunc           func(dnd.Command)        // Callback for a drag and drop message (OSC 72)
	outputQ           *outputQueue
	outputDone        chan struct{}
	suppressCallbacks atomic.Bool
	closed            atomic.Bool

	hasFocus       atomic.Bool
	focusReporting bool

	// HasNewOutput is set when new data is written to the terminal.
	// Used by MarkTerminalsWithNewContent to avoid unconditional dirty-marking.
	HasNewOutput atomic.Bool
	// HasGraphicsOutput is set instead of HasNewOutput for a write that was
	// only kitty graphics commands leaving every cell as it was, while the
	// cursor is hidden: a frame of a guest that streams images. See
	// graphicsOnly.
	HasGraphicsOutput atomic.Bool

	frameInterval atomic.Int64

	coalesceSignal atomic.Bool

	coalesceWake chan struct{}

	renderCostNanos atomic.Int64

	queuedBytes atomic.Int64

	outputEpoch atomic.Uint64

	writesBegun, writesNoted atomic.Uint64

	streamOwnsSize atomic.Bool

	lastScrollbackLen atomic.Int64

	// PTYDataChan is a shared channel (buffered 1) that PTY readers signal
	// to trigger rendering. Non-blocking send coalesces rapid updates.
	PTYDataChan chan struct{}

	Tiled bool // True when window is in shared-border tiling mode (no individual borders)

	// AgentState is the semantic agent state the daemon reports for this pane
	// (working, needs_input, idle, done, errored, or empty for none). It is set
	// from the daemon state sync and read by the renderer to draw the per-window
	// state indicator; it is written and read on the UI goroutine, like CustomName.
	AgentState string
	// AgentMessage is the optional short note reported with AgentState.
	AgentMessage string
	// AgentReason is why AgentState is not the state the agent gave:
	// "silent" for a hooked pane that stopped reporting, whose AgentState is
	// then unknown. Empty otherwise.
	AgentReason string
	// AgentKind is what sort of block a needs_input state is ("approval",
	// "question"), as the daemon recorded it, empty when the source did not
	// say. The rail draws it as the row's need word.
	AgentKind string
	// AgentHarness is the harness id the reporting source named, empty when the
	// state came from something that named none. Alert sinks pass it on.
	AgentHarness string
	// StartedBySession and StartedByWindow name the pane whose call created
	// this one, as the daemon recorded it. Both are empty when no pane did.
	StartedBySession string
	StartedByWindow  string
	// AgentChild marks an agent pane another agent started: the StartedBy
	// pane is its parent agent, and the rail draws its row under the
	// parent's.
	AgentChild bool
	// AgentMeta is what the pane reported about its agent through
	// set-agent-meta, as the daemon synced it. The rail draws it. Replaced
	// whole on every sync and never edited in place.
	AgentMeta []sessiontree.MetaToken
	// AgentQueued is how many messages wait in the pane's delivery queue, as the
	// daemon synced it. Zero for none, and from a daemon that has no queue.
	AgentQueued int
	// AgentDraftHeld is true while the person's draft in the agent's input
	// box holds the next queued message, as the daemon synced it.
	AgentDraftHeld bool
	// AgentSettled marks an agent the person put away on the Settled shelf,
	// as the daemon synced it.
	AgentSettled bool
	// AgentQuiet marks a pane that raises no alert unless it asks for an
	// approval. The rail marks the row.
	AgentQuiet bool
	// AgentSubagents is how many subagents the pane's agent is running, as the
	// daemon synced it. Zero for none, and from a daemon that counts none.
	AgentSubagents int
	// AgentSubagentList is the subagents themselves, as the daemon synced
	// them, in start order. Nil from a daemon that sends only the count.
	// Replaced whole on every sync and never edited in place.
	AgentSubagentList []sessiontree.Subagent
	// ProgramStatus is the pane's OSC 7501 records, as the daemon synced them,
	// the root first. Replaced whole on every sync and never edited in place.
	ProgramStatus []sessiontree.ProgramRecord
	// AgentStateAt is when the pane entered AgentState (Unix nanoseconds), as
	// the daemon stamped it. The rail shows the elapsed time so a pane waiting
	// on input reads differently from one that just started working.
	AgentStateAt int64
	// AgentCompletionSeq is the daemon's count of turns this pane has finished
	// (session.WindowState.CompletionSeq). The rail compares it with the count
	// the pane had when it was last focused.
	AgentCompletionSeq uint64
	// ForegroundCmd is the base name of what the pane is running, as the daemon
	// detected it, or empty at a shell prompt. Session surfaces label a row with
	// it, because a title is the same string for every pane in one directory.
	ForegroundCmd string
	// Cwd is the directory the pane's shell last said it was in, from OSC 7, or
	// empty when it has never said.
	//
	// It is written on the Update goroutine from the cwd-change channel, never
	// from the PTY reader, which is why it is a plain field: CwdFunc does the
	// non-blocking send and the app applies it where the rest of the window
	// model is owned.
	//
	// Empty is a real and common state. A shell only fills this in if it emits
	// OSC 7, which fish does out of the box and bash and zsh mostly do not, so
	// anything reading it has to have an answer for not knowing.
	Cwd string
	// DaemonCwd is the last directory the daemon reported for the pane, so a
	// client that takes the directory from the daemon can tell a new answer
	// from the same answer sent again. See app.OS.takeDaemonCwd.
	DaemonCwd string
	// CwdAnnounced says this client parsed an OSC 7 report from the pane's
	// shell itself, so Cwd is the shell's own answer and as fresh as any.
	CwdAnnounced bool
	// CwdHost names the machine the pane's shell last reported a folder on,
	// when that is not the machine the pane runs on: the pane is running ssh.
	// Empty otherwise. Only the daemon reports it.
	CwdHost string
	// CwdElsewhereDir is the folder that report from CwdHost named. An ssh
	// split starts the new pane there. Only a client that runs the pane
	// itself sets it.
	CwdElsewhereDir string

	// Host is the machine the pane's process runs on, empty for this one.
	//
	// It is carried on the client, rather than left in the session state the
	// daemon holds, because it is a thing the person has to be able to see. A
	// pane whose shell is on another machine looks exactly like one whose
	// shell is here, and the difference decides what a command in it does: the
	// same line is a different act on a build box than on a laptop. So the
	// frame says which machine, and it says it whether or not the shell is
	// cooperating about its prompt.
	Host          string
	hostElsewhere atomic.Bool
	// HostLink is "reconnecting" while the link to Host is lost and the far
	// machine keeps the process for its grace, and empty otherwise. The frame
	// says so in words, because a pane whose screen stopped moving looks the
	// same as one whose process has nothing to say.
	HostLink string
	// ScreenStopped is set while the daemon has stopped feeding this pane's
	// terminal emulator after it failed again and again. The rail says so.
	ScreenStopped bool
	// Recording is set while the daemon writes this pane to an asciicast
	// file (record-pane). The rail marks the pane.
	Recording bool

	KittyPassthroughFunc func(cmd *vt.KittyCommand, rawData []byte)
	SixelPassthroughFunc func(cmd *vt.SixelCommand, cursorX, cursorY, absLine int)

	cmdWaitOnce sync.Once
	ioWg        sync.WaitGroup
}

// CopyModeState represents the current state within copy mode
type CopyModeState int

const (
	// CopyModeNormal is the default navigation mode
	CopyModeNormal CopyModeState = iota
	// CopyModeSearch is active when typing a search query
	CopyModeSearch
	// CopyModeVisualChar is character-wise visual selection
	CopyModeVisualChar
	// CopyModeVisualLine is line-wise visual selection
	CopyModeVisualLine
	// CopyModeVisualBlock is a rectangular visual selection
	CopyModeVisualBlock
)

// InVisual reports whether the state is one of the visual selections.
func (s CopyModeState) InVisual() bool {
	return s == CopyModeVisualChar || s == CopyModeVisualLine || s == CopyModeVisualBlock
}

// Position represents a 2D coordinate
type Position struct {
	X, Y int
}

// SearchMatch represents a single search result
type SearchMatch struct {
	Line   int    // Absolute line number (scrollback + screen)
	StartX int    // Start column
	EndX   int    // End column (exclusive)
	Text   string // Matched text
}

// SearchCache caches search results for performance
type SearchCache struct {
	Query     string
	Matches   []SearchMatch
	CacheTime time.Time
	Valid     bool

	// What the search was made over, so the next one can tell whether it
	// may search only the lines this one matched. See narrowedLines in
	// internal/input. CaseSensitive is the setting it ran with. Capped is
	// set when it stopped at the match limit, so Matches may not be all
	// of them. HistoryGen and HistoryLen are the history's generation and
	// length.
	CaseSensitive bool
	Capped        bool
	HistoryGen    uint64
	HistoryLen    int
}

// CopyPending is the second key a copy-mode command waits for. At most one
// is pending: a key that is not the one waited for ends the wait.
type CopyPending uint8

const (
	// CopyPendingNone waits for nothing.
	CopyPendingNone CopyPending = iota
	// CopyPendingG waits for the second g of gg.
	CopyPendingG
	// CopyPendingChar waits for the character after f, F, t or T.
	CopyPendingChar
)

// CopyMode holds all state for vim-style copy/scrollback mode
type CopyMode struct {
	Active       bool          // True when copy mode is active
	State        CopyModeState // Current sub-state
	CursorX      int           // Cursor X position (relative to viewport)
	CursorY      int           // Cursor Y position (relative to viewport)
	ScrollOffset int           // Lines scrolled back from bottom

	// Visual selection state
	VisualStart Position // Selection start (absolute coordinates)
	VisualEnd   Position // Selection end (absolute coordinates)

	// Search state
	Search         textedit.Line // the search text being typed, with its caret
	SearchMatches  []SearchMatch // All search results
	CurrentMatch   int           // Index of current match
	CaseSensitive  bool          // Case-sensitive search
	SearchBackward bool          // True for ? (backward), false for / (forward)
	SearchCache    SearchCache   // Cached search results (exported for copymode package)

	// Pending is the second key a two-key command waits for: the second g of
	// gg, or the character of f, F, t or T. PendingSince is when it began.
	Pending      CopyPending
	PendingSince time.Time

	// SearchOrigin is where the cursor was when the search prompt opened.
	// Each key typed into the prompt searches again from here, not from the
	// match the previous key jumped to, and Esc puts the cursor back here.
	SearchOrigin SearchOrigin

	// Character search state (f/F/t/T commands)
	PendingCharCount   int  // The count typed before f/F/t/T, 1 when none
	LastCharSearch     rune // Last searched character
	LastCharSearchDir  int  // 1 for forward (f/t), -1 for backward (F/T)
	LastCharSearchTill bool // true for till (t/T), false for find (f/F)

	// Count prefix (e.g., 10j means move down 10 times)
	PendingCount   int       // Accumulated count (0 means no count)
	CountStartTime time.Time // When the last digit of the count was typed

	// Implicit marks a copy mode session that the user never asked for.
	//
	// Rendering scrollback is copy mode's job, so a mouse wheel or a drag
	// inside a pane has to turn it on to show anything at all. That is a
	// mechanism, not a mode the user chose: an implicit session announces
	// nothing, keeps the dock showing terminal mode, draws no copy-mode
	// cursor, and ends the moment the view is back at the bottom or a key is
	// pressed. Copy mode entered on purpose (the prefix binding, the command
	// palette) leaves this false and behaves exactly as it always has.
	Implicit bool
}

// SearchMatchCount is the match count as the search prompt shows it: "1000+"
// when the search stopped at limit matches (appearance.copy_search_max_matches),
// so a capped count does not read as the whole count.
func SearchMatchCount(n, limit int) string {
	if n >= limit {
		return fmt.Sprintf("%d+", limit)
	}
	return fmt.Sprint(n)
}

// SearchOrigin is the copy-mode cursor position saved when a search prompt
// opens. Line is an absolute buffer line (scrollback first, then the screen),
// not a scroll offset: a scroll offset counts from the bottom, so output that
// arrives while the prompt is open would move it to another line. Row is the
// viewport row the cursor was on, kept so a restore shows the same view.
type SearchOrigin struct {
	CursorX, Row, Line int
}

// BeginSearch opens the search prompt: / when backward is false, ? when it is
// true. The cursor position is saved as the origin the search runs from.
// scrollbackLen is the pane's scrollback length now, to make the origin line
// absolute. Matches from an earlier search are dropped, so an empty query
// shows none.
func (cm *CopyMode) BeginSearch(backward bool, scrollbackLen int) {
	cm.State = CopyModeSearch
	cm.Search.Set("")
	cm.SearchBackward = backward
	cm.SearchMatches = nil
	cm.CurrentMatch = 0
	cm.SearchOrigin = SearchOrigin{
		CursorX: cm.CursorX,
		Row:     cm.CursorY,
		Line:    scrollbackLen - cm.ScrollOffset + cm.CursorY,
	}
}

func shortID(id string) string {
	if len(id) < 8 {
		return id
	}
	return id[:8]
}

func newWindowBase(id, title string, x, y, width, height, z int, ptyDataChan chan struct{}, scrollbackLines int) *Window {
	if title == "" {
		title = "Terminal " + shortID(id)
	}

	terminalWidth := max(width-2, 1)
	terminalHeight := max(height-2, 1)
	terminal := vt.NewWithScrollback(terminalWidth, terminalHeight, scrollbackLines)
	scrollclear.Apply(terminal)

	window := &Window{
		Width:         width,
		Height:        height,
		X:             x,
		Y:             y,
		Z:             z,
		ID:            id,
		Terminal:      terminal,
		PTYDataChan:   ptyDataChan,
		LastUpdate:    time.Now(),
		Dirty:         true,
		ContentDirty:  true,
		PositionDirty: true,
	}
	window.CachedCursorStyle, window.CachedCursorSteady = terminal.CursorStyle()
	window.SetTitle(title)

	applyTheme(terminal)

	terminal.SetCallbacks(vt.Callbacks{
		AltScreen: func(enabled bool) {
			if !window.suppressCallbacks.Load() {
				window.SetAltScreen(enabled)
				if !enabled && window.NvimNavResetFunc != nil {
					window.NvimNavResetFunc()
				}
			}
		},
		Title: func(title string) {
			if title != "" {
				window.SetTitle(title)
			}
		},
		ExplicitIconName: func(name string) {
			if !window.DaemonMode {
				window.SetIconName(name)
			}
		},
		ClipboardSet: func(_ string, content string) {
			if window.ClipboardSetFunc != nil {
				window.ClipboardSetFunc(content)
			}
		},
		Notify: func(title, body string) {
			if window.NotifyFunc != nil {
				window.NotifyFunc(title, body)
			}
		},
		Bell: func() {
			if window.BellFunc != nil {
				window.BellFunc()
			}
		},
		WorkingDirectory: func(cwd string) {
			if window.CwdFunc != nil {
				window.CwdFunc(cwd)
			}
		},
		PrivateOSC: func(cmd int, data []byte) {
			if cmd == dnd.Code {
				if window.DnDFunc != nil {
					if dnd, ok := dnd.Parse(data); ok {
						window.DnDFunc(dnd)
					}
				}
				return
			}
			if cmd != nvimNavigatorOSC {
				return
			}
			handleNvimNavigatorOSC(data, func(direction string) {
				if window.NvimNavFunc != nil {
					window.NvimNavFunc(direction)
				}
			}, func(active bool) {
				if window.NvimNavStateFunc != nil {
					window.NvimNavStateFunc(active)
				}
			})
		},
	})

	return window
}

// NewWindow creates a new terminal window with the specified properties.
// It spawns a shell process, sets up PTY communication, and initializes the virtual terminal.
//
// A failure returns a nil window and the reason. The reason used to be dropped
// on the floor, which is how a pane that never appeared came to leave nothing
// behind to look at; the daemon path has always returned this error, and a user
// who cannot open a pane is owed the same answer whichever path they are on.
//
// command, when given, becomes the pane's process in place of the shell: argv
// exec'd directly, no shell parsing anything. The launcher runs programs this
// way because bytes typed at a shell are re-parsed by whatever shell it is,
// and the window closes when the program exits, the same way it does when a
// shell does.
func NewWindow(id, title string, x, y, width, height, z int, exitChan chan string, ptyDataChan chan struct{}, scrollbackLines int, command ...string) (*Window, error) {
	return NewWindowIn("", id, title, x, y, width, height, z, exitChan, ptyDataChan, scrollbackLines, command...)
}

// NewWindowIn is NewWindow with the directory the process starts in. An empty
// dir, or one that is not a directory, keeps this process's own.
func NewWindowIn(dir, id, title string, x, y, width, height, z int, exitChan chan string, ptyDataChan chan struct{}, scrollbackLines int, command ...string) (*Window, error) {
	return NewWindowInCell(dir, id, title, x, y, width, height, z, exitChan, ptyDataChan, scrollbackLines, 0, 0, command...)
}

// NewWindowInCell is NewWindowIn with the host's cell in pixels, zero when it
// is not known. The pty is made with that cell's pixel size before the process
// starts, so the SetCellPixelDimensions that follows writes the size the pty
// already has. Born with the fallback cell instead, the pty changed in its
// pixels alone when the host's cell came, and the kernel sent the shell a
// SIGWINCH at the size it started with.
func NewWindowInCell(dir, id, title string, x, y, width, height, z int, exitChan chan string, ptyDataChan chan struct{}, scrollbackLines, cellWidth, cellHeight int, command ...string) (*Window, error) {
	if dir != "" && !ptyspawn.DirExists(dir) {
		dir = ""
	}
	window := newWindowBase(id, title, x, y, width, height, z, ptyDataChan, scrollbackLines)
	terminalWidth := max(width-2, 1)
	terminalHeight := max(height-2, 1)

	termType, colorTerm := getTerminalEnv()

	if debuglog.Enabled() {
		debugMsg := fmt.Sprintf("[%s] NewWindow TERM=%s COLORTERM=%s (envTERM=%s envCOLORTERM=%s)\n",
			time.Now().Format("15:04:05.000"), termType, colorTerm, os.Getenv("TERM"), os.Getenv("COLORTERM"))
		if f, err := debuglog.Open(debuglog.Path); err == nil {
			_, _ = f.WriteString(debugMsg)
			_ = f.Close()
		}
	}

	ptyInstance, cmd, err := ptyspawn.SpawnTTY(terminalWidth, terminalHeight, cellWidth, cellHeight, func(string) *exec.Cmd {
		// #nosec G204 - the command is the user's own shell or a program they chose
		var cmd *exec.Cmd
		shell, integration, login := "", false, false
		if len(command) > 0 {
			cmd = exec.Command(command[0], command[1:]...)
		} else {
			shell, integration, login = detectShell()
			cmd = exec.Command(shell)
		}
		cmd.Dir = dir
		cmd.Env = append(guestBaseEnv(),
			"TERM="+termType,
			"COLORTERM="+colorTerm,
			"TERM_PROGRAM="+guestTermProgram(command),
			"TERM_PROGRAM_VERSION="+guestenv.TermProgramVersion(),
			"TUIOS_WINDOW_ID="+id,
			guestKittyAnimation(),
		)
		if integration {
			if args, env, ok := shellinteg.Setup(shell, cmd.Env); ok {
				cmd.Args = append(cmd.Args, args...)
				cmd.Env = env
			}
		}
		if login {
			shellinteg.Login(cmd)
		}
		return cmd
	}, debugLine)
	if err != nil {
		return nil, err
	}

	_ = ptyspawn.SetWinsize(ptyInstance, terminalWidth, terminalHeight, terminalWidth*cellWidth, terminalHeight*cellHeight)
	window.SeedAnnouncedSize(terminalWidth, terminalHeight)

	_, cancel := context.WithCancel(context.Background())

	window.Pty = ptyInstance
	window.Cmd = cmd
	window.cancelFunc = cancel

	if cmd.Process != nil {
		if pgid, err := getPgid(cmd.Process.Pid); err == nil {
			window.ShellPgid = pgid
		}
	}

	window.PublishGeometry()

	window.outputDone = make(chan struct{})
	window.coalesceWake = make(chan struct{}, 1)
	go window.renderCoalescer()

	window.handleIOOperations()

	window.enableTerminalFeatures()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("window %s goroutine panic: %v\n%s", window.ID, r, debug.Stack())
			}
		}()

		window.waitForCmd()

		window.SetProcessExited(true)

		cancel()

		time.Sleep(config.ProcessWaitDelay)

		select {
		case exitChan <- id:
		default:
		}
	}()

	return window, nil
}

// NewDaemonWindow creates a new terminal window that uses a daemon-managed PTY.
// Unlike NewWindow, this doesn't spawn a local PTY. I/O is proxied through the daemon.
// The caller is responsible for subscribing to PTY output and handling I/O.
func NewDaemonWindow(id, title string, x, y, width, height, z int, ptyID string, ptyDataChan chan struct{}, scrollbackLines int) *Window {
	window := newWindowBase(id, title, x, y, width, height, z, ptyDataChan, scrollbackLines)
	window.PTYID = ptyID
	window.DaemonMode = true
	window.outputQ = newOutputQueue()
	window.outputDone = make(chan struct{})
	window.coalesceWake = make(chan struct{}, 1)

	go window.outputWriter()
	go window.renderCoalescer()

	return window
}

// GuestCursor reads the guest's cursor: its position and whether it is
// hidden. It also refreshes the Cached* cursor fields, the style and colour
// included, so a caller that needs them reads them there after this call.
//
// The I/O lock is taken only if it is free. A pane in an output burst holds
// the exclusive side almost continuously, and the frame that would wait for
// it carries the user's keystroke echo. When the lock is busy the cursor the
// last read saw is returned. During a synchronized update the same cache holds
// the cursor with the last complete content, rather than exposing the guest's
// intermediate drawing positions or styles. ok is false without a terminal.
func (w *Window) GuestCursor() (pos uv.Position, hidden, ok bool) {
	if w.Terminal == nil {
		return pos, false, false
	}
	if w.TryRLockIO() {
		if w.Terminal == nil {
			w.RUnlockIO()
			return pos, false, false
		}
		if !w.Terminal.IsSyncActive() {
			w.CachedCursor = w.Terminal.CursorPosition()
			w.CachedCursorHidden = w.Terminal.IsCursorHidden()
			w.CachedCursorStyle, w.CachedCursorSteady = w.Terminal.CursorStyle()
			w.CachedCursorColor = w.Terminal.GuestCursorColor()
		}
		w.RUnlockIO()
	}
	return w.CachedCursor, w.CachedCursorHidden, true
}

// SetHasFocus records whether this pane has focus on a screen someone could be
// looking at. A local pane whose guest turns on focus reporting (DECSET 1004)
// is sent the focus-in report when this is true, as xterm sends the current
// state on the set. A daemon pane is answered by the daemon instead.
func (w *Window) SetHasFocus(focused bool) {
	w.hasFocus.Store(focused)
}

// SetHost records the machine the pane's process runs on.
func (w *Window) SetHost(host string) {
	w.Host = host
	w.hostElsewhere.Store(host != "")
}

// ProcessElsewhere reports whether the pane's process runs on another
// machine. It is safe from any goroutine.
func (w *Window) ProcessElsewhere() bool { return w.hostElsewhere.Load() }

// SearchQuery is the search text.
func (cm *CopyMode) SearchQuery() string { return cm.Search.String() }
