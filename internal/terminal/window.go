// Package terminal provides terminal window management and PTY abstraction.
package terminal

import (
	"bytes"
	"context"
	"fmt"
	"image/color"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	uv "github.com/charmbracelet/ultraviolet"
	xpty "github.com/charmbracelet/x/xpty"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/pool"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// passThroughCursorStyle detects DECSCUSR (cursor style) sequences in the data
// and writes them directly to stdout to pass through to the parent terminal.
// The VT emulator absorbs these sequences, so we need to re-emit them.
// DECSCUSR format: CSI Ps SP q (ESC [ Ps SPACE q) where Ps is optional (0-6)
func passThroughCursorStyle(data []byte) {
	// Look for DECSCUSR pattern: \x1b[N q where N is 0-6 (or no digit)
	idx := 0
	for idx < len(data) {
		// Find ESC [
		escIdx := bytes.Index(data[idx:], []byte("\x1b["))
		if escIdx == -1 {
			break
		}
		escIdx += idx

		// Check if this could be DECSCUSR
		// Need at least ESC [ SP q (4 bytes from escIdx)
		if escIdx+4 > len(data) {
			idx = escIdx + 1
			continue
		}

		// Check for pattern: optional digit(s) followed by space and 'q'
		numEnd := escIdx + 2
		for numEnd < len(data) && data[numEnd] >= '0' && data[numEnd] <= '9' {
			numEnd++
		}

		// Check if followed by " q" (space then q)
		if numEnd+1 < len(data) && data[numEnd] == ' ' && data[numEnd+1] == 'q' {
			// Found DECSCUSR sequence - write it to stdout
			seq := data[escIdx : numEnd+2]
			_, _ = os.Stdout.Write(seq)
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
	Title                  string
	CustomName             string // User-defined window name
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
	SelectionStart         struct{ X, Y int } // Selection start position
	SelectionEnd           struct{ X, Y int } // Selection end position
	IsSelecting            bool               // True when selecting text
	SelectedText           string             // Currently selected text
	SelectionCursor        struct{ X, Y int } // Current cursor position in selection mode
	ProcessExited          bool               // True when process has exited
	// Enhanced text selection support
	SelectionMode int // 0 = character, 1 = word, 2 = line
	LastClickTime time.Time
	LastClickX    int
	LastClickY    int
	ClickCount    int // Track number of consecutive clicks for word/line selection
	// Scrollback mode support
	ScrollbackMode   bool // True when viewing scrollback history
	ScrollbackOffset int  // Number of lines scrolled back (0 = at bottom, viewing live output)
	// Alternate screen buffer tracking for TUI detection
	IsAltScreen bool // True when application is using alternate screen buffer (nvim, vim, etc.)
	// Cursor style tracking for passthrough to parent terminal
	CursorStyle vt.CursorStyle // Current cursor style (block, underline, bar)
	CursorBlink bool           // Whether cursor should blink
	// Cell dimensions in pixels (for TIOCGWINSZ pixel reporting to child processes)
	CellPixelWidth  int
	CellPixelHeight int
	// Vim-style copy mode
	CopyMode *CopyMode // Copy mode state (nil when not active)
	// Daemon session support
	PTYID             string               // ID of daemon-managed PTY (empty for local PTYs)
	DaemonMode        bool                 // True when PTY is managed by daemon
	DaemonWriteFunc   func([]byte) error   // Callback for sending input to daemon PTY
	DaemonResizeFunc  func(w, h int) error // Callback for resizing daemon PTY
	DaemonCloseFunc   func()               // Callback when window is closed (to notify daemon)
	OnProcessExit     func()               // Callback when PTY process exits (to close window)
	outputChan        chan []byte          // Channel for serializing daemon PTY output writes
	outputDone        chan struct{}        // Signal to stop output writer goroutine
	suppressCallbacks atomic.Bool          // Suppress VT emulator callbacks during state restoration (prevents race conditions)

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
func NewWindow(id, title string, x, y, width, height, z int, exitChan chan string) *Window {
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
		Title:              title,
		Width:              width,
		Height:             height,
		X:                  x,
		Y:                  y,
		Z:                  z,
		ID:                 id,
		Terminal:           terminal,
		LastUpdate:         time.Now(),
		Dirty:              true,
		ContentDirty:       true,
		PositionDirty:      true,
		CachedContent:      "",
		CachedLayer:        nil,
		IsBeingManipulated: false,
		IsAltScreen:        false,
	}

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
				window.IsAltScreen = enabled
			}
		},
		CursorStyle: func(style vt.CursorStyle, steady bool) {
			// Note: the callback receives "steady" value (true = NOT blinking)
			// despite the parameter being named "blink" in the Callbacks struct
			window.CursorStyle = style
			window.CursorBlink = !steady // Invert: steady=false means blinking=true
		},
		Title: func(title string) {
			// Update window title from terminal escape sequence
			if title != "" {
				window.Title = title
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

	ctx, cancel := context.WithCancel(context.Background())

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
				// Silently recover from panics during process monitoring
				_ = r // Explicitly ignore the recovered value
			}
		}()

		// Wait for process to exit using sync.Once to prevent race conditions
		// with Close() which may also wait for the process.
		window.waitForCmd()

		// Mark process as exited
		window.ProcessExited = true

		// Clean up
		cancel()

		// Give a small delay to ensure final output is captured
		time.Sleep(config.ProcessWaitDelay)

		// Notify exit channel
		select {
		case exitChan <- id:
		case <-ctx.Done():
			// Context cancelled, exit silently
		default:
			// Channel full or closed, exit silently
		}
	}()

	return window
}

