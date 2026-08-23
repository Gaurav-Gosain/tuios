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

	uv "github.com/charmbracelet/ultraviolet"

	"charm.land/lipgloss/v2"
	xpty "github.com/charmbracelet/x/xpty"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// ioMu guards the emulator cell buffer: the PTY reader and the daemon output
// path write it, Resize reallocates it, and the renderer reads it.
//
// It is NOT reentrant, and that is not a style preference but a hard
// correctness rule. sync.RWMutex starves readers once a writer is queued, so a
// goroutine that holds RLockIO and then takes RLockIO again on the same window
// parks behind that queued writer while still holding the read lock the writer
// is waiting for. Neither side can proceed and the process wedges at zero CPU.
// A live PTY produces output constantly, so the writer is queued often and the
// window for this is wide.
//
// The rule for callers: never take either lock while already holding either
// lock on the same window, and do not call a helper that takes one from inside
// a locked region. Where a value is needed on both sides of that boundary,
// follow the split this package already uses - a locking entry point plus a
// lock-free variant for callers that are already inside (ScrollbackLenSync
// versus ScrollbackLen) - or hoist the read out of the locked region entirely.

// LockIO/UnlockIO: exclusive lock for PTY writes (mutates cell buffer).
func (w *Window) LockIO()   { w.ioMu.Lock() }
func (w *Window) UnlockIO() { w.ioMu.Unlock() }

// RLockIO/RUnlockIO: shared lock for rendering (reads cell buffer).
func (w *Window) RLockIO()   { w.ioMu.RLock() }
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

// The following scalar/string fields are written by the VT callbacks on the
// PTY/monitor goroutine and read on the Bubble Tea UI goroutine, so they are
// stored atomically and accessed only through these methods.

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

// IsAltScreen reports whether the application is using the alternate screen buffer.
func (w *Window) IsAltScreen() bool { return w.isAltScreen.Load() }

// SetAltScreen records whether the application is using the alternate screen buffer.
func (w *Window) SetAltScreen(v bool) { w.isAltScreen.Store(v) }

// clipboard returns the last clipboard content set via OSC 52.
func (w *Window) clipboard() string {
	if p := w.clipboardContent.Load(); p != nil {
		return *p
	}
	return ""
}

// setClipboard records the last clipboard content set via OSC 52.
func (w *Window) setClipboard(content string) { w.clipboardContent.Store(&content) }

// Cache for local terminal environment variables (detect once, reuse for local windows)
// SSH sessions will detect per-connection based on their environment
var (
	localTermType  string
	localColorTerm string
	localEnvOnce   sync.Once
)

