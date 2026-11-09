package app

import (
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// The keys of the keymap actions the daemon runs (parity gap 1, step 4,
// batch A; see internal/session/daemon_actions.go). This client sent their
// result in its next state push, so two clients that pressed one at once
// each pushed a state built on the one it had, and the later push lost the
// other's step. Now the key sends the action to the daemon, which runs it
// on its own state, one at a time, and every client takes the result from
// the push.
//
// move_workspace_left and right are an op already (RenumberWorkspaces), and
// close_workspace asks first and closes through CloseWindow, so their keys
// keep their handlers.
//
// Batch B: a swap or a resize step key in the split layout goes too. The
// tree ops carried a whole tree built on the one this client held, so two
// clients that resized at once each sent a tree with only its own step.

// SendsDaemonAction reports whether the key for action goes to the daemon
// as an intent instead of to its handler here.
func (m *OS) SendsDaemonAction(action string) bool {
	if !m.IsDaemonSession || !m.DaemonClient.DaemonActions() {
		return false
	}
	if session.IsDirectionAction(action) {
		// A swap or a resize step in the split layout: the daemon runs it on
		// its tree. A focus key stays here: it may leave or enter the rail,
		// or go to the editor in the pane, which only this client knows, and
		// the focus it sets is a value, not a step, so a push carries it
		// whole.
		return !strings.HasPrefix(action, "focus_") && !strings.HasPrefix(action, "terminal_focus_") &&
			m.AutoTiling && m.LayoutModeName() == LayoutModeBSP && !m.SidebarFocused
	}
	if !session.IsDaemonAction(action) {
		return false
	}
	switch action {
	case "move_workspace_left", "move_workspace_right", "close_workspace":
		return false
	}
	return true
}

// SendDaemonAction sends the key for action to the daemon. A focus it moves
// arrives with the push, and enters terminal mode there when the person
// asked for that on a targeted focus (auto_enter_terminal_on_focus).
func (m *OS) SendDaemonAction(action string) {
	if strings.HasPrefix(action, "select_window_") && m.Settings.AutoEnterTerminalOnFocus != config.AutoEnterTerminalOff {
		m.focusEnterPending = true
	}
	m.intentSent = true
	if err := m.DaemonClient.SendIntent("Action", action); err != nil {
		m.focusEnterPending = false
		m.ShowNotification("Could not reach the daemon: "+err.Error(), "error", m.Settings.NotificationDuration)
	}
}

// takeFocusEnter enters terminal mode for a focus a sent select_window moved,
// once the push that moved it lands.
func (m *OS) takeFocusEnter(focusChanged bool) {
	if !m.focusEnterPending || !focusChanged {
		return
	}
	m.focusEnterPending = false
	if m.Mode == WindowManagementMode && m.FocusedWindow >= 0 {
		m.ChooseMode(TerminalMode, ModeCauseKey)
	}
}

// TakeIntentSent reports whether the input just handled sent a daemon action
// and clears the mark. The push that follows every input must not go out
// then: it carries this client's state from before the action, and landing
// after the daemon ran it, it put the panes' rectangles back where they were
// before, so the next sync moved them there (a swap undone on screen at the
// next close).
func (m *OS) TakeIntentSent() bool {
	sent := m.intentSent
	m.intentSent = false
	return sent
}
