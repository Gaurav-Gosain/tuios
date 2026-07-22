// Package input implements TUIOS input handling and key forwarding.
//
// This module handles keyboard input in both Window Management and Terminal modes.
package input

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// PrefixKeyTimeout is the duration after which prefix mode times out
const PrefixKeyTimeout = 2 * time.Second

// HandleInput is the main input coordinator that routes messages to appropriate handlers
func HandleInput(msg tea.Msg, o *app.OS) (tea.Model, tea.Cmd) {
	var result tea.Model
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		result, cmd = HandleKeyPress(msg, o)
	case tea.PasteStartMsg:
		return o, nil
	case tea.PasteEndMsg:
		return o, nil
	case tea.MouseClickMsg:
		if o.ShowScrollbackBrowser {
			result, cmd = handleScrollbackBrowserMouseClick(msg, o)
		} else {
			result, cmd = handleMouseClick(msg, o)
		}
	case tea.MouseMotionMsg:
		if o.ShowScrollbackBrowser {
			result, cmd = handleScrollbackBrowserMouseMotion(msg, o)
			// Don't sync motion events
			return result, cmd
		}
		// Don't sync on motion - too frequent
		return handleMouseMotion(msg, o)
	case tea.MouseReleaseMsg:
		if o.ShowScrollbackBrowser {
			result, cmd = handleScrollbackBrowserMouseRelease(o)
		} else {
			result, cmd = handleMouseRelease(msg, o)
		}
	case tea.MouseWheelMsg:
		if o.ShowScrollbackBrowser {
			result, cmd = handleScrollbackBrowserMouseWheel(msg, o)
		} else {
			result, cmd = handleMouseWheel(msg, o)
		}
	case tea.PasteMsg:
		// Handle bracketed paste from terminal (when pasting via Cmd+V in Ghostty, etc.)
		// Only handle paste in terminal mode
		if o.Mode == app.TerminalMode {
			o.ClipboardContent = msg.Content
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
	default:
		return o, nil
	}

	// Sync state to daemon after any input that might have changed state
	// This ensures state persists across reconnects without explicit save
	if o.IsDaemonSession {
		o.SyncStateToDaemon()
	}

	return result, cmd
}

// shouldShowQuitDialog checks if there are any terminals with active foreground processes
// to show quit confirmation for. Returns true if any window has a foreground process
// (besides the shell itself), or if we're unable to detect (falls back to true).
func shouldShowQuitDialog(o *app.OS) bool {
	if config.AlwaysConfirmQuit {
		return true
	}
	// Check each window for active foreground processes
	for _, win := range o.Windows {
		if win != nil && win.HasForegroundProcess() {
			return true
		}
	}
	return false
}

// quitSession ends the session and the client. In a daemon session that means
// killing the session, not just detaching from it: quitting is the user saying
// the session is over, and leaving it running would strand it with no way back
// except an explicit attach.
//
// It was written out at six call sites (the three quit keybindings, and the yes
// button of the confirmation dialog reached by key, by enter and by mouse), each
// of which could drift from the others about whether to kill the session or run
// Cleanup. There is one of them now.
//
// The kill-and-clean sequence itself lives on OS.QuitSession, which also records
// that the quit was deliberate. That matters because killing the session makes
// the daemon announce the session ending and the connection dropping back to us,
// and either can land before the program finishes quitting; without the recorded
// intent Update reports the user's own quit as an unexpected termination.
func quitSession(o *app.OS) (*app.OS, tea.Cmd) {
	o.QuitSession()
	return o, tea.Quit
}

// requestQuit is what a quit keybinding does: put up the confirmation dialog
// when a window is running something the user would lose, and quit outright when
// nothing is. The dialog's own buttons call quitSession directly.
func requestQuit(o *app.OS) (*app.OS, tea.Cmd) {
	if shouldShowQuitDialog(o) {
		o.ShowQuitConfirm = true
		o.QuitConfirmSelection = 0 // Default to Yes
		return o, nil
	}
	return quitSession(o)
}

// detachSession leaves the session running and quits this client. It pushes
// state first so the session the user comes back to is the one they left.
// Outside a daemon session there is nothing to detach from, and the caller
// decides what that means instead.
func detachSession(o *app.OS) (*app.OS, tea.Cmd, bool) {
	if !o.IsDaemonSession {
		return o, nil, false
	}
	o.SyncStateToDaemon()
	o.FireDetached()
	// Deliberately no Cleanup: the session outlives this client.
	return o, tea.Quit, true
}

