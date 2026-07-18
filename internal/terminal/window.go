// Package terminal provides terminal window management and PTY abstraction.
package terminal

import (
	"bytes"
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

	"charm.land/lipgloss/v2"
	xpty "github.com/charmbracelet/x/xpty"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// passThroughCursorStyle detects DECSCUSR (cursor style) sequences in the data
// and writes them directly to stdout to pass through to the parent terminal.
// The VT emulator absorbs these sequences, so we need to re-emit them.
// DECSCUSR format: CSI Ps SP q (ESC [ Ps SPACE q) where Ps is optional (0-6)
// LockIO/UnlockIO: exclusive lock for PTY writes (mutates cell buffer).
func (w *Window) LockIO()   { w.ioMu.Lock() }
func (w *Window) UnlockIO() { w.ioMu.Unlock() }

// RLockIO/RUnlockIO: shared lock for rendering (reads cell buffer).
func (w *Window) RLockIO()   { w.ioMu.RLock() }
func (w *Window) RUnlockIO() { w.ioMu.RUnlock() }

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

// CursorStyle returns the current cursor style.
func (w *Window) CursorStyle() vt.CursorStyle { return vt.CursorStyle(w.cursorStyle.Load()) }

// SetCursorStyle records the current cursor style.
func (w *Window) SetCursorStyle(style vt.CursorStyle) { w.cursorStyle.Store(int32(style)) }

// CursorBlink reports whether the cursor should blink.
func (w *Window) CursorBlink() bool { return w.cursorBlink.Load() }

// SetCursorBlink records whether the cursor should blink.
func (w *Window) SetCursorBlink(blink bool) { w.cursorBlink.Store(blink) }

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

func passThroughCursorStyle(data []byte) {
	// Fast path: DECSCUSR sequences contain " q" (space-q). If neither
	// byte is present, skip the scan entirely. This avoids O(n) work on
	// the vast majority of PTY output chunks at 300+ fps.
	if !bytes.Contains(data, []byte(" q")) {
		return
	}
	idx := 0
	for idx < len(data) {
		escIdx := bytes.Index(data[idx:], []byte("\x1b["))
		if escIdx == -1 {
			break
		}
		escIdx += idx
		if escIdx+4 > len(data) {
			break
		}
		numEnd := escIdx + 2
		for numEnd < len(data) && data[numEnd] >= '0' && data[numEnd] <= '9' {
			numEnd++
		}
		if numEnd+1 < len(data) && data[numEnd] == ' ' && data[numEnd+1] == 'q' {
			_, _ = os.Stdout.Write(data[escIdx : numEnd+2])
			idx = numEnd + 2
			continue
		}
		idx = escIdx + 1
	}
}

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
	title                  atomic.Pointer[string] // Written on PTY/monitor goroutine, read on UI goroutine
	CustomName             string                 // User-defined window name
	Width                  int
	Height                 int
	X                      int
	Y                      int
	Z                      int
	ID                     string
	Terminal               *vt.Emulator
	Pty                    xpty.Pty
	Cmd                    *exec.Cmd
	ShellPgid              int // Process group ID of the shell
	LastUpdate             time.Time
	Dirty                  bool
	ContentDirty           bool
	PositionDirty          bool
	CachedContent          string
	CachedLayer            *lipgloss.Layer
	LastTerminalSeq        int
	IsBeingManipulated     bool               // True when being dragged or resized
	UpdateCounter          int                // Counter for throttling background updates
	cancelFunc             context.CancelFunc // For graceful goroutine cleanup
	ioMu                   sync.RWMutex       // Protect I/O operations
	Minimized              bool               // True when window is minimized to dock
	Minimizing             bool               // True when window is being minimized (animation playing)
	MinimizeHighlightUntil time.Time          // Highlight dock tab until this time
	MinimizeOrder          int64              // Unix nano timestamp when minimized (for dock ordering)
	PreMinimizeX           int                // Store position before minimizing
	PreMinimizeY           int                // Store position before minimizing
	PreMinimizeWidth       int                // Store size before minimizing
	PreMinimizeHeight      int                // Store size before minimizing
	Workspace              int                // Workspace this window belongs to
	Zoomed                 bool               // True when window is zoomed (fullscreen)
	PreZoomX               int                // Store position before zooming
	PreZoomY               int                // Store position before zooming
	PreZoomWidth           int                // Store size before zooming
	PreZoomHeight          int                // Store size before zooming
	SelectionStart         struct{ X, Y int } // Selection start position
	SelectionEnd           struct{ X, Y int } // Selection end position
	IsSelecting            bool               // True when selecting text
	SelectedText           string             // Currently selected text
	SelectionCursor        struct{ X, Y int } // Current cursor position in selection mode
	processExited          atomic.Bool        // Written on PTY/monitor goroutine, read on UI goroutine
	// Enhanced text selection support
	SelectionMode int // 0 = character, 1 = word, 2 = line
	LastClickTime time.Time
	LastClickX    int
	LastClickY    int
	ClickCount    int // Track number of consecutive clicks for word/line selection
	// Scrollback mode support
	ScrollbackMode   bool // True when viewing scrollback history
	ScrollbackOffset int  // Number of lines scrolled back (0 = at bottom, viewing live output)
	// Alternate screen buffer tracking for TUI detection.
	// Written on PTY/monitor goroutine, read on UI goroutine.
	isAltScreen atomic.Bool // True when application is using alternate screen buffer (nvim, vim, etc.)
	// Floating pane support
	IsFloating bool // True when window is floating (not in BSP tiling)
	IsPinned   bool // True when floating pane persists across workspace switches
	// Cursor style tracking for passthrough to parent terminal.
	// Written by the VT callback on the PTY goroutine, read on the UI goroutine.
	cursorStyle atomic.Int32 // Current cursor style (block, underline, bar)
	cursorBlink atomic.Bool  // Whether cursor should blink
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
	outputChan        chan []byte              // Channel for serializing daemon PTY output writes
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

	// PTYDataChan is a shared channel (buffered 1) that PTY readers signal
	// to trigger rendering. Non-blocking send coalesces rapid updates.
	PTYDataChan chan struct{}

	Tiled bool // True when window is in shared-border tiling mode (no individual borders)

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
}