// NewDaemonWindow creates a new terminal window that uses a daemon-managed PTY.
// Unlike NewWindow, this doesn't spawn a local PTY - I/O is proxied through the daemon.
// The caller is responsible for subscribing to PTY output and handling I/O.
func NewDaemonWindow(id, title string, x, y, width, height, z int, ptyID string) *Window {
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
		Title:              title,
		Width:              width,
		Height:             height,
		X:                  x,
		Y:                  y,
		Z:                  z,
		ID:                 id,
		Terminal:           terminal,
		LastUpdate:         time.Now(),
		Dirty:              true,
		ContentDirty:       true,
		PositionDirty:      true,
		CachedContent:      "",
		CachedLayer:        nil,
		IsBeingManipulated: false,
		IsAltScreen:        false,
		PTYID:              ptyID,
		DaemonMode:         true,
		outputChan:         make(chan []byte, 1000), // Buffered channel for output
		outputDone:         make(chan struct{}),
		// suppressCallbacks defaults to false (zero value)
	}

	// Start output writer goroutine to serialize writes
	go window.outputWriter()

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
				window.IsAltScreen = enabled
			}
		},
		CursorStyle: func(style vt.CursorStyle, steady bool) {
			// Note: the callback receives "steady" value (true = NOT blinking)
			// despite the parameter being named "blink" in the Callbacks struct
			window.CursorStyle = style
			window.CursorBlink = !steady // Invert: steady=false means blinking=true
		},
		Title: func(title string) {
			// Update window title from terminal escape sequence
			if title != "" {
				window.Title = title
			}
		},
	})

	return window
}

// outputWriter is a goroutine that serializes writes to the terminal emulator.
// This ensures output is written in order for daemon mode windows.
func (w *Window) outputWriter() {
	// Nil channel check - if channels aren't initialized, exit
	if w.outputDone == nil || w.outputChan == nil {
		return
	}

	for {
		select {
		case <-w.outputDone:
			return
		case data, ok := <-w.outputChan:
			if !ok {
				// Channel closed
				return
			}
			if w.Terminal != nil {
				w.ioMu.Lock()
				_, _ = w.Terminal.Write(data)
				w.ioMu.Unlock()
				w.MarkContentDirty()
			}
		}
	}
}

// StartDaemonResponseReader starts a goroutine to read and DRAIN responses from
// the terminal emulator. We don't forward these to the PTY because:
//  1. Responses were appearing as visible escape sequences in the output
//  2. Applications in daemon mode receive queries from the daemon's VT emulator
//     and don't need responses from client emulators
//
// This must be called after the Terminal is set up.
func (w *Window) StartDaemonResponseReader() {
	if !w.DaemonMode || w.Terminal == nil {
		return
	}

	go func() {
		buf := make([]byte, 4096)
		for {
			// Terminal.Read() blocks, so we can't use select here.
			// The goroutine will exit when Terminal is closed (returns error).
			_, err := w.Terminal.Read(buf)
			if err != nil {
				return
			}
			// Drain responses - don't send to PTY to avoid escape sequence leaks
		}
	}()
}

