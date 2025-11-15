// Package input implements TUIOS input handling and key forwarding.
//
// This module handles keyboard input in both Window Management and Terminal modes.
package input

import (
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	tea "github.com/charmbracelet/bubbletea/v2"
)

// PrefixKeyTimeout is the duration after which prefix mode times out
const PrefixKeyTimeout = 2 * time.Second

// HandleInput is the main input coordinator that routes messages to appropriate handlers
func HandleInput(msg tea.Msg, o *app.OS) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		return HandleKeyPress(msg, o)
	case tea.PasteStartMsg:
		return o, nil
	case tea.PasteEndMsg:
		return o, nil
	case tea.MouseClickMsg:
		return handleMouseClick(msg, o)
	case tea.MouseMotionMsg:
		return handleMouseMotion(msg, o)
	case tea.MouseReleaseMsg:
		return handleMouseRelease(msg, o)
	case tea.MouseWheelMsg:
		return handleMouseWheel(msg, o)
	case tea.PasteMsg:
		// Handle bracketed paste from terminal (when pasting via Cmd+V in Ghostty, etc.)
		// Only handle paste in terminal mode
		if o.Mode == app.TerminalMode {
			o.ClipboardContent = string(msg)
			handleClipboardPaste(o)
		}
		return o, nil
	case tea.ClipboardMsg:
		// Handle OSC 52 clipboard read response (from tea.ReadClipboard)
		// Only handle paste in terminal mode
		if o.Mode == app.TerminalMode {
			o.ClipboardContent = msg.Content
			handleClipboardPaste(o)
		}
		return o, nil
	}
	return o, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// shouldShowQuitDialog checks if there are any terminals with active foreground processes
// to show quit confirmation for. Returns true if any window has a foreground process
// (besides the shell itself), or if we're unable to detect (falls back to true).
func shouldShowQuitDialog(o *app.OS) bool {
	// Check each window for active foreground processes
	for _, win := range o.Windows {
		if win != nil && win.HasForegroundProcess() {
			return true
		}
	}
	return false
}

// HandleKeyPress handles all keyboard input and routes to mode-specific handlers
func HandleKeyPress(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	// Capture key event for showkeys overlay if enabled
	if o.ShowKeys {
		o.CaptureKeyEvent(msg)
	}

	// Handle quit confirmation dialog (highest priority - works in any mode)
	if o.ShowQuitConfirm {
		key := msg.String()

		// Close dialog with escape
		if key == "esc" {
			o.ShowQuitConfirm = false
			o.QuitConfirmSelection = 0
			return o, nil
		}

		// Navigate with arrow keys or vim keys
		if key == "left" || key == "h" {
			o.QuitConfirmSelection = 0 // Yes (left)
			return o, nil
		}
		if key == "right" || key == "l" {
			o.QuitConfirmSelection = 1 // No (right)
			return o, nil
		}

		// Quick selection with y/n keys
		if key == "y" {
			o.QuitConfirmSelection = 0 // Yes
			o.Cleanup()
			return o, tea.Quit
		}
		if key == "n" {
			o.QuitConfirmSelection = 1 // No
			o.ShowQuitConfirm = false
			return o, nil
		}

		// Confirm selection with enter
		if key == "enter" {
			if o.QuitConfirmSelection == 0 {
				// Yes selected - quit
				o.Cleanup()
				return o, tea.Quit
			} else {
				// No selected - close dialog
				o.ShowQuitConfirm = false
				return o, nil
			}
		}

		// Quit dialog is showing but key wasn't handled - ignore it
		return o, nil
	}

	// Handle script pause/resume (Ctrl+P)
	if msg.String() == "ctrl+p" && o.ScriptMode {
		o.ScriptPaused = !o.ScriptPaused
		return o, nil
	}

	// Handle rename mode
	if o.RenamingWindow {
		return handleRenameMode(msg, o)
	}

	// Terminal mode handling
	if o.Mode == app.TerminalMode {
		return HandleTerminalModeKey(msg, o)
	}

	// Check for prefix key activation in window management mode
	msgStr := strings.ToLower(msg.String())
	leaderKey := strings.ToLower(config.LeaderKey)
	if msgStr == leaderKey {
		return handlePrefixKey(msg, o)
	}

	// Handle workspace prefix commands (Ctrl+B, w, ...)
	if o.WorkspacePrefixActive {
		return HandleWorkspacePrefixCommand(msg, o)
	}

	// Handle minimize prefix commands (Ctrl+B, m, ...)
	if o.MinimizePrefixActive {
		return HandleMinimizePrefixCommand(msg, o)
	}

	// Handle tiling prefix commands (Ctrl+B, t, ...)
	if o.TilingPrefixActive {
		return HandleTilingPrefixCommand(msg, o)
	}

	// Handle debug prefix commands (Ctrl+B, D, ...)
	if o.DebugPrefixActive {
		return HandleDebugPrefixCommand(msg, o)
	}

	// Handle prefix commands in window management mode
	if o.PrefixActive {
		return HandlePrefixCommand(msg, o)
	}

	// Timeout prefix mode after 2 seconds
	if o.PrefixActive && time.Since(o.LastPrefixTime) > PrefixKeyTimeout {
		o.PrefixActive = false
	}

	// Handle window management mode keys
	return HandleWindowManagementModeKey(msg, o)
}

