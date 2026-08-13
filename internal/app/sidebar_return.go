package app

// The rail borrows the keyboard, not the user's place in the panes. Entering it
// records the mode and the focused pane so leaving it hands both back: browsing
// the sessions is not a request to stop typing, any more than moving a pane is
// (BeginResizeMode and CtrlDragWasTerminal, the same bargain).
//
// A rail action that deliberately relocates the user disarms the return, and
// what it chose stands. There are three: focusing a pane from a window row,
// attaching to another session, and creating one. Everything else in the rail
// is a browse, and a browse is what esc undoes.

// beginSidebarReturn records what the rail is borrowing from.
func (m *OS) beginSidebarReturn() {
	m.sidebarReturnArmed = true
	m.sidebarReturnMode = m.Mode
	m.sidebarReturnWindow = ""
	if w := m.GetFocusedWindow(); w != nil {
		m.sidebarReturnWindow = w.ID
	}
}

// clearSidebarReturn drops the record, leaving the user where the rail put them.
func (m *OS) clearSidebarReturn() {
	m.sidebarReturnArmed = false
	m.sidebarReturnWindow = ""
}

// endSidebarReturn hands back the borrowed mode and pane. Idempotent: every path
// out of the rail runs it, and only the first has anything to do. The pane is
// restored by ID rather than index, since the rail can close or reorder panes
// while it holds the keyboard.
func (m *OS) endSidebarReturn() {
	if !m.sidebarReturnArmed {
		return
	}
	id := m.sidebarReturnWindow
	m.clearSidebarReturn()

	m.Mode = m.sidebarReturnMode
	if id == "" {
		return
	}
	for i, w := range m.Windows {
		if w.ID == id && w.Workspace == m.CurrentWorkspace && !w.Minimized {
			m.FocusWindow(i)
			return
		}
	}
	// The pane is gone (closed, minimized, or on another workspace now). Window
	// management is the mode that can go find another one, so a terminal mode
	// with nothing to type into is not handed back.
	if m.Mode == TerminalMode && m.GetFocusedWindow() == nil {
		m.Mode = WindowManagementMode
	}
}
