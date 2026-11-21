package app

import (
	"slices"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

func (m *OS) agentSeen(windowID string) bool {
	return m.SidebarAgentSeen[windowID]
}

// RailAgentState is the state the rail and the agents view show for a pane, and
// whether its finished turn has been seen: railAgentState, for a renderer that
// draws them from this model (tuios gui-bridge).
func (m *OS) RailAgentState(windowID, state string, seq uint64) (string, bool) {
	return m.railAgentState(windowID, state, seq)
}

// AgentSeenWindows lists the panes whose finished turn has been seen, in every
// session: the same bits the agents view groups other sessions' panes by.
func (m *OS) AgentSeenWindows() []string {
	out := make([]string, 0, len(m.SidebarAgentSeen))
	for id, seen := range m.SidebarAgentSeen {
		if seen {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return out
}

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

func agentTransitionNotice(to string) (string, string) {
	switch to {
	case "needs_input":
		return sidebarStateWords(to), "warning"
	case "errored":
		return "errored", "error"
	case "done":
		return sidebarStateWords(to), "success"
	case "working":
		return "working", "info"
	case "idle":
		return "idle", "info"
	}
	return "", ""
}

func (m *OS) noteAgentState(w *terminal.Window, to string) {
	if w == nil || w.AgentState == to {
		return
	}
	from := w.AgentState
	w.AgentState = to
	m.noteAgentsSeen()
	focused := m.GetFocusedWindow() == w

	switch {
	case to != "done":
		if m.SidebarAgentSeen[w.ID] {
			delete(m.SidebarAgentSeen, w.ID)
			m.saveSidebarState()
		}
	case focused:
		m.markAgentSeen(w.ID)
	}
	if focused {
		m.markAgentSeenSeq(w.ID, w.AgentCompletionSeq)
	}
	m.noteThreadViewedLive(w)

	m.considerAgentAlert(w, from, to)
}

func (m *OS) noteAgentTurnWithin(w *terminal.Window) {
	if w == nil {
		return
	}
	m.noteAgentsSeen()
	if m.GetFocusedWindow() == w {
		if w.AgentState == "done" {
			m.markAgentSeen(w.ID)
		}
		m.markAgentSeenSeq(w.ID, w.AgentCompletionSeq)
	} else if m.SidebarAgentSeen[w.ID] {
		delete(m.SidebarAgentSeen, w.ID)
		m.saveSidebarState()
	}
	m.noteThreadViewedLive(w)
	m.considerAgentAlert(w, "working", w.AgentState)
}

func (m *OS) markFocusedAgentSeen(i int) {
	if i < 0 || i >= len(m.Windows) {
		return
	}
	w := m.Windows[i]
	if w == nil {
		return
	}
	m.noteAgentReturn(w)
	if w.AgentState == "done" {
		m.markAgentSeen(w.ID)
	}
	m.markAgentSeenSeq(w.ID, w.AgentCompletionSeq)
	m.markAgentSeenAt(w)
}

func (m *OS) markSwitchSeen() {
	if !m.switchSeenPending {
		return
	}
	m.switchSeenPending = false
	m.markFocusedAgentSeen(m.FocusedWindow)
}

var agentSeenAtNow = func() int64 { return time.Now().UnixNano() }

func (m *OS) markAgentSeenAt(w *terminal.Window) {
	if w == nil || w.ID == "" || (w.AgentState == "" && w.AgentCompletionSeq == 0) {
		return
	}
	if m.SidebarAgentSeenAt == nil {
		m.SidebarAgentSeenAt = make(map[string]int64, 1)
	}
	m.SidebarAgentSeenAt[w.ID] = agentSeenAtNow()
	m.saveSidebarState()
}

func (m *OS) agentAwaySince(windowID string) (int64, bool) {
	at, ok := m.SidebarAgentSeenAt[windowID]
	return at, ok && at > 0
}

func (m *OS) markAgentSeenSeq(windowID string, seq uint64) {
	if windowID == "" || seq == 0 || m.SidebarAgentSeenSeq[windowID] >= seq {
		return
	}
	if m.SidebarAgentSeenSeq == nil {
		m.SidebarAgentSeenSeq = make(map[string]uint64, 1)
	}
	m.SidebarAgentSeenSeq[windowID] = seq
	m.saveSidebarState()
}

func (m *OS) agentFinishedUnread(windowID, state string, seq uint64) bool {
	if seq == 0 || (state != "idle" && state != "unknown") {
		return false
	}
	return seq > m.SidebarAgentSeenSeq[windowID]
}

func (m *OS) railAgentState(windowID, state string, seq uint64) (string, bool) {
	if _, ok := m.paneAsk(windowID); ok {
		return "needs_input", false
	}
	if m.agentFinishedUnread(windowID, state, seq) {
		return "done", false
	}
	return state, m.agentSeen(windowID)
}

func (m *OS) paneAsk(windowID string) (session.AttentionItem, bool) {
	if windowID == "" {
		return session.AttentionItem{}, false
	}
	for _, it := range m.Inbox.Items {
		if it.Kind == session.AttentionAsk && it.Host == "" && !it.Stale && it.Window == windowID {
			return it, true
		}
	}
	return session.AttentionItem{}, false
}

func (m *OS) paneAskNote(windowID, message, kind string) (string, string) {
	it, ok := m.paneAsk(windowID)
	if !ok || message != "" {
		return message, m.paneApprovalWord(windowID, kind)
	}
	return it.Summary, harness.PromptKindQuestion
}
