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

// HandleInput is the main input coordinator that routes messages to appropriate handlers
func HandleInput(msg tea.Msg, o *app.OS) (tea.Model, tea.Cmd) {
	var result tea.Model
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		result, cmd = HandleKeyPress(msg, o)
	case tea.KeyReleaseMsg:
		// Releases only arrive once the host has been asked for event types, and
		// tuios itself does one thing with one: end a hold. No binding acts on a
		// release, because acting on a release as well as a press would run every
		// binding twice. What is left goes to a pane that asked for releases.
		if o.ReleaseHoldKey(tea.KeyPressMsg(msg.Key())) {
			result, cmd = o, nil
			break
		}
		forwardKeyReleaseToFocused(msg, o)
		return o, nil
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
		// Incoming bracketed paste from the outer terminal (ESC[200~ ... ESC[201~).
		// This is passthrough input, not a clipboard operation: the outer terminal
		// pasted on the user's behalf, or an IME such as fcitx5 wrapped a commit in
		// paste markers. Forward it to the focused window's PTY without touching the
		// stored clipboard and without a "Pasted" notification (matching tmux/VTM).
		if o.Mode == app.TerminalMode {
			forwardPasteToFocused(o, msg.Content)
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

// requestQuit is what a quit keybinding does. In a daemon session it always
// opens the quit menu: quitting there used to silently and permanently kill
// the session, and detach was never surfaced, so the menu is what makes the
// safe option the default. Standalone it keeps the old shape: menu only when a
// window is running something the user would lose, instant quit otherwise.
func requestQuit(o *app.OS) (*app.OS, tea.Cmd) {
	if o.IsDaemonSession || shouldShowQuitDialog(o) {
		o.OpenQuitMenu()
		return o, nil
	}
	return quitSession(o)
}

// detachSession leaves the session running and quits this client (see
// OS.DetachClient, the one detach implementation). Outside a daemon session
// there is nothing to detach from, and the caller decides what that means
// instead.
func detachSession(o *app.OS) (*app.OS, tea.Cmd, bool) {
	cmd := o.DetachClient()
	if cmd == nil {
		return o, nil, false
	}
	return o, cmd, true
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

	// Hold-to-window-mode. The trigger is consumed here, before any mode routing,
	// so holding it cannot type anything into a pane; every other key struck
	// while it is held loses the trigger's own modifier, so holding Option and
	// tapping n runs the window-mode action bound to n.
	if o.PressHoldKey(msg) {
		return o, nil
	}
	msg = o.StripHoldModifier(msg)

	// A chord that only resolved because tuios recognised the character macOS
	// composed out of it is proof the Option key is not being sent as Alt.
	if chord, ok := macOptionChord(msg); ok && chord != msg.Keystroke() {
		o.NoteComposedOptionChord(chord)
	}

	// esc takes a message off the dock, in every mode, without consuming the key.
	//
	// Not consuming it is the whole design. esc means something to the shell,
	// to vim, to copy mode and to every overlay below, and a message is not
	// worth stealing it from any of them; dismissing is non-destructive, so
	// doing it as a side effect of an esc the user pressed for another reason
	// costs them nothing. What it buys is an exit for a sticky error, which
	// otherwise waits forever, and a way out of any message the user has read
	// and does not want to sit through.
	//
	// It runs before the overlay routing below so it works while help, the
	// palette or the quit dialog is up, since those are exactly the moments a
	// message is in the way.
	if msg.String() == "esc" {
		o.DismissNotifications()
	}

	// The close confirmation outranks even the quit menu: it is the last thing
	// between a keystroke and a session that cannot be brought back, so nothing
	// underneath may answer for it.
	if o.ShowSessionClose {
		return handleSessionCloseInput(msg, o)
	}

	// Handle the quit menu (highest priority - works in any mode)
	if o.ShowQuitMenu {
		return handleQuitMenuKey(msg, o)
	}

	// An open context menu owns the keyboard until it is dismissed. It is
	// checked before the mode split so it is navigable from terminal mode too:
	// the menu can be opened there, and arrow keys meant for it must not be
	// forwarded to the shell underneath.
	if o.ContextMenuActive() {
		return handleContextMenuKey(msg, o)
	}

	// The accent picker and an in-flight rename are both opened from the rail,
	// which owns the keyboard while focused, so they are checked ahead of it: an
	// editor and a picker own every key while they are up, wherever they came
	// from.
	if o.ShowAccentPicker {
		return handleAccentPickerInput(msg, o)
	}
	if o.Renaming() {
		return handleRenameMode(msg, o)
	}

	// The sidebar rail owns the keyboard while focused, in both terminal and
	// window mode (it is reachable from either via ctrl+b o), so pane and window
	// bindings never fire underneath it. Checked after the modal overlays above,
	// which outrank it, and before the mode split, which it supersedes.
	//
	// The leader is the one exception, and a pending chord with it. The rail
	// swallows what it does not bind, so holding the leader here would strand
	// every prefix command behind an esc, and ctrl+b e (which toggles the rail's
	// focus) could never toggle it back off. Both are left to fall through to the
	// mode handlers below, which already route the whole chord; PrefixActive
	// stays set for sub-prefixes too, so ctrl+b w 2 works from the rail as well.
	// The help overlay and the command palette are the other exceptions, for the
	// same reason as the modals above: the rail's own keys open both, and the rail
	// swallows what it does not bind, so leaving it in front would strand the
	// overlay's scroll, search and close keys (and every character of a palette
	// query) behind an esc that also drops the rail's focus.
	if o.SidebarFocused && !o.ShowHelp && !o.ShowCommandPalette && !o.PrefixActive && !isLeaderKey(msg) {
		return HandleSidebarKey(msg, o)
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

	// Script pause/resume while a script is actively playing. Its own config
	// section rather than the global one: script playback is its own keyboard
	// context, so sharing ctrl+p with the palette is not a conflict, and once a
	// script finishes ScriptMode is left (see maybeExitFinishedScript) and the
	// palette has the key back.
	if o.ScriptMode &&
		sectionAction(msg, o, (*config.KeybindRegistry).GetScriptAction) == "script_pause" {
		o.ScriptPaused = !o.ScriptPaused
		return o, nil
	}

	// Terminal mode handling
	if o.Mode == app.TerminalMode {
		return HandleTerminalModeKey(msg, o)
	}

	// Check for prefix key activation in window management mode
	if isLeaderKey(msg) {
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

	// Handle window management mode keys
	return HandleWindowManagementModeKey(msg, o)
}

// handleRenameMode handles keyboard input while the rename editor is open, for
// every kind of target it can point at. The editor is deliberately the same one
// in all three cases.
func handleRenameMode(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	switch msg.String() {
	case "enter":
		// CommitRename is the one rename. A window name is applied here; a
		// session or workspace name is daemon-owned and comes back as a command,
		// because the verb call must not run on this goroutine.
		return o, o.CommitRename()
	case "esc":
		// Cancel renaming
		o.EndRename()
		return o, nil
	case "backspace":
		o.RenameBackspace()
		return o, nil
	case "space":
		// A space arrives under its key name, never as a one-character string,
		// which is why names could not hold one.
		o.RenameAppend(" ")
		return o, nil
	default:
		// Text carries the characters the keypress actually produced, so an
		// accented or wide rune arrives whole instead of as stray bytes.
		o.RenameAppend(msg.Text)
		return o, nil
	}
}

// isLeaderKey reports whether a key press is the configured leader.
func isLeaderKey(msg tea.KeyPressMsg) bool {
	return strings.EqualFold(msg.String(), config.LeaderKey)
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