// handleRenameMode handles keyboard input during window renaming
func handleRenameMode(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	switch msg.String() {
	case "enter":
		// Apply the new name
		if focusedWindow := o.GetFocusedWindow(); focusedWindow != nil {
			focusedWindow.CustomName = o.RenameBuffer
			focusedWindow.InvalidateCache()
		}
		o.RenamingWindow = false
		o.RenameBuffer = ""
		return o, nil
	case "esc":
		// Cancel renaming
		o.RenamingWindow = false
		o.RenameBuffer = ""
		return o, nil
	case "backspace":
		if len(o.RenameBuffer) > 0 {
			o.RenameBuffer = o.RenameBuffer[:len(o.RenameBuffer)-1]
		}
		return o, nil
	default:
		// Add character to buffer if it's a printable character
		if len(msg.String()) == 1 && msg.String()[0] >= 32 && msg.String()[0] < 127 {
			o.RenameBuffer += msg.String()
		}
		return o, nil
	}
}

// handlePrefixKey handles Ctrl+B prefix key activation
func handlePrefixKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	// If prefix is already active, deactivate it (double leader key cancels)
	if o.PrefixActive {
		o.PrefixActive = false
		return o, nil
	}
	// Activate prefix mode
	o.PrefixActive = true
	o.LastPrefixTime = time.Now()
	return o, nil
}

// HandlePrefixCommand handles prefix commands (Ctrl+B followed by another key)
func HandlePrefixCommand(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	// Deactivate prefix after handling command
	o.PrefixActive = false

	switch msg.String() {
	case "w":
		// Activate workspace prefix mode
		o.WorkspacePrefixActive = true
		o.PrefixActive = true // Keep prefix active for the next key
		o.LastPrefixTime = time.Now()
		return o, nil
	case "m":
		// Activate minimize prefix mode
		o.MinimizePrefixActive = true
		o.PrefixActive = true // Keep prefix active for the next key
		o.LastPrefixTime = time.Now()
		return o, nil
	case "t":
		// Activate tiling/window prefix mode
		o.TilingPrefixActive = true
		o.PrefixActive = true // Keep prefix active for the next key
		o.LastPrefixTime = time.Now()
		return o, nil
	case "D":
		// Activate debug prefix mode (Ctrl+B, Shift+D)
		o.DebugPrefixActive = true
		o.PrefixActive = true // Keep prefix active for the next key
		o.LastPrefixTime = time.Now()
		return o, nil
	// Window management
	case "c":
		// Create new window (like tmux)
		o.AddWindow("")
		return o, nil
	case "x":
		// Close current window
		if len(o.Windows) > 0 && o.FocusedWindow >= 0 {
			o.DeleteWindow(o.FocusedWindow)
		}
		return o, nil
	case ",", "r":
		// Rename window (like tmux with ',' or like normal mode with 'r')
		if len(o.Windows) > 0 && o.FocusedWindow >= 0 {
			focusedWindow := o.GetFocusedWindow()
			if focusedWindow != nil {
				o.RenamingWindow = true
				o.RenameBuffer = focusedWindow.CustomName
			}
		}
		return o, nil

	// Window navigation
	case "n", "tab":
		// Next window
		if len(o.Windows) > 0 {
			o.CycleToNextVisibleWindow()
		}
		return o, nil
	case "p", "shift+tab":
		// Previous window (like tmux with 'p' or like normal mode with 'shift+tab')
		if len(o.Windows) > 0 {
			o.CycleToPreviousVisibleWindow()
		}
		return o, nil
	case "0", "1", "2", "3", "4", "5", "6", "7", "8", "9":
		// Jump to window by number
		return handlePrefixWindowSelection(msg, o)

	// Layout commands
	case "space":
		// Toggle tiling mode (like tmux)
		o.AutoTiling = !o.AutoTiling
		if o.AutoTiling {
			o.TileAllWindows()
		}
		return o, nil
	case "z":
		// Toggle fullscreen for current window
		if !o.AutoTiling && len(o.Windows) > 0 && o.FocusedWindow >= 0 {
			o.Snap(o.FocusedWindow, app.SnapFullScreen)
		}
		return o, nil

	// Copy mode
	case "[":
		// Enter copy mode (vim-style scrollback/selection)
		if focusedWindow := o.GetFocusedWindow(); focusedWindow != nil {
			focusedWindow.EnterCopyMode()
			o.ShowNotification("COPY MODE (hjkl/q)", "info", 2*time.Second)
		}
		return o, nil

	// Help
	case "?":
		// Toggle help
		o.ShowHelp = !o.ShowHelp
		return o, nil

	case "q":
		// Show quit confirmation dialog (only if there are terminals)
		if shouldShowQuitDialog(o) {
			o.ShowQuitConfirm = true
			o.QuitConfirmSelection = 0 // Default to Yes
		} else {
			// No terminals - just quit
			o.Cleanup()
			return o, tea.Quit
		}
		return o, nil

	// Exit prefix mode
	case "esc", "ctrl+c":
		// Just cancel prefix mode
		return o, nil

	default:
		// Unknown command, ignore
		return o, nil
	}
}

// handlePrefixWindowSelection handles window selection via prefix+number
func handlePrefixWindowSelection(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	num := int(msg.String()[0] - '0')
	if o.AutoTiling {
		// In tiling mode, select visible window in current workspace
		visibleIndex := 0
		for i, win := range o.Windows {
			if win.Workspace == o.CurrentWorkspace && !win.Minimized {
				visibleIndex++
				if visibleIndex == num || (num == 0 && visibleIndex == 10) {
					o.FocusWindow(i)
					break
				}
			}
		}
	} else {
		// Normal mode, select by absolute index in current workspace
		windowsInWorkspace := 0
		for i, win := range o.Windows {
			if win.Workspace == o.CurrentWorkspace {
				windowsInWorkspace++
				if windowsInWorkspace == num || (num == 0 && windowsInWorkspace == 10) {
					o.FocusWindow(i)
					break
				}
			}
		}
	}
	return o, nil
}