// WriteOutput writes output data to the terminal emulator.
// Used in daemon mode to process PTY output received from the daemon.
func (w *Window) WriteOutput(data []byte) {
	if w.Terminal != nil {
		w.ioMu.Lock()
		_, _ = w.Terminal.Write(data)
		w.ioMu.Unlock()
		w.MarkContentDirty()
	}
}

// WriteOutputAsync writes output data to the terminal emulator without blocking.
// Used in daemon mode to process PTY output received from the daemon.
// Data is queued to a channel and written in order by the outputWriter goroutine.
func (w *Window) WriteOutputAsync(data []byte) {
	if w.Terminal == nil || w.outputChan == nil {
		return
	}
	// Copy data since the caller's buffer may be reused
	dataCopy := make([]byte, len(data))
	copy(dataCopy, data)

	// Queue to channel - non-blocking with buffered channel
	select {
	case w.outputChan <- dataCopy:
		// Successfully queued
	default:
		// Channel full - drop data (shouldn't happen with large buffer)
	}
}

// UpdateThemeColors updates the terminal colors when the theme changes
func (w *Window) UpdateThemeColors() {
	if w.Terminal != nil {
		if theme.IsEnabled() {
			w.Terminal.SetThemeColors(
				theme.TerminalFg(),
				theme.TerminalBg(),
				theme.TerminalCursor(),
				theme.GetANSIPalette(),
			)
		} else {
			w.Terminal.SetThemeColors(nil, nil, nil, [16]color.Color{})
		}
		// Mark the window as dirty to trigger a redraw
		w.Dirty = true
		w.ContentDirty = true
	}
}

func detectShell() string {
	// Check user configuration first
	if cfg, err := config.LoadUserConfig(); err == nil && cfg.Appearance.PreferredShell != "" {
		preferredShell := cfg.Appearance.PreferredShell

		// just do a check in case
		if runtime.GOOS == "windows" && !strings.HasSuffix(strings.ToLower(preferredShell), ".exe") {
			preferredShell += ".exe"
		}

		shellExists := false
		if runtime.GOOS == "windows" {
			_, err = exec.LookPath(preferredShell)
			shellExists = err == nil
		} else {
			_, err = os.Stat(preferredShell)
			shellExists = err == nil
		}

		if shellExists {
			return preferredShell
		}
		fmt.Fprintf(os.Stderr, "Warning: Configured shell '%s' not found. Falling back to defaults.\n", preferredShell)
	}

	// Check environment variable
	if shell := os.Getenv("SHELL"); shell != "" {
		return shell
	}

	// Check if we're on Windows
	if runtime.GOOS == "windows" {
		// Check for PowerShell or CMD
		shells := []string{
			"powershell.exe",
			"pwsh.exe", // PowerShell Core/7+
			"cmd.exe",
		}
		for _, shell := range shells {
			if _, err := exec.LookPath(shell); err == nil {
				return shell
			}
		}
		// Windows fallback
		return "cmd.exe"
	}

	// Unix/Linux/macOS shells
	shells := []string{"/bin/bash", "/bin/zsh", "/bin/fish", "/bin/sh"}
	for _, shell := range shells {
		if _, err := os.Stat(shell); err == nil {
			return shell
		}
	}
	// Unix fallback
	return "/bin/sh"
}

// getTerminalEnv returns TERM and COLORTERM values for the current environment.
// For local sessions, this is cached after first detection.
// The environment is detected from os.Environ() which includes SSH forwarded vars.
func getTerminalEnv() (termType, colorTerm string) {
	// Use sync.Once to cache local terminal detection
	// This runs once per process lifetime for efficiency
	localEnvOnce.Do(func() {
		// First check if TERM/COLORTERM are already set in the environment
		// This handles the case where tuios-web sets them explicitly because
		// os.Stdout is not a TTY in web mode
		envTerm := os.Getenv("TERM")
		envColorTerm := os.Getenv("COLORTERM")

		// If COLORTERM=truecolor is set, trust the environment
		// This is the case for web sessions where we explicitly set these
		if envColorTerm == "truecolor" && envTerm != "" && envTerm != "dumb" {
			localTermType = envTerm
			localColorTerm = envColorTerm
			return
		}

		// Detect terminal capabilities using colorprofile (from charm)
		// This handles TERM, COLORTERM, NO_COLOR, CLICOLOR, terminfo, and tmux detection
		// For SSH sessions, os.Environ() will include the SSH client's environment
		profile := colorprofile.Detect(os.Stdout, os.Environ())
		localTermType, localColorTerm = profileToEnv(profile)
	})
	return localTermType, localColorTerm
}