// Window represents a terminal window with its own shell process.
// Each window maintains its own virtual terminal, PTY, and rendering cache.
// Scrollback buffer support is provided by the vendored vt library.
type Window struct {
	title         atomic.Pointer[string]           // Written on PTY/monitor goroutine, read on UI goroutine
	geomSnap      atomic.Pointer[GeometrySnapshot] // See PublishGeometry
	CustomName    string                           // User-defined window name
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
	cwd           cwdCache  // Memoised working directory, see CWD
	LastUpdate    time.Time
	Dirty         bool
	ContentDirty  bool
	PositionDirty bool
	CachedContent string
	CachedLayer   *lipgloss.Layer
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
	// cachedContentDim is the dim percentage CachedContent was rendered at,
	// zero for an undimmed frame. It belongs with the two above for the same
	// reason: a cached frame is only usable by a caller that wants the frame it
	// actually holds, and once unfocused panes can be dimmed, focus is one of
	// the things that decides what it holds.
	//
	// Unexported behind accessors because nothing outside the render path has
	// any business setting it, and setting it wrong serves a stale frame.
	cachedContentDim int
	// CachedCursor is what this window's cursor was the last time the render
	// loop could read it: where it is, whether it is hidden, and the shape the
	// guest asked for. Reading the live one needs the I/O lock, which a pane
	// flooding output holds in a near-continuous burst, and the frame that
	// would block on it is the same frame carrying the user's keystroke echo.
	// Serving a cursor one frame old costs nothing anyone can see.
	CachedCursor       uv.Position
	CachedCursorHidden bool
	CachedCursorStyle  vt.CursorStyle
	CachedCursorSteady bool
	// SyncHoldContent is the last frame this window rendered from a guest that
	// was not mid-update, kept solely so the synchronized-output hold (DEC 2026)
	// has something complete to present. Every other cache here is invalidated
	// by a layout action, which is precisely when the hold needs one: a retile
	// or a scroll landing while the guest is between 2026h and 2026l used to
	// leave the renderer nothing to hold and it composed the half-drawn buffer.
	// Replaced by the next complete frame and dropped on close; deliberately
	// untouched by MarkContentDirty, MarkPositionDirty and InvalidateCache.
	SyncHoldContent    string
	LastTerminalSeq    int
	IsBeingManipulated bool // True when being dragged or resized
	// announcedW/H are the emulator dimensions last handed downstream: to the
	// PTY, to the daemon, and to the guest as a redraw. Resize used to decide
	// "did the size change" by comparing against Width and Height, which is
	// wrong the moment a resize is split in two. ResizeVisual sets Width and
	// Height for the live preview, so by the time the deferred half runs they
	// already match, Resize concludes nothing changed, and nothing downstream is
	// told - the guest keeps drawing to the size it had before the drag.
	//
	// INVARIANT: this is the size the real PTY has, and Resize skips announcing
	// on the strength of it. So the two must move together. Resize and
	// SeedAnnouncedSize are the only things allowed to write these fields, and
	// nothing outside this file may call Pty.Resize or DaemonResizeFunc without
	// recording the result: a caller that announces a size behind Resize's back
	// leaves the record naming a size the shell no longer has, and the next
	// Resize back to that size is skipped as redundant when it is the only thing
	// that would have corrected the shell. That is exactly how a full-screen
	// pane ended up running an 80x24 shell.
	announcedW, announcedH int
	// toldW/H are the size the guest has actually been sent, which equals
	// announcedW/H except while a hold is open. A layout update walks a pane
	// through several rectangles and only the last one is real, so the hold lets
	// Resize record the intent step by step and sends the settled size once.
	// See HoldAnnouncements.
	toldW, toldH  int
	announceHeld  bool
	UpdateCounter int                // Counter for throttling background updates
	cancelFunc    context.CancelFunc // For graceful goroutine cleanup
	// ioMu guards the emulator cell buffer and the Pty/Terminal handles. See
	// the block comment above LockIO for the full contract; the short version:
	//
	//   LOCK ORDER (global, whole process):
	//       app.OS.terminalMu  ->  Window.ioMu  ->  KittyPassthrough.mu / SixelPassthrough.mu
	//
	//   May be held together: terminalMu and ioMu, in that order only
	//   (renderTerminal does exactly this). Never take terminalMu while
	//   holding ioMu. Never take ioMu inside a passthrough callback: the PTY
	//   reader already holds ioMu across Terminal.Write, which dispatches
	//   those callbacks under kp.mu/sp.mu, so the reverse order closes a
	//   cycle (see OS.snapshotPlacementScrollbackLens).
	//
	//   NOT REENTRANT, either side, on the same window. sync.RWMutex starves
	//   readers behind a queued writer, so RLock-inside-RLock deadlocks
	//   against a writer waiting on the outer RLock.
	//
	//   NEVER BLOCK WHILE HOLDING IT. No Pty.Write, no Pty.Read, no channel
	//   send, no Cmd.Wait. Snapshot the handle under the lock, release, then
	//   block (SendInput and both handleIOOperations goroutines do this). A
	//   blocking write under the read lock wedges the renderer, because the
	//   PTY reader's queued LockIO starves every later RLock.
	//
	//   Two windows' ioMu are never held simultaneously, so there is no
	//   window-to-window ordering to respect.
	ioMu                   sync.RWMutex
	Minimized              bool        // True when window is minimized to dock
	Minimizing             bool        // True when window is being minimized (animation playing)
	MinimizeHighlightUntil time.Time   // Highlight dock tab until this time
	MinimizeOrder          int64       // Unix nano timestamp when minimized (for dock ordering)
	PreMinimizeX           int         // Store position before minimizing
	PreMinimizeY           int         // Store position before minimizing
	PreMinimizeWidth       int         // Store size before minimizing
	PreMinimizeHeight      int         // Store size before minimizing
	Workspace              int         // Workspace this window belongs to
	Zoomed                 bool        // True when window is zoomed (fullscreen)
	PreZoomX               int         // Store position before zooming
	PreZoomY               int         // Store position before zooming
	PreZoomWidth           int         // Store size before zooming
	PreZoomHeight          int         // Store size before zooming
	processExited          atomic.Bool // Written on PTY/monitor goroutine, read on UI goroutine
	// Multi-click tracking. What a press selects is decided by how many clicks
	// it makes; the selection itself is copy mode's, see CopyMode below.
	LastClickTime time.Time
	LastClickX    int
	LastClickY    int
	ClickCount    int
	// ScrollbackOffset mirrors CopyMode.ScrollOffset for rendering
	ScrollbackOffset int // Number of lines scrolled back (0 = at bottom, viewing live output)
	// Alternate screen buffer tracking for TUI detection.
	// Written on PTY/monitor goroutine, read on UI goroutine.
	isAltScreen atomic.Bool // True when application is using alternate screen buffer (nvim, vim, etc.)
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
	clipboardContent  atomic.Pointer[string]   // Written by VT callback on PTY goroutine, read on UI goroutine (OSC 52)
	ClipboardSetFunc  func(string)             // Callback to propagate clipboard to host
	NotifyFunc        func(title, body string) // Callback for guest desktop notifications (OSC 9/777/99)
	BellFunc          func()                   // Callback for guest bell (BEL)
	CwdFunc           func(cwd string)         // Callback for the shell's working directory changing (OSC 7)
	outputChan        chan outputChunk         // Channel for serializing daemon PTY output writes
	outputDone        chan struct{}            // Signal to stop output writer goroutine
	suppressCallbacks atomic.Bool              // Suppress VT emulator callbacks during state restoration (prevents race conditions)
	closed            atomic.Bool              // Set by Close() so the external outputChan sender (WriteOutputAsync) stops before teardown

	// HasNewOutput is set when new data is written to the terminal.
	// Used by MarkTerminalsWithNewContent to avoid unconditional dirty-marking.
	HasNewOutput atomic.Bool

	// coalesceSignal is the daemon renderCoalescer's own render-trigger flag.
	// outputWriter sets it after each batch; renderCoalescer consumes it at a
	// capped rate to fire PTYDataChan. It is separate from HasNewOutput so the
	// coalescer no longer consumes that flag: HasNewOutput survives for the UI
	// goroutine's MarkTerminalsWithNewContent, which does the dirty-marking.
	// This keeps window model fields (Dirty/ContentDirty/CachedContent) off the
	// background goroutine, which otherwise races the renderer and Close().
	coalesceSignal atomic.Bool

	// coalesceWake wakes the coalescer when the flag above is set, so it can
	// sleep between bursts instead of polling for a flag that is almost always
	// false. Buffered 1 with a non-blocking send: the coalescer only needs to
	// know that something arrived, not how much.
	coalesceWake chan struct{}

	// renderCostNanos is what the client's last composed frame cost, written by
	// the UI goroutine after every real compose. The coalescer paces itself
	// against it so a pane cannot keep asking for frames faster than the client
	// can draw them; see coalesceInterval.
	renderCostNanos atomic.Int64

	// queuedBytes is how much daemon output has been queued for this pane's
	// emulator and not yet written to it. WriteOutputAsync adds what it
	// managed to queue, outputWriter subtracts what it takes back off, so it
	// is the pane's backlog in bytes rather than in channel slots, which vary
	// in size by two orders of magnitude. The coalescer reads it to find out
	// whether the frame it is about to ask for has already been superseded;
	// see coalesceInterval.
	queuedBytes atomic.Int64

	// outputEpoch stamps every chunk queued for the emulator. DiscardPendingOutput
	// bumps it, and outputWriter throws away anything stamped with an older one,
	// which is how a pane that has just been restored from a daemon snapshot
	// avoids having output from before the snapshot applied on top of it.
	outputEpoch atomic.Uint64

	// streamOwnsSize is set while a daemon subscription feeds this emulator,
	// which is when the stream is what resizes it. See SetStreamOwnsSize.
	streamOwnsSize atomic.Bool

	// lastScrollbackLen is the most recent scrollback length ScrollbackLenSync
	// managed to read. It answers that call when the I/O lock is busy, so the
	// compositor never waits on a bursting pane just to size a scrollbar.
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
	// AgentHarness is the harness id the reporting source named, empty when the
	// state came from something that named none. Alert sinks pass it on.
	AgentHarness string
	// AgentStateAt is when the pane entered AgentState (Unix nanoseconds), as
	// the daemon stamped it. The rail shows the elapsed time so a pane waiting
	// on input reads differently from one that just started working.
	AgentStateAt int64
	// ForegroundCmd is the base name of what the pane is running, as the daemon
	// detected it, or empty at a shell prompt. Session surfaces label a row with
	// it, because a title is the same string for every pane in one directory.
	ForegroundCmd string

	KittyPassthroughFunc func(cmd *vt.KittyCommand, rawData []byte)
	SixelPassthroughFunc func(cmd *vt.SixelCommand, cursorX, cursorY, absLine int)

	// cmdWaitOnce ensures cmd.Wait() is only called once to prevent race conditions
	cmdWaitOnce sync.Once
	// ioWg tracks I/O goroutines for clean shutdown
	ioWg sync.WaitGroup
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
)

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
}

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
	SearchQuery     string        // Current search query
	SearchMatches   []SearchMatch // All search results
	CurrentMatch    int           // Index of current match
	CaseSensitive   bool          // Case-sensitive search
	SearchBackward  bool          // True for ? (backward), false for / (forward)
	SearchCache     SearchCache   // Cached search results (exported for copymode package)
	PendingGCount   bool          // Waiting for second 'g' in 'gg'
	LastCommandTime time.Time     // For detecting 'gg' sequence

	// Character search state (f/F/t/T commands)
	PendingCharSearch  bool // Waiting for character after f/F/t/T
	LastCharSearch     rune // Last searched character
	LastCharSearchDir  int  // 1 for forward (f/t), -1 for backward (F/T)
	LastCharSearchTill bool // true for till (t/T), false for find (f/F)

	// Count prefix (e.g., 10j means move down 10 times)
	PendingCount   int       // Accumulated count (0 means no count)
	CountStartTime time.Time // When count entry started (for timeout)

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

