package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/agentseen"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

func (m *OS) agentRowUnseen(e sidebarAgentEntry) bool {
	if e.Host != "" || e.WindowID == "" {
		return false
	}
	return agentseen.Unseen(e.State, e.DoneSeen, e.StateAt, m.SidebarAgentViewed[e.WindowID])
}

func (m *OS) markAgentViewed(e sidebarAgentEntry) {
	if !m.agentRowUnseen(e) {
		return
	}
	m.setAgentViewed(e.WindowID, e.StateAt)
}

func (m *OS) setAgentViewed(windowID string, at int64) {
	if m.SidebarAgentViewed[windowID] >= at {
		return
	}
	if m.SidebarAgentViewed == nil {
		m.SidebarAgentViewed = make(map[string]int64, 1)
	}
	m.SidebarAgentViewed[windowID] = at
	m.saveSidebarState()
	m.cockpitTouch()
}

func (m *OS) noteThreadViewedLive(w *terminal.Window) {
	if w == nil || !m.ShowInbox || !m.threadShown() {
		return
	}
	e, ok := m.cockpitAgent()
	if !ok || e.Host != "" || e.WindowID != w.ID || e.SessionID != m.sidebarCurrentSessionID() {
		return
	}
	state, seen := m.railAgentState(w.ID, w.AgentState, w.AgentCompletionSeq)
	if agentseen.Unseen(state, seen, w.AgentStateAt, m.SidebarAgentViewed[w.ID]) {
		m.setAgentViewed(w.ID, w.AgentStateAt)
	}
}
