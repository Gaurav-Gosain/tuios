package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss/v2"
	"github.com/charmbracelet/x/vt"
	pty "github.com/aymanbagabas/go-pty"
)

// Window represents a terminal window with its own shell process.
// Each window maintains its own virtual terminal, PTY, and rendering cache.
type Window struct {
	Title              string
	CustomName         string // User-defined window name
	Width              int
	Height             int
	X                  int
	Y                  int
	Z                  int
	ID                 string
	Terminal           *vt.Terminal
	Pty                pty.Pty
	Cmd                *pty.Cmd
	LastUpdate         time.Time
	Dirty              bool
	ContentDirty       bool
	PositionDirty      bool
	CachedContent      string
	CachedLayer        *lipgloss.Layer
	LastTerminalSeq    int
	IsBeingManipulated bool               // True when being dragged or resized
	updateCounter      int                // Counter for throttling background updates
	cancelFunc         context.CancelFunc // For graceful goroutine cleanup
	ioMu               sync.RWMutex       // Protect I/O operations
	Minimized          bool               // True when window is minimized to dock
	Minimizing         bool               // True when window is being minimized (animation playing)
	PreMinimizeX       int                // Store position before minimizing
	PreMinimizeY       int                // Store position before minimizing
	PreMinimizeWidth   int                // Store size before minimizing
	PreMinimizeHeight  int                // Store size before minimizing
	Workspace          int                // Workspace this window belongs to
	SelectionStart     struct{ X, Y int } // Selection start position
	SelectionEnd       struct{ X, Y int } // Selection end position
	IsSelecting        bool               // True when selecting text
	SelectedText       string             // Currently selected text
	SelectionCursor    struct{ X, Y int } // Current cursor position in selection mode
	ProcessExited      bool               // True when process has exited
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
	terminal := vt.NewTerminal(terminalWidth, terminalHeight)

	// Detect shell
	shell := detectShell()

	// Set up environment
	cmd := exec.Command(shell)
	cmd.Env = append(os.Environ(),
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
		"TUIOS_WINDOW_ID="+id,
	)

	// Create PTY and start command
	ptyInstance, err := pty.New()
	if err != nil {
		// Return nil to indicate failure - caller should handle this
		return nil
	}

	// Create command through PTY
	ptyCmd := ptyInstance.Command(shell)
	ptyCmd.Env = cmd.Env
	
	// Start the command
	if err := ptyCmd.Start(); err != nil {
		ptyInstance.Close()
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())

	window := &Window{
		Title:              title,
		Width:              width,
		Height:             height,
		X:                  x,
		Y:                  y,
		Z:                  z,
		ID:                 id,
		Terminal:           terminal,
		Pty:                ptyInstance,
		Cmd:                ptyCmd,
		LastUpdate:         time.Now(),
		Dirty:              true,
		ContentDirty:       true,
		PositionDirty:      true,
		CachedContent:      "",
		CachedLayer:        nil,
		IsBeingManipulated: false,
		cancelFunc:         cancel,
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

		// Wait for process to exit
		ptyCmd.Wait()

		// Mark process as exited
		window.ProcessExited = true

		// Clean up
		cancel()

		// Give a small delay to ensure final output is captured
		time.Sleep(time.Duration(ProcessWaitDelay) * time.Millisecond)

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

func detectShell() string {
	// Check environment variable first
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

// enableTerminalFeatures enables advanced terminal features like bracketed paste
func (w *Window) enableTerminalFeatures() {
	if w.Pty == nil {
		return
	}

	// Enable bracketed paste mode - allows terminals to distinguish pasted text
	// This prevents accidental command execution when pasting
	// Enable bracketed paste mode, ignore errors if PTY is not ready
	_, _ = w.Pty.Write([]byte("\x1b[?2004h"))

	// Don't enable mouse modes automatically - let applications request them
	// Applications like vim, less, htop will enable mouse support themselves
	// by sending the appropriate escape sequences
}

// disableTerminalFeatures disables advanced terminal features before closing
func (w *Window) disableTerminalFeatures() {
	if w.Pty == nil {
		return
	}

	// Disable bracketed paste mode
	// Disable bracketed paste mode, ignore errors during cleanup
	_, _ = w.Pty.Write([]byte("\x1b[?2004l"))

	// We don't need to disable mouse tracking since we're not enabling it
	// Applications that enabled it will disable it themselves
}

func (w *Window) handleIOOperations() {
	ctx, cancel := context.WithCancel(context.Background())
	w.cancelFunc = cancel

	// PTY to Terminal copy (output from shell) - with proper context handling
	go func() {
		defer func() {
			if r := recover(); r != nil {
				// Silently recover from panics during PTY read
				_ = r // Explicitly ignore the recovered value
			}
		}()

		// Get buffer from pool for better memory management
		bufPtr := byteSlicePool.Get().(*[]byte)
		buf := *bufPtr
		defer byteSlicePool.Put(bufPtr)
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
					// Write to terminal with mutex protection
					w.ioMu.RLock()
					if w.Terminal != nil {
						w.Terminal.Write(buf[:n])
					}
					w.ioMu.RUnlock()
				}
			}
		}
	}()

	// Terminal to PTY copy (input to shell) - with proper context handling
	go func() {
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
				if w.Terminal == nil {
					return
				}

				n, err := w.Terminal.Read(buf)
				if err != nil {
					if err != io.EOF && !strings.Contains(err.Error(), "file already closed") &&
						!strings.Contains(err.Error(), "input/output error") {
						// Log unexpected errors for debugging
						_ = err
					}
					return
				}
				if n > 0 {
					w.ioMu.RLock()
					if w.Pty != nil {
						if _, err := w.Pty.Write(buf[:n]); err != nil {
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

	w.Terminal.Resize(termWidth, termHeight)
	if w.Pty != nil {
		if err := w.Pty.Resize(termWidth, termHeight); err != nil {
			// Log PTY resize error for debugging, but continue operation
			// This is not fatal as the terminal can still function
			_ = err // Acknowledge error but don't break functionality
		}
	}
	w.Width = width
	w.Height = height

	// Mark both position and content dirty for resize operations
	w.MarkPositionDirty()
	w.MarkContentDirty()
}

// Close closes the window and cleans up resources.
func (w *Window) Close() {
	// Nil safety check
	if w == nil {
		return
	}

	// Disable terminal features before closing
	w.disableTerminalFeatures()

	// Cancel all goroutines first
	if w.cancelFunc != nil {
		w.cancelFunc()
		w.cancelFunc = nil
	}

	// Cleanup with proper synchronization
	w.ioMu.Lock()
	defer w.ioMu.Unlock()

	// Close PTY first to stop I/O operations
	if w.Pty != nil {
		// Best effort close - ignore errors
		w.Pty.Close()
		w.Pty = nil
	}

	// Kill the process with timeout
	if w.Cmd != nil && w.Cmd.Process != nil {
		done := make(chan bool, 1)
		go func() {
			defer func() {
				if r := recover(); r != nil {
					// Silently recover from panics during process cleanup
					_ = r // Explicitly ignore the recovered value
				}
			}()

			// Best effort kill
			w.Cmd.Process.Kill()
			w.Cmd.Wait()
			done <- true
		}()

		// Wait for process cleanup with timeout
		select {
		case <-done:
			// Clean shutdown
		case <-time.After(time.Millisecond * 500):
			// Force cleanup after shorter timeout for better responsiveness
		}

		w.Cmd = nil
	}

	// Clear terminal reference
	w.Terminal = nil
}

// SendInput sends input to the window's terminal with enhanced error handling.
func (w *Window) SendInput(input []byte) error {
	if w == nil {
		return fmt.Errorf("window is nil")
	}

	w.ioMu.RLock()
	defer w.ioMu.RUnlock()

	if w.Pty == nil {
		return fmt.Errorf("no PTY available")
	}

	if len(input) == 0 {
		return nil // Nothing to send
	}

	n, err := w.Pty.Write(input)
	if err != nil {
		return fmt.Errorf("failed to write to PTY: %w", err)
	}

	if n != len(input) {
		return fmt.Errorf("partial write to PTY: wrote %d of %d bytes", n, len(input))
	}

	// Force immediate update
	w.Dirty = true
	w.ContentDirty = true

	// Clear cached content to force re-render
	w.CachedContent = ""
	w.CachedLayer = nil

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
