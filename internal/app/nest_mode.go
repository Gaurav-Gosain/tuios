package app

import "github.com/Gaurav-Gosain/tuios/internal/config"

// Nest mode: tmux-matryoshka's prefix Enter, as the nest action.
//
// A program in the pane that reads the same keys as tuios, a tmux or a
// tuios on another machine with ctrl+b for a leader, gets none of them while
// tuios takes them first. Nest mode turns tuios off for one client: terminal
// mode gives the focused pane every key, the leader included, and lock
// mode's paths (the hold key, the prefix repeat window, the workspace chords)
// see a locked client. One key comes back, keybindings.nest_exit_key, F12
// unless it says otherwise, and it ends the mode without reaching the pane.
//
// The state is the client's, not the session's: another client attached to
// the same session keeps its keys. It ends with its exit key, when the client
// stays out of terminal mode (see settleNest), and on detach, since a client
// that attaches builds a new model.
//
// The overlays still take their keys while they are up, as they do under
// lock: one opened with the mouse in nest mode would otherwise be stranded.
// The rail's list takes them too while it has the keyboard, and nest mode
// waits behind it: the pill says SIDEBAR, not NEST, and esc gives the keys
// back to the pane in nest mode.

// Nested reports whether nest mode gives this client's keys to the focused
// pane now.
func (m *OS) Nested() bool {
	return m.nest && m.Mode == TerminalMode && !m.SidebarFocused
}

// EnterNest turns nest mode on: terminal mode, the rail's list giving the
// keyboard back, and every key to the pane the user types into from the next
// press, a rail session pane that has the keyboard or the focused pane. It
// reports false, and changes nothing, with no focused pane: terminal mode
// with nothing focused drops back to window mode at the next state sync.
func (m *OS) EnterNest() bool {
	if m.GetFocusedWindow() == nil {
		return false
	}
	m.ExitSidebarFocus()
	m.EnterTerminalMode()
	m.nest = true
	m.MarkAllDirty()
	return true
}

// settleNest ends nest mode when the client has left terminal mode. It runs
// at the end of every Update, so it holds whatever path set the mode: a lot of
// them assign Mode directly (closing the last pane, a workspace switch, some
// mouse paths). Without it the flag outlived the window mode they left the
// client in, and came back with the next terminal mode on its own.
//
// A gesture borrows window mode and owes terminal mode back when it ends: a
// pane drag or resize, a ctrl drag, the hold key, a click that will enter
// terminal mode on release. The borrow spans several messages, so nest mode
// waits for the gesture rather than ending at its press.
func (m *OS) settleNest() {
	if m.nest && m.Mode != TerminalMode && !m.modeBorrowed() {
		m.nest = false
		m.MarkAllDirty()
	}
}

// modeBorrowed reports whether a gesture holds window mode and will give
// terminal mode back when it ends.
func (m *OS) modeBorrowed() bool {
	return m.pointerGestureWasTerminal || m.CtrlDragWasTerminal || m.hold.active ||
		m.ClickToTypePending || m.Dragging || m.Resizing
}

// LeaveNest turns nest mode off, and leaves the client in terminal mode.
func (m *OS) LeaveNest() {
	if !m.nest {
		return
	}
	m.nest = false
	m.MarkAllDirty()
}

// nestLabel is what the dock's mode pill, or the badge, says in nest mode:
// NEST and the key that ends it.
func (m *OS) nestLabel() string {
	return "NEST " + config.KeyLabel(m.Settings.GetNestExitKey())
}

// modePillShown reports whether the dock draws a mode pill at all.
func (m *OS) modePillShown() bool {
	if m.Settings.DockbarPosition == "hidden" {
		return false
	}
	m.ensureDockPlan()
	return m.dockPlan.Has(config.DockComponentMode)
}
