package app

func (m *OS) beginSidebarReturn() {
	m.borrowKeyboard(leaseRail)
	m.sidebarReturnWindow = ""
	if w := m.GetFocusedWindow(); w != nil {
		m.sidebarReturnWindow = w.ID
	}
}

func (m *OS) recordSidebarRow() {
	m.sidebarLastRow, m.sidebarLastRowSet = m.sidebarCursorRow()
}

func (m *OS) restoreSidebarRow() bool {
	if !m.sidebarLastRowSet {
		return false
	}
	for i, r := range m.SidebarNav {
		if sidebarNavRowsEqual(r, m.sidebarLastRow) {
			m.sidebarSetCursor(i)
			return true
		}
	}
	return false
}

func (m *OS) clearSidebarReturn() {
	m.cancelLease(leaseRail)
	m.sidebarReturnWindow = ""
}

func (m *OS) endSidebarReturn() {
	if !m.modeLeased(leaseRail) {
		return
	}
	id := m.sidebarReturnWindow
	m.sidebarReturnWindow = ""
	for i, w := range m.Windows {
		if id != "" && w.ID == id && w.Workspace == m.CurrentWorkspace && !w.Minimized {
			m.FocusWindow(i)
			break
		}
	}
	m.returnMode(leaseRail)
}
