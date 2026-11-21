package app

import (
	"slices"

	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
)

type sidebarRevealState struct {
	WindowID  string
	SessionID string
	ScrollT   int
	ScrollS   int
	Valid     bool
}

func (m *OS) sidebarRevealFocus(sessions, sessionRows []sessiontree.Node, terminals []sidebarTerminalEntry, rowsT, rowsS int) {
	r := m.sidebarReveal
	focused := m.sidebarFocusedWindowID()
	attached := m.sidebarCurrentSessionID()
	if focused != "" && (!r.Valid || focused != r.WindowID) && (!r.Valid || m.SidebarScroll[sidebarSectionTerminals] == r.ScrollT) {
		for i, e := range terminals {
			if e.WindowID == focused {
				m.SidebarScroll[sidebarSectionTerminals] = sidebarRevealOffset(m.SidebarScroll[sidebarSectionTerminals], i, rowsT, len(terminals))
				break
			}
		}
	}
	if (!r.Valid || attached != r.SessionID) && (!r.Valid || m.SidebarScroll[sidebarSectionSessions] == r.ScrollS) {
		idx := m.sidebarAttachedRow(sessions, sessionRows, attached)
		m.SidebarScroll[sidebarSectionSessions] = sidebarRevealOffset(m.SidebarScroll[sidebarSectionSessions], idx, rowsS, len(sessionRows))
	}
}

func (m *OS) sidebarAttachedRow(sessions, rows []sessiontree.Node, attached string) int {
	isAttached := func(n sessiontree.Node) bool {
		return n.Kind == sessiontree.KindSession && n.ID == attached && !isRemoteNode(n)
	}
	if i := slices.IndexFunc(rows, isAttached); i >= 0 {
		return i
	}
	at := slices.IndexFunc(sessions, isAttached)
	if at < 0 {
		return -1
	}
	node := sessions[at]
	if node.Worktree != nil {
		if i := slices.IndexFunc(rows, func(n sessiontree.Node) bool {
			return sidebarRepoOfRow(n) == node.Worktree.Repo && n.Space == node.Space
		}); i >= 0 {
			return i
		}
	}
	host := m.attachedMachine()
	if isGlobalSessionRow(node) {
		host = GlobalSessionName
	}
	return slices.IndexFunc(rows, func(n sessiontree.Node) bool {
		return n.Kind == sessiontree.KindHost && n.Host == host
	})
}

func (m *OS) sidebarRecordReveal() {
	m.sidebarReveal = sidebarRevealState{
		WindowID:  m.sidebarFocusedWindowID(),
		SessionID: m.sidebarCurrentSessionID(),
		ScrollT:   m.SidebarScroll[sidebarSectionTerminals],
		ScrollS:   m.SidebarScroll[sidebarSectionSessions],
		Valid:     true,
	}
}

func (m *OS) sidebarFocusedWindowID() string {
	if w := m.GetFocusedWindow(); w != nil {
		return w.ID
	}
	return ""
}

func sidebarRevealOffset(scroll, idx, rows, total int) int {
	if rows <= 0 || total <= rows || idx < 0 || idx >= total {
		return scroll
	}
	maxScroll := total - rows
	scroll = max(min(scroll, maxScroll), 0)
	if idx < scroll {
		return idx
	}
	visible := rows
	if scroll < maxScroll {
		visible = rows - 1
	}
	if idx < scroll+visible {
		return scroll
	}
	return max(min(idx-rows+2, maxScroll), 0)
}