// profileToEnv converts a colorprofile.Profile to TERM and COLORTERM environment variables.
// Returns (termType, colorTerm) where colorTerm may be empty string.
func profileToEnv(profile colorprofile.Profile) (termType, colorTerm string) {
	// Get parent TERM for preserving specific terminal types
	parentTerm := os.Getenv("TERM")

	switch profile {
	case colorprofile.TrueColor:
		// Prefer parent TERM, fallback to xterm-256color
		// Note: We support XTWINOPS but xterm-256color terminfo doesn't advertise it
		// Applications must query the terminal directly (which works via our CSI 't' handler)
		if parentTerm != "" {
			termType = parentTerm
		} else {
			termType = "xterm-256color"
		}
		colorTerm = "truecolor"

	case colorprofile.ANSI256:
		// 256 color support
		if parentTerm != "" && strings.Contains(parentTerm, "256color") {
			termType = parentTerm
		} else if strings.HasPrefix(parentTerm, "screen") {
			termType = "screen-256color"
		} else if strings.HasPrefix(parentTerm, "tmux") {
			termType = "tmux-256color"
		} else {
			termType = "xterm-256color"
		}
		colorTerm = "" // Don't set COLORTERM for 256 color

	case colorprofile.ANSI:
		// Basic 16 color support
		if parentTerm != "" && parentTerm != "dumb" {
			termType = parentTerm
		} else {
			termType = "xterm"
		}
		colorTerm = ""

	case colorprofile.Ascii, colorprofile.NoTTY:
		// No color support or not a TTY
		termType = "dumb"
		colorTerm = ""

	default:
		// Fallback to sensible default
		termType = "xterm-256color"
		colorTerm = ""
	}

	return termType, colorTerm
}

// enableTerminalFeatures enables advanced terminal features
func (w *Window) enableTerminalFeatures() {
	if w.Pty == nil {
		return
	}

	// Bracketed paste mode is handled by wrapping paste content with escape sequences
	// when pasting (see input.go handleClipboardPaste). We don't need to enable it
	// via the PTY as that sends the sequence to the shell's stdin, which can cause
	// the escape codes to be echoed back and appear as garbage in the terminal.
	// The shell/application running in the PTY will handle bracketed paste mode
	// if it supports it, based on receiving the wrapped paste content.

	// Don't enable mouse modes automatically - let applications request them
	// Applications like vim, less, htop will enable mouse support themselves
	// by sending the appropriate escape sequences
}

// disableTerminalFeatures disables advanced terminal features before closing
func (w *Window) disableTerminalFeatures() {
	if w.Pty == nil {
		return
	}

	// No terminal features to explicitly disable
	// Bracketed paste is handled at the application level
	// Mouse tracking is managed by applications themselves
}

