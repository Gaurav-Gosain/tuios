package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

const (
	workspaceMarkBell uint8 = 1 << iota
	workspaceMarkActivity
)

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
		if tabs.ActivityMarker == "" || m.Settings.Calm {
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

func (m *OS) clearViewedWorkspaceMarks() {
	if len(m.workspaceMarkSet) > 0 {
		delete(m.workspaceMarkSet, m.CurrentWorkspace)
	}
}

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

func (m *OS) workspaceWantsAttention(n int) bool {
	for _, w := range m.Windows {
		if w.Workspace == n && sidebarAttention(m.windowMarkState(w)) {
			return true
		}
	}
	return false
}

// PaneMarkMsg is the daemon's push that a pane this client does not stream
// rang the bell or printed. See session/pane_marks.go.
type PaneMarkMsg struct {
	Mark session.PaneMarkPayload
}

func (m *OS) notePaneMark(p session.PaneMarkPayload) bool {
	for _, w := range m.Windows {
		if w.PTYID == "" || w.PTYID != p.PTYID {
			continue
		}
		changed := false
		if p.Marks&session.PaneMarkBell != 0 && m.noteWorkspaceMark(w.Workspace, workspaceMarkBell) {
			changed = true
		}
		if p.Marks&session.PaneMarkOutput != 0 && !w.Minimized && m.noteWorkspaceMark(w.Workspace, workspaceMarkActivity) {
			changed = true
		}
		return changed
	}
	return false
}