// shortID trims an ID for a title or a log line. IDs reach this package from
// restored session state and from the daemon wire, where nothing guarantees
// they are UUID-length, so a plain id[:8] slice would panic.
func shortID(id string) string {
	if len(id) < 8 {
		return id
	}
	return id[:8]
}

// NewWindow creates a new terminal window with the specified properties.
// It spawns a shell process, sets up PTY communication, and initializes the virtual terminal.
// Returns nil if window creation fails.
//
// command, when given, becomes the pane's process in place of the shell: argv
// exec'd directly, no shell parsing anything. The launcher runs programs this
// way because bytes typed at a shell are re-parsed by whatever shell it is,
// and the window closes when the program exits, the same way it does when a
// shell does.
func NewWindow(id, title string, x, y, width, height, z int, exitChan chan string, ptyDataChan chan struct{}, command ...string) *Window {
	if title == "" {
		title = "Terminal " + shortID(id)
	}

	// Create VT terminal with inner dimensions (accounting for borders)
	terminalWidth := max(width-2, 1)
	terminalHeight := max(height-2, 1)
	// Create terminal with scrollback buffer support
	terminal := vt.New(terminalWidth, terminalHeight)
	// Set scrollback buffer size from config (default: 10000, configurable via --scrollback-lines or config file)
	terminal.SetScrollbackMaxLines(config.ScrollbackLines)

	// Set cell size for XTWINOPS terminal size reporting
	// Using 10x20 pixels as reasonable defaults for a typical monospace font
	terminal.SetCellSize(10, 20)

	window := &Window{
		Width:              width,
		Height:             height,
		X:                  x,
		Y:                  y,
		Z:                  z,
		ID:                 id,
		Terminal:           terminal,
		PTYDataChan:        ptyDataChan,
		LastUpdate:         time.Now(),
		Dirty:              true,
		ContentDirty:       true,
		PositionDirty:      true,
		CachedContent:      "",
		CachedLayer:        nil,
		IsBeingManipulated: false,
	}
	// The cursor cache is served whenever the render loop cannot take the I/O
	// lock, including on the very first frame, so it starts on what the fresh
	// emulator reports rather than on a zero value that means a blinking block.
	window.CachedCursorStyle, window.CachedCursorSteady = terminal.CursorStyle()
	window.SetTitle(title)

	// Apply theme colors to the terminal (only if theming is enabled)
	if theme.IsEnabled() {
		terminal.SetThemeColors(
			theme.TerminalFg(),
			theme.TerminalBg(),
			theme.TerminalCursor(),
			theme.GetANSIPalette(),
		)
	} else {
		// When theming is disabled, just set nil colors to use terminal defaults
		terminal.SetThemeColors(nil, nil, nil, [16]color.Color{})
	}

	// Set up callbacks to track terminal state changes
	terminal.SetCallbacks(vt.Callbacks{
		AltScreen: func(enabled bool) {
			// Suppress callback during state restoration to prevent race conditions
			// where buffered PTY output overwrites restored state
			if !window.suppressCallbacks.Load() {
				window.SetAltScreen(enabled)
			}
		},
		Title: func(title string) {
			// Update window title from terminal escape sequence
			if title != "" {
				window.SetTitle(title)
			}
		},
		ClipboardSet: func(_ string, content string) {
			window.setClipboard(content)
			if window.ClipboardSetFunc != nil {
				window.ClipboardSetFunc(content)
			}
		},
		ClipboardQuery: func(_ string) string {
			return window.clipboard()
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
	})

	// Set up environment
	// #nosec G204 - the command is the user's own shell or a program they chose
	var cmd *exec.Cmd
	if len(command) > 0 {
		cmd = exec.Command(command[0], command[1:]...)
	} else {
		cmd = exec.Command(detectShell())
	}

	// Get cached terminal environment (detected once on first window creation)
	termType, colorTerm := getTerminalEnv()

	// Debug logging for terminal environment
	if os.Getenv("TUIOS_DEBUG_INTERNAL") == "1" {
		debugMsg := fmt.Sprintf("[%s] NewWindow TERM=%s COLORTERM=%s (envTERM=%s envCOLORTERM=%s)\n",
			time.Now().Format("15:04:05.000"), termType, colorTerm, os.Getenv("TERM"), os.Getenv("COLORTERM"))
		if f, err := os.OpenFile("/tmp/tuios-debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
			_, _ = f.WriteString(debugMsg)
			_ = f.Close()
		}
	}

	cmd.Env = append(os.Environ(),
		"TERM="+termType,
		"COLORTERM="+colorTerm,
		"TERM_PROGRAM="+guestTermProgram(), // Terminal identity guests can act on
		"TERM_PROGRAM_VERSION=0.1.0",       // Version for compatibility checking
		"TUIOS_WINDOW_ID="+id,
	)

	// Create PTY with initial size
	// xpty requires dimensions at creation time
	ptyInstance, err := xpty.NewPty(terminalWidth, terminalHeight)
	if err != nil {
		// Return nil to indicate failure - caller should handle this
		return nil
	}

	// Set up the command to use the PTY as controlling terminal
	// This is platform-specific (see pty_unix.go and pty_windows.go)
	setupPTYCommand(cmd)

	// Start the command with PTY
	// xpty handles command connection internally
	if err := ptyInstance.Start(cmd); err != nil {
		_ = ptyInstance.Close()
		return nil
	}

	// Resize PTY after process starts to ensure size is properly set
	// Some PTY implementations require the process to be running before accepting resize
	if err := ptyInstance.Resize(terminalWidth, terminalHeight); err != nil {
		// Not a critical error, continue
		_ = err
	}

	_, cancel := context.WithCancel(context.Background())

	// Update window with PTY and command info
	window.Pty = ptyInstance
	window.Cmd = cmd
	window.cancelFunc = cancel

	// Store shell's process group ID for later detection of foreground processes
	if cmd.Process != nil {
		if pgid, err := getPgid(cmd.Process.Pid); err == nil {
			window.ShellPgid = pgid
		}
	}

	// Publish the initial geometry before the PTY reader starts, so the
	// passthrough callbacks running on that goroutine always have a snapshot
	// to read instead of the live fields the update loop mutates.
	window.PublishGeometry()

	// Start I/O handling
	window.handleIOOperations()

	// Enable terminal features
	window.enableTerminalFeatures()

	// Monitor process lifecycle
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("window %s goroutine panic: %v\n%s", window.ID, r, debug.Stack())
			}
		}()

		// Wait for process to exit using sync.Once to prevent race conditions
		// with Close() which may also wait for the process.
		window.waitForCmd()

		// Mark process as exited
		window.SetProcessExited(true)

		// Clean up
		cancel()

		// Give a small delay to ensure final output is captured
		time.Sleep(config.ProcessWaitDelay)

		// Notify exit channel (ctx is already cancelled above, so don't
		// include ctx.Done  - it would randomly win the select and drop
		// the exit notification, causing the window to stay open)
		select {
		case exitChan <- id:
		default:
			// Safe to drop only here: SetProcessExited above is already true,
			// so the maintenance tick's exit sweep closes this window even if
			// nobody ever reads the signal. A daemon-backed pane has no such
			// backstop, which is why its exits are queued instead (see
			// app.OS.queueWindowExit).
		}
	}()

	return window
}