func (w *Window) handleIOOperations() {
	ctx, cancel := context.WithCancel(context.Background())
	w.cancelFunc = cancel

	// PTY to Terminal copy (output from shell) - with proper context handling
	w.ioWg.Add(1)
	go func() {
		defer w.ioWg.Done()
		defer func() {
			if r := recover(); r != nil {
				// Silently recover from panics during PTY read
				_ = r // Explicitly ignore the recovered value
			}
		}()

		// Get buffer from pool for better memory management
		bufPtr := pool.GetByteSlice()
		buf := *bufPtr
		defer pool.PutByteSlice(bufPtr)
		for {
			select {
			case <-ctx.Done():
				// Context cancelled, exit gracefully
				return
			default:
				// Set a reasonable timeout for read operations
				if w.Pty == nil {
					return
				}

				n, err := w.Pty.Read(buf)
				if err != nil {
					if err != io.EOF && !strings.Contains(err.Error(), "file already closed") &&
						!strings.Contains(err.Error(), "input/output error") {
						// Log unexpected errors for debugging
						_ = err
					}
					return
				}
				if n > 0 {
					// Debug: Log all data from PTY (applications sending queries)
					if os.Getenv("TUIOS_DEBUG_INTERNAL") == "1" {
						if len(buf[:n]) >= 2 && buf[0] == '\x1b' {
							debugMsg := fmt.Sprintf("[%s] PTY->Terminal query: %q (hex: % x)\n",
								time.Now().Format("15:04:05.000"), string(buf[:n]), buf[:n])
							if f, err := os.OpenFile("/tmp/tuios-debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
								_, _ = f.WriteString(debugMsg)
								_ = f.Close()
							}
						}
					}

					// Pass through cursor style sequences to parent terminal
					// The VT emulator absorbs DECSCUSR, so we re-emit them
					passThroughCursorStyle(buf[:n])

					// Write to terminal with mutex protection
					w.ioMu.RLock()
					if w.Terminal != nil {
						_, _ = w.Terminal.Write(buf[:n]) // Ignore write errors in read loop
					}
					w.ioMu.RUnlock()
				}
			}
		}
	}()

	// Terminal to PTY copy (input to shell) - with proper context handling
	w.ioWg.Add(1)
	go func() {
		defer w.ioWg.Done()
		defer func() {
			if r := recover(); r != nil {
				// Silently recover from panics during terminal read
				_ = r // Explicitly ignore the recovered value
			}
		}()

		// Use a smaller buffer for terminal-to-PTY operations
		buf := make([]byte, 4096)
		for {
			select {
			case <-ctx.Done():
				// Context cancelled, exit gracefully
				return
			default:
				// Set a reasonable timeout for read operations
				// Use lock to synchronize with Close() which may set w.Terminal = nil
				w.ioMu.RLock()
				terminal := w.Terminal
				w.ioMu.RUnlock()

				if terminal == nil {
					return
				}

				n, err := terminal.Read(buf)
				if err != nil {
					if err != io.EOF && !strings.Contains(err.Error(), "file already closed") &&
						!strings.Contains(err.Error(), "input/output error") {
						// Log unexpected errors for debugging
						_ = err
					}
					return
				}
				if n > 0 {
					data := buf[:n]

					// Debug: Log all escape sequences from terminal when debug mode is enabled
					if os.Getenv("TUIOS_DEBUG_INTERNAL") == "1" {
						if len(data) >= 2 && data[0] == '\x1b' {
							debugMsg := fmt.Sprintf("[%s] Terminal->PTY escape seq: %q (hex: % x)\n",
								time.Now().Format("15:04:05.000"), string(data), data)
							if f, err := os.OpenFile("/tmp/tuios-debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
								_, _ = f.WriteString(debugMsg)
								_ = f.Close()
							}
						}
					}

					// Fix incorrect CPR responses from VT library for nushell compatibility
					// The VT library responds to ESC[6n queries but returns stale/incorrect cursor positions
					// This causes nushell to incorrectly clear the screen thinking it's at the wrong position
					// We detect CPR responses (ESC[{row};{col}R) and replace with actual cursor position
					if len(data) >= 6 && data[0] == '\x1b' && data[1] == '[' && data[len(data)-1] == 'R' {
						// This looks like a CPR response, check if it contains semicolon
						if bytes.Contains(data, []byte(";")) {
							w.ioMu.RLock()
							if w.Terminal != nil {
								pos := w.Terminal.CursorPosition()
								// Get the actual current cursor position (1-indexed for terminal protocol)
								actualY := pos.Y + 1
								actualX := pos.X + 1
								// Replace with corrected cursor position
								data = fmt.Appendf(nil, "\x1b[%d;%dR", actualY, actualX)
							}
							w.ioMu.RUnlock()
						}
					}

					// Debug: Log XTWINOPS responses when debug mode is enabled
					if os.Getenv("TUIOS_DEBUG_INTERNAL") == "1" {
						if len(data) >= 6 && data[0] == '\x1b' && data[1] == '[' && data[len(data)-1] == 't' {
							// This looks like an XTWINOPS response
							debugMsg := fmt.Sprintf("[%s] XTWINOPS response to PTY: %q (hex: % x)\n",
								time.Now().Format("15:04:05.000"), string(data), data)
							// Append to debug log file
							if f, err := os.OpenFile("/tmp/tuios-debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
								_, _ = f.WriteString(debugMsg)
								_ = f.Close()
							}
						}
					}

					// Write to PTY
					w.ioMu.RLock()
					if w.Pty != nil {
						if _, err := w.Pty.Write(data); err != nil {
							// Ignore write errors during I/O operations
							_ = err
						}
					}
					w.ioMu.RUnlock()
				}
			}
		}
	}()
}