// HandleKeyPress handles all keyboard input and routes to mode-specific handlers
func HandleKeyPress(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	// Capture the keypress for the showkeys overlay when it is enabled. This is
	// the earliest shared point in the input path, before any mode routing or
	// handler can consume the key, so the overlay reflects keys in both
	// window-management and terminal mode. It only observes; it never consumes.
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
			return quitSession(o)
		}
		if key == "n" {
			o.QuitConfirmSelection = 1 // No
			o.ShowQuitConfirm = false
			return o, nil
		}

		// Confirm selection with enter
		if key == "enter" {
			if o.QuitConfirmSelection == 0 {
				return quitSession(o)
			}
			// No selected - close dialog
			o.ShowQuitConfirm = false
			return o, nil
		}

		// Quit dialog is showing but key wasn't handled - ignore it
		return o, nil
	}

	// Terminal-mode keystrokes are recorded at the point they are actually
	// forwarded to the PTY (see recordTerminalKey in HandleTerminalModeKey), not
	// here: recording before prefix/overlay routing captured prefix chords,
	// copy-mode keys, palette queries, and transition-suppressed fragments that
	// never reach the shell, so tapes replayed garbage. WM-mode actions are
	// recorded at dispatch time.

	// Handle the project-tape review/trust dialog (modal, highest priority after
	// quit): it must swallow keys so a keystroke meant for the dialog never leaks
	// to the shell or a window-manager binding.
	if o.ShowTapeReview {
		if o.HandleTapeReviewInput(msg.String()) {
			return o, nil
		}
	}

	// Handle tape manager overlay (high priority - intercepts keys when shown)
	if o.ShowTapeManager {
		if o.HandleTapeManagerInput(msg.String()) {
			return o, nil
		}
		// Key not handled by tape manager, fall through
	}

	// Handle script pause/resume (Ctrl+P) while a script is actively playing.
	// Once a script finishes, ScriptMode is left (see maybeExitFinishedScript),
	// so this no longer shadows the command palette binding. Matched on the
	// decoded key event so it works under every Kitty keyboard encoding.
	if o.ScriptMode && isCtrlP(msg) {
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

	// Handle layout prefix commands (Ctrl+B, L, ...)
	if o.LayoutPrefixActive {
		return handleTerminalLayoutPrefix(msg, o)
	}

	// Handle tape prefix commands (Ctrl+B, T, ...)
	if o.TapePrefixActive {
		return HandleTapePrefixCommand(msg, o)
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
		// Apply the new name. RenameWindowByID is the one rename: in a daemon
		// session it asks the daemon, which owns the name, and the change comes
		// back as a state push.
		if focusedWindow := o.GetFocusedWindow(); focusedWindow != nil {
			_ = o.RenameWindowByID(focusedWindow.ID, o.RenameBuffer)
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
			if fw := o.GetFocusedWindow(); fw != nil {
				fw.InvalidateCache()
			}
		}
		return o, nil
	default:
		// Add character to buffer if it's a printable character
		if len(msg.String()) == 1 && msg.String()[0] >= 32 && msg.String()[0] < 127 {
			o.RenameBuffer += msg.String()
			// Invalidate cache so the rename input is visible immediately
			if fw := o.GetFocusedWindow(); fw != nil {
				fw.InvalidateCache()
			}
		}
		return o, nil
	}
}

// handlePrefixKey handles Ctrl+B prefix key activation
func handlePrefixKey(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
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

// handleLogViewerKey handles keyboard input when the log viewer overlay is active.
// This is shared between terminal mode and window management mode.
func handleLogViewerKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	key := msg.String()

	// Close log viewer with q or esc
	if key == "q" || key == "esc" {
		o.ShowLogs = false
		o.LogScrollOffset = 0
		return o, nil
	}

	logsPerPage, maxScroll := logScrollBounds(o.Height, len(o.LogMessages))

	// Scroll up/down
	if key == "up" || key == "k" {
		if o.LogScrollOffset > 0 {
			o.LogScrollOffset--
		}
		return o, nil
	}
	if key == "down" || key == "j" {
		if o.LogScrollOffset < maxScroll {
			o.LogScrollOffset++
		}
		return o, nil
	}

	// Page up/down (scroll by half page)
	pageSize := max(logsPerPage/2, 1)
	if key == "pgup" || key == "ctrl+u" {
		o.LogScrollOffset -= pageSize
		if o.LogScrollOffset < 0 {
			o.LogScrollOffset = 0
		}
		return o, nil
	}
	if key == "pgdown" || key == "ctrl+d" {
		o.LogScrollOffset += pageSize
		if o.LogScrollOffset > maxScroll {
			o.LogScrollOffset = maxScroll
		}
		return o, nil
	}

	// Go to top/bottom
	if key == "g" || key == "home" {
		o.LogScrollOffset = 0
		return o, nil
	}
	if key == "G" || key == "end" {
		o.LogScrollOffset = maxScroll
		return o, nil
	}

	// Ignore other keys when log viewer is active
	return o, nil
}

// logScrollBounds computes the scrollable range for the log viewer overlay.
// Returns logsPerPage (visible capacity) and maxScroll (maximum scroll offset).
func logScrollBounds(screenHeight, totalLogs int) (logsPerPage, maxScroll int) {
	maxDisplayHeight := max(screenHeight-8, 8)

	// Fixed overhead: title (1) + blank after title (1) + blank before hint (1) + hint (1) = 4
	fixedLines := 4
	// If scrollable, add scroll indicator: blank (1) + indicator (1) = 2
	if totalLogs > maxDisplayHeight-fixedLines {
		fixedLines = 6
	}
	logsPerPage = max(maxDisplayHeight-fixedLines, 1)
	maxScroll = max(totalLogs-logsPerPage, 0)
	return logsPerPage, maxScroll
}
