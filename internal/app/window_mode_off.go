package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// Window mode off: keybindings.window_mode = false (discussion #2249).
//
// A tmux user types into a pane all the time and reaches every action through
// the leader. With window mode off, tuios works that way: while a pane has the
// focus the client is in terminal mode, and nothing leaves it there.
//
// Two rules make that hold, and both live here and in mode.go:
//
//   - A choice of window mode with a pane in focus is a choice of terminal
//     mode (ChooseMode). That covers every key, click and action that picks
//     window mode: Prefix+Esc, Alt+Esc, enter_window_mode, a restore from the
//     dock, a tape's WindowManagementMode.
//   - After each message, a client left in window mode with a pane in focus
//     and no gesture under way goes back to terminal mode (settle). That
//     covers the borrows a pointer gesture takes, which are not choices, and
//     a pane that arrives while there was none, as when the welcome screen's
//     n opens the first pane.
//
// With no pane in focus the client stays in window mode, as it always does:
// there is no pane to type into, and the welcome screen's keys are window
// mode keys.
//
// The keys whose only work is to enter window mode say once, in a notice,
// that it is off (NoteWindowModeOff), and the help, the palette hints and
// the leader menu leave them out (config.IsWindowModeAction).

// WindowModeOff reports whether keybindings.window_mode is false.
func (m *OS) WindowModeOff() bool { return m.Settings.NoWindowMode }

// windowModeRefused reports whether a choice of window mode turns into a
// choice of terminal mode: window mode is off and a pane has the focus.
func (m *OS) windowModeRefused() bool {
	return m.Settings.NoWindowMode && m.GetFocusedWindow() != nil
}

// settleWindowModeOff puts a client that a message left in window mode back
// in terminal mode, when window mode is off, a pane has the focus, and no
// gesture holds window mode for its length.
func (m *OS) settleWindowModeOff() {
	if !m.Settings.NoWindowMode || m.Mode != WindowManagementMode || m.modeBorrowed() {
		return
	}
	if m.GetFocusedWindow() == nil {
		return
	}
	m.ChooseMode(TerminalMode, ModeCauseAction)
}

// NoteWindowModeOff is what a key that only enters window mode does while
// window mode is off. The first such key in a client shows a notice that
// says so and names the leader. The keys after it do nothing.
func (m *OS) NoteWindowModeOff() {
	if m.windowModeNoted {
		return
	}
	m.windowModeNoted = true
	m.ShowNotification("Window mode is off. Press "+config.KeyLabel(m.Settings.LeaderKey)+" for window actions.",
		"info", m.Settings.NotificationDuration)
}