// Resize resizes the window and its terminal.
func (w *Window) Resize(width, height int) {
	if w.Terminal == nil {
		return
	}

	termWidth := max(width-2, 1)
	termHeight := max(height-2, 1)

	// Check if size actually changed
	sizeChanged := w.Width != width || w.Height != height

	w.Terminal.Resize(termWidth, termHeight)
	if w.Pty != nil {
		if err := w.Pty.Resize(termWidth, termHeight); err != nil {
			_ = err
		}
		if w.CellPixelWidth > 0 && w.CellPixelHeight > 0 {
			xpixel := termWidth * w.CellPixelWidth
			ypixel := termHeight * w.CellPixelHeight
			_ = w.SetPtyPixelSize(termWidth, termHeight, xpixel, ypixel)
		}
	} else if w.DaemonMode && w.DaemonResizeFunc != nil {
		// In daemon mode, use the resize callback to notify the daemon
		if err := w.DaemonResizeFunc(termWidth, termHeight); err != nil {
			_ = err // Acknowledge error but don't break functionality
		}
	}
	w.Width = width
	w.Height = height

	// Mark both position and content dirty for resize operations
	w.MarkPositionDirty()
	w.MarkContentDirty()

	// Trigger redraw if size changed to force applications to adapt
	if sizeChanged && w.Pty != nil {
		w.TriggerRedraw()
	}
}

// ResizeVisual updates the window dimensions without triggering PTY resize.
// This is used during mouse drag to provide immediate visual feedback while
// deferring expensive PTY resize operations until the drag completes.
// The terminal emulator dimensions are updated to ensure correct rendering.
func (w *Window) ResizeVisual(width, height int) {
	w.Width = width
	w.Height = height

	// Critical: Update terminal emulator dimensions so rendering uses correct bounds.
	// This prevents the "stuck" height and dimension mismatch issues during drag.
	// PTY resize is still deferred until mouse release (via pending resizes).
	if w.Terminal != nil {
		termWidth := max(width-2, 1)
		termHeight := max(height-2, 1)
		w.Terminal.Resize(termWidth, termHeight)
	}

	w.MarkPositionDirty()
	// Note: NOT marking ContentDirty to preserve cached content during drag
	// This improves responsiveness during resize operations
}

// SetCellPixelDimensions sets the cell pixel dimensions for the window.
// This is used to report accurate pixel dimensions to child processes via TIOCGWINSZ.
// Call this after window creation with the host terminal's cell dimensions.
func (w *Window) SetCellPixelDimensions(cellWidth, cellHeight int) {
	w.CellPixelWidth = cellWidth
	w.CellPixelHeight = cellHeight

	w.Terminal.SetCellSize(cellWidth, cellHeight)

	if w.Pty != nil && cellWidth > 0 && cellHeight > 0 {
		termWidth := max(w.Width-2, 1)
		termHeight := max(w.Height-2, 1)
		xpixel := termWidth * cellWidth
		ypixel := termHeight * cellHeight
		_ = w.SetPtyPixelSize(termWidth, termHeight, xpixel, ypixel)
	}
}

// waitForCmd waits for the command to exit, ensuring Wait() is only called once.
// This prevents race conditions when both the process monitor goroutine and Close()
// try to wait for the process.
func (w *Window) waitForCmd() {
	if w == nil || w.Cmd == nil {
		return
	}
	w.cmdWaitOnce.Do(func() {
		_ = w.Cmd.Wait() // Best effort, ignore error
	})
}

