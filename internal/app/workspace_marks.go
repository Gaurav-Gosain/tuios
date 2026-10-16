package app

import "github.com/Gaurav-Gosain/tuios/internal/config"

// Marks on the dock's workspace tabs: a bell or output on a workspace you are
// not looking at, as tmux and screen flag them in their window lists. Each is
// kept only while [dock.tabs] names a marker for it, so a dock that draws
// none keeps no record and does no work. The marks of the workspace in view
// are dropped: you are looking at it.

// The kinds of workspace mark, as bits.
const (
	workspaceMarkBell uint8 = 1 << iota
	workspaceMarkActivity
)

// noteWorkspaceMark records a mark on workspace ws, and reports whether it is
// new, so the caller repaints once and not on every output.
func (m *OS) noteWorkspaceMark(ws int, mark uint8) bool {
	if ws <= 0 || ws == m.CurrentWorkspace {
		return false
	}
	tabs := m.dockTabs()
	switch mark {
	case workspaceMarkBell:
		if tabs.BellMarker == "" {
			return false
		}
	case workspaceMarkActivity:
		if tabs.ActivityMarker == "" {
			return false
		}
	}
	if m.workspaceMarkSet[ws]&mark != 0 {
		return false
	}
	if m.workspaceMarkSet == nil {
		m.workspaceMarkSet = map[int]uint8{}
	}
	m.workspaceMarkSet[ws] |= mark
	return true
}

// noteWindowBell records a bell from the window id on its workspace.
func (m *OS) noteWindowBell(id string) {
	for _, w := range m.Windows {
		if w.ID == id {
			if m.noteWorkspaceMark(w.Workspace, workspaceMarkBell) {
				m.MarkAllDirty()
			}
			return
		}
	}
}

// clearViewedWorkspaceMarks drops the marks of the workspace in view.
func (m *OS) clearViewedWorkspaceMarks() {
	if len(m.workspaceMarkSet) > 0 {
		delete(m.workspaceMarkSet, m.CurrentWorkspace)
	}
}

// workspaceMarks is what workspace n's tab carries after its label: the bell
// marker, then the activity marker, each when there is one to show.
func (m *OS) workspaceMarks(tabs *config.DockTabsConfig, n int) string {
	bits := m.workspaceMarkSet[n]
	if bits == 0 || n == m.dockWorkspace() {
		return ""
	}
	out := ""
	if bits&workspaceMarkBell != 0 {
		out += tabs.BellMarker
	}
	if bits&workspaceMarkActivity != 0 {
		out += tabs.ActivityMarker
	}
	return out
}

// workspaceWantsAttention reports whether a pane on workspace n waits for a
// person: an agent asking for input or stopped on an error.
func (m *OS) workspaceWantsAttention(n int) bool {
	for _, w := range m.Windows {
		if w.Workspace == n && sidebarAttention(m.windowMarkState(w)) {
			return true
		}
	}
	return false
}
