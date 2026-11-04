package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/agentseen"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// The unseen mark on an agent's row in the agents view: its agent finished or
// failed after the person last had its thread on screen. The rule is
// agentseen.Unseen. The watermark is the agent's state-entry time the thread
// was last viewed at, kept by window ID in SidebarAgentViewed and persisted in
// the sidebar state file, so a client restart keeps it.
//
// It is this client's, like the rail's seen bits beside it, and not the
// daemon's: what one person has looked at is not what another has (see
// session/agent_turns.go). Viewing a thread moves only this watermark, not the
// rail's seen bit, so a row stays in its group while the cursor passes over it.
//
// It costs nothing at idle: the mark moves on a state change and on a view,
// both of which already run on the Update goroutine.

// agentRowUnseen reports whether an agents-view row draws the unseen mark.
// Another machine's agent has no thread here, so it never does.
func (m *OS) agentRowUnseen(e sidebarAgentEntry) bool {
	if e.Host != "" || e.WindowID == "" {
		return false
	}
	return agentseen.Unseen(e.State, e.DoneSeen, e.StateAt, m.SidebarAgentViewed[e.WindowID])
}

// markAgentViewed records that the person has an agent's thread on screen.
// Only a row that draws the mark writes, so moving the cursor over rows that
// have nothing new costs a map read each.
func (m *OS) markAgentViewed(e sidebarAgentEntry) {
	if !m.agentRowUnseen(e) {
		return
	}
	m.setAgentViewed(e.WindowID, e.StateAt)
}

// setAgentViewed moves a window's watermark to at.
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

// noteThreadViewedLive moves the watermark of a pane whose thread is on
// screen as its state changes: an agent that finishes while the person
// watches its thread has been seen.
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