// Close closes the window and cleans up resources.
func (w *Window) Close() {
	// Nil safety check
	if w == nil {
		return
	}

	// Disable terminal features before closing
	w.disableTerminalFeatures()

	// Stop daemon output writer goroutine if running
	if w.outputDone != nil {
		close(w.outputDone)
		w.outputDone = nil
	}
	// Close output channel to unblock any pending writes
	if w.outputChan != nil {
		close(w.outputChan)
		w.outputChan = nil
	}

	// Cancel all goroutines first
	if w.cancelFunc != nil {
		w.cancelFunc()
		w.cancelFunc = nil
	}

	// Close PTY and Terminal to unblock I/O goroutines
	// Must close both because:
	// - PTY close unblocks the PTY->Terminal goroutine
	// - Terminal close unblocks the Terminal->PTY goroutine (reads from emulator response pipe)
	w.ioMu.Lock()
	if w.Pty != nil {
		_ = w.Pty.Close()
		w.Pty = nil
	}
	if w.Terminal != nil {
		_ = w.Terminal.Close()
		w.Terminal = nil
	}
	w.ioMu.Unlock()

	// Wait briefly for I/O goroutines (they should exit fast after PTY/Terminal close)
	done := make(chan struct{})
	go func() {
		w.ioWg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Millisecond):
	}

	// Kill the process
	if w.Cmd != nil && w.Cmd.Process != nil {
		_ = w.Cmd.Process.Kill()
		w.waitForCmd()
		w.Cmd = nil
	}

	// Clear caches to free memory
	w.CachedContent = ""
	w.CachedLayer = nil
	w.SelectedText = ""

	// Clear copy mode to free memory
	if w.CopyMode != nil {
		w.CopyMode.SearchMatches = nil
		w.CopyMode.SearchCache.Matches = nil
		w.CopyMode = nil
	}
}

// SendInput sends input to the window's terminal with enhanced error handling.
func (w *Window) SendInput(input []byte) error {
	if w == nil {
		return fmt.Errorf("window is nil")
	}

	if len(input) == 0 {
		return nil // Nothing to send
	}

	// In daemon mode, use the callback to send input to daemon PTY
	if w.DaemonMode {
		if w.DaemonWriteFunc == nil {
			// Debug: this might be why input fails
			if f, _ := os.OpenFile("/tmp/tuios-input-debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); f != nil {
				_, _ = fmt.Fprintf(f, "[%s] SendInput: DaemonWriteFunc is nil! PTYID=%s\n",
					time.Now().Format("15:04:05.000"), w.PTYID)
				_ = f.Close()
			}
			return fmt.Errorf("daemon write function not set")
		}
		return w.DaemonWriteFunc(input)
	}

	// Local mode - write directly to PTY
	w.ioMu.RLock()
	defer w.ioMu.RUnlock()

	if w.Pty == nil {
		return fmt.Errorf("no PTY available")
	}

	n, err := w.Pty.Write(input)
	if err != nil {
		return fmt.Errorf("failed to write to PTY: %w", err)
	}

	if n != len(input) {
		return fmt.Errorf("partial write to PTY: wrote %d of %d bytes", n, len(input))
	}

	// Only mark as dirty - don't clear cache here for better input performance
	// Cache will be invalidated during render if content actually changed
	w.Dirty = true
	w.ContentDirty = true

	return nil
}

// MarkPositionDirty marks the window position as dirty.
func (w *Window) MarkPositionDirty() {
	w.Dirty = true
	w.PositionDirty = true
	// Position changes invalidate the cached layer but NOT the content cache
	// This allows us to keep the expensive terminal content rendering
	w.CachedLayer = nil
	// DON'T clear w.CachedContent here - keep it for performance
}

// MarkContentDirty marks the window content as dirty.
func (w *Window) MarkContentDirty() {
	w.Dirty = true
	w.ContentDirty = true
	// Content changes invalidate both cached content and layer
	w.CachedContent = ""
	w.CachedLayer = nil
}

// ClearDirtyFlags clears all dirty flags.
func (w *Window) ClearDirtyFlags() {
	w.Dirty = false
	w.ContentDirty = false
	w.PositionDirty = false
}

// InvalidateCache invalidates the cached content.
func (w *Window) InvalidateCache() {
	w.CachedLayer = nil
	w.CachedContent = ""
}