// NewWindow creates a new terminal window with the specified properties.
// It spawns a shell process, sets up PTY communication, and initializes the virtual terminal.
// Returns nil if window creation fails.
func NewWindow(id, title string, x, y, width, height, z int, exitChan chan string, ptyDataChan chan struct{}) *Window {
	if title == "" {
		title = "Terminal " + id[:8]
	}

	// Create VT terminal with inner dimensions (accounting for borders)
	terminalWidth := max(width-2, 1)
	terminalHeight := max(height-2, 1)
	// Create terminal with scrollback buffer support
	terminal := vt.NewEmulator(terminalWidth, terminalHeight)
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
		CursorStyle: func(style vt.CursorStyle, steady bool) {
			// Note: the callback receives "steady" value (true = NOT blinking)
			// despite the parameter being named "blink" in the Callbacks struct
			window.SetCursorStyle(style)
			window.SetCursorBlink(!steady) // Invert: steady=false means blinking=true
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
	})

	// Detect shell
	shell := detectShell()

	// Set up environment
	// #nosec G204 - shell is intentionally user-controlled for terminal functionality
	cmd := exec.Command(shell)

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
		"TERM_PROGRAM=TUIOS",         // Identify as TUIOS terminal emulator
		"TERM_PROGRAM_VERSION=0.1.0", // Version for compatibility checking
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
			// Channel full, exit silently
		}
	}()

	return window
}

// NewDaemonWindow creates a new terminal window that uses a daemon-managed PTY.
// Unlike NewWindow, this doesn't spawn a local PTY - I/O is proxied through the daemon.
// The caller is responsible for subscribing to PTY output and handling I/O.
func NewDaemonWindow(id, title string, x, y, width, height, z int, ptyID string, ptyDataChan chan struct{}) *Window {
	if title == "" {
		title = "Terminal " + id[:8]
	}

	// Create VT terminal with inner dimensions (accounting for borders)
	terminalWidth := max(width-2, 1)
	terminalHeight := max(height-2, 1)
	terminal := vt.NewEmulator(terminalWidth, terminalHeight)
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
		outputChan:         make(chan []byte, 16384), // Large buffer: kitty images can be 250+ chunks
		outputDone:         make(chan struct{}),
		// suppressCallbacks defaults to false (zero value)
	}
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
		CursorStyle: func(style vt.CursorStyle, steady bool) {
			// Note: the callback receives "steady" value (true = NOT blinking)
			// despite the parameter being named "blink" in the Callbacks struct
			window.SetCursorStyle(style)
			window.SetCursorBlink(!steady) // Invert: steady=false means blinking=true
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
	})

	return window
}