// NewDaemonWindow creates a new terminal window that uses a daemon-managed PTY.
// Unlike NewWindow, this doesn't spawn a local PTY - I/O is proxied through the daemon.
// The caller is responsible for subscribing to PTY output and handling I/O.
func NewDaemonWindow(id, title string, x, y, width, height, z int, ptyID string, ptyDataChan chan struct{}) *Window {
	if title == "" {
		title = "Terminal " + shortID(id)
	}

	// Create VT terminal with inner dimensions (accounting for borders)
	terminalWidth := max(width-2, 1)
	terminalHeight := max(height-2, 1)
	terminal := vt.New(terminalWidth, terminalHeight)
	terminal.SetScrollbackMaxLines(config.ScrollbackLines)
	terminal.SetCellSize(10, 20)

	window := &Window{
		Width:              width,
		Height:             height,
		X:                  x,
		Y:                  y,
		Z:                  z,
		ID:                 id,
		Terminal:           terminal,
		PTYDataChan:        ptyDataChan,
		LastUpdate:         time.Now(),
		Dirty:              true,
		ContentDirty:       true,
		PositionDirty:      true,
		CachedContent:      "",
		CachedLayer:        nil,
		IsBeingManipulated: false,
		PTYID:              ptyID,
		DaemonMode:         true,
		outputChan:         make(chan outputChunk, 16384), // Large buffer: kitty images can be 250+ chunks
		outputDone:         make(chan struct{}),
		coalesceWake:       make(chan struct{}, 1),
		// suppressCallbacks defaults to false (zero value)
	}
	// See NewWindow: the cursor cache must not start on a zero value.
	window.CachedCursorStyle, window.CachedCursorSteady = terminal.CursorStyle()
	window.SetTitle(title)

	// Start output writer goroutine to serialize writes
	go window.outputWriter()
	// Start render coalescer to prevent partial-frame flickering
	go window.renderCoalescer()

	// Apply theme colors to the terminal (only if theming is enabled)
	if theme.IsEnabled() {
		terminal.SetThemeColors(
			theme.TerminalFg(),
			theme.TerminalBg(),
			theme.TerminalCursor(),
			theme.GetANSIPalette(),
		)
	} else {
		terminal.SetThemeColors(nil, nil, nil, [16]color.Color{})
	}

	// Set up callbacks to track terminal state changes
	terminal.SetCallbacks(vt.Callbacks{
		AltScreen: func(enabled bool) {
			// Suppress callback during state restoration to prevent race conditions
			// where buffered PTY output overwrites restored state
			if !window.suppressCallbacks.Load() {
				window.SetAltScreen(enabled)
			}
		},
		Title: func(title string) {
			// Update window title from terminal escape sequence
			if title != "" {
				window.SetTitle(title)
			}
		},
		ClipboardSet: func(_ string, content string) {
			window.setClipboard(content)
			if window.ClipboardSetFunc != nil {
				window.ClipboardSetFunc(content)
			}
		},
		ClipboardQuery: func(_ string) string {
			return window.clipboard()
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
	})

	return window
}