// ScrollbackLen returns the number of lines in the scrollback buffer.
func (w *Window) ScrollbackLen() int {
	if w.Terminal == nil {
		return 0
	}
	return w.Terminal.ScrollbackLen()
}

// ScrollbackLine returns a line from the scrollback buffer at the given index.
// Index 0 is the oldest line. Returns nil if index is out of bounds.
func (w *Window) ScrollbackLine(index int) uv.Line {
	if w.Terminal == nil {
		return nil
	}
	return w.Terminal.ScrollbackLine(index)
}

// ClearScrollback clears the scrollback buffer.
func (w *Window) ClearScrollback() {
	if w.Terminal != nil {
		w.Terminal.ClearScrollback()
	}
}

// SetScrollbackMaxLines sets the maximum number of lines for the scrollback buffer.
func (w *Window) SetScrollbackMaxLines(maxLines int) {
	if w.Terminal != nil {
		w.Terminal.SetScrollbackMaxLines(maxLines)
	}
}

// EnterScrollbackMode enters scrollback viewing mode.
func (w *Window) EnterScrollbackMode() {
	w.ScrollbackMode = true
	w.ScrollbackOffset = 0 // Start at the bottom (most recent scrollback)
	w.InvalidateCache()
}

// ExitScrollbackMode exits scrollback viewing mode.
func (w *Window) ExitScrollbackMode() {
	w.ScrollbackMode = false
	w.ScrollbackOffset = 0
	w.InvalidateCache()
}

// ScrollUp scrolls up in the scrollback buffer.
func (w *Window) ScrollUp(lines int) {
	if !w.ScrollbackMode || w.Terminal == nil {
		return
	}

	maxOffset := w.ScrollbackLen()
	w.ScrollbackOffset = min(w.ScrollbackOffset+lines, maxOffset)
	w.InvalidateCache()
}

// ScrollDown scrolls down in the scrollback buffer.
func (w *Window) ScrollDown(lines int) {
	if !w.ScrollbackMode {
		return
	}

	w.ScrollbackOffset = max(w.ScrollbackOffset-lines, 0)
	if w.ScrollbackOffset == 0 {
		// If we scrolled all the way down, exit scrollback mode
		w.ExitScrollbackMode()
	} else {
		w.InvalidateCache()
	}
}

// EnterCopyMode enters vim-style copy/scrollback mode.
// This replaces both ScrollbackMode and SelectionMode with a unified vim interface.
func (w *Window) EnterCopyMode() {
	if w.CopyMode == nil {
		w.CopyMode = &CopyMode{}
	}

	w.CopyMode.Active = true
	w.CopyMode.State = CopyModeNormal
	w.CopyMode.CursorX = 0
	w.CopyMode.CursorY = w.Height / 2 // Start in MIDDLE (vim-style)
	w.CopyMode.ScrollOffset = 0       // Start at live content
	w.CopyMode.SearchQuery = ""
	w.CopyMode.SearchMatches = nil
	w.CopyMode.CurrentMatch = 0
	w.CopyMode.CaseSensitive = false
	w.CopyMode.PendingGCount = false

	// Sync with window scrollback
	w.ScrollbackOffset = 0

	w.InvalidateCache()
}

// ExitCopyMode exits copy mode and returns to normal terminal mode.
func (w *Window) ExitCopyMode() {
	if w.CopyMode != nil {
		w.CopyMode.Active = false
		w.CopyMode.State = CopyModeNormal
		w.CopyMode.ScrollOffset = 0
		// Clear search state
		w.CopyMode.SearchQuery = ""
		w.CopyMode.SearchMatches = nil
		w.CopyMode.SearchCache.Valid = false
	}

	// CRITICAL: Return to live content (bottom of scrollback)
	w.ScrollbackOffset = 0
	w.InvalidateCache()
}

// EnableCallbacks re-enables VT emulator callbacks after state restoration.
// This is used to prevent race conditions where buffered PTY output overwrites
// restored state during daemon session reattachment.
func (w *Window) EnableCallbacks() {
	w.suppressCallbacks.Store(false)
}

// DisableCallbacks temporarily disables VT emulator callbacks.
// This is used during state restoration to prevent race conditions.
func (w *Window) DisableCallbacks() {
	w.suppressCallbacks.Store(true)
}
