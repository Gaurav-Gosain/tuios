package app

// The unread bit on a finished pane, herdr's best idea: "done" means the agent
// stopped AND you have not looked yet. Without it a done row is permanent green
// noise, identical whether it was reviewed an hour ago or never seen at all.
//
// The bit is derived from two events and nothing else: focusing a done pane
// sets it, and any state change out of done drops it, so the next time that
// pane finishes it is unread again. There is no daemon protocol for it because
// "has this human looked at it" is per client, not per session.

// agentSeen reports whether a finished pane has already been looked at.
func (m *OS) agentSeen(windowID string) bool {
	return m.SidebarAgentSeen[windowID]
}

// markAgentSeen records a look at a finished pane. The write to disk is guarded
// by the current value, so walking a focus chain over already-seen panes costs
// nothing.
func (m *OS) markAgentSeen(windowID string) {
	if windowID == "" || m.SidebarAgentSeen[windowID] {
		return
	}
	if m.SidebarAgentSeen == nil {
		m.SidebarAgentSeen = make(map[string]bool, 1)
	}
	m.SidebarAgentSeen[windowID] = true
	m.saveSidebarState()
}

// noteAgentState folds one window's agent-state transition into the unread bit.
// Leaving done clears it; finishing under the user's own eyes counts as seen,
// since a pane you are already looking at has nothing left to announce.
func (m *OS) noteAgentState(windowID, from, to string, focused bool) {
	if from == to {
		return
	}
	switch {
	case to != "done":
		if m.SidebarAgentSeen[windowID] {
			delete(m.SidebarAgentSeen, windowID)
			m.saveSidebarState()
		}
	case focused:
		m.markAgentSeen(windowID)
	}
}

// markFocusedAgentSeen clears the unread bit of the window being focused, which
// is every route into a pane (click, rail, palette, notification jump) since
// they all land in FocusWindow.
func (m *OS) markFocusedAgentSeen(i int) {
	if i < 0 || i >= len(m.Windows) {
		return
	}
	if w := m.Windows[i]; w != nil && w.AgentState == "done" {
		m.markAgentSeen(w.ID)
	}
}
