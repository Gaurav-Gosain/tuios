package app

// The rail borrows the keyboard, not the user's place in the panes. Entering it
// records the mode and the focused pane so leaving it hands both back: browsing
// the sessions is not a request to stop typing, any more than moving a pane is
// (BeginPointerGesture and CtrlDragWasTerminal, the same bargain).
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

// The rail remembers its own place the same way, and for the same reason. Enter
// on a terminal row hands the keyboard to that pane, and coming back landed the
// cursor on the attached session's row: the user was three rows into the
// terminals section, went to a pane, came back, and had to walk down again. The
// row the rail was left on is part of what the rail borrowed.

// recordSidebarRow saves the row the cursor was on as the rail gives the
// keyboard back.
func (m *OS) recordSidebarRow() {
	m.sidebarLastRow, m.sidebarLastRowSet = m.sidebarCursorRow()
}

// restoreSidebarRow puts the cursor back on the row the rail was last left on,
// and reports whether it could. It refuses on a row the current frame no longer
// draws (a pane closed, a session gone, the agents filter changed under it), so
// the caller can fall back to landing on the attached session.
func (m *OS) restoreSidebarRow() bool {
	if !m.sidebarLastRowSet {
		return false
	}
	for i, r := range m.SidebarNav {
		if sidebarNavRowsEqual(r, m.sidebarLastRow) {
			// The next render re-anchors by identity off the cursor, so setting the
			// index is enough; no follow request of its own is needed.
			m.sidebarSetCursor(i)
			return true
		}
	}
	return false
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
