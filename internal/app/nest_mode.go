package app

import "github.com/Gaurav-Gosain/tuios/internal/config"

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

func (m *OS) settleNest() {
	if m.nest && m.Mode != TerminalMode && !m.modeBorrowed() {
		m.nest = false
		m.MarkAllDirty()
	}
}

func (m *OS) modeBorrowed() bool {
	return len(m.mode.leases) > 0 ||
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

func (m *OS) nestLabel() string {
	return "NEST " + config.KeyLabel(m.Settings.GetNestExitKey())
}

func (m *OS) modePillShown() bool {
	if m.Settings.DockbarPosition == "hidden" {
		return false
	}
	m.ensureDockPlan()
	return m.dockPlan.Has(config.DockComponentMode)
}
