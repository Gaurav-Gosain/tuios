package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/harness"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// The unread bit on a finished pane, herdr's best idea: "done" means the agent
// stopped AND you have not looked yet. Without it a done row is permanent green
// noise, identical whether it was reviewed an hour ago or never seen at all.
//
// The bit is derived from two events and nothing else: focusing a done pane
// sets it, and any state change out of done drops it, so the next time that
// pane finishes it is unread again. There is no daemon protocol for it because
// "has this human looked at it" is per client, not per session.
//
// done only comes from an explicit report, so the daemon also counts finished
// turns: every working-to-rest transition bumps a pane's CompletionSeq (see
// session/agent_turns.go). This client remembers the count each pane had when
// its user last focused it, and a pane at rest (idle or unknown) whose count
// has moved past that is drawn exactly as an unread done pane is. Looking at it
// records the new count, and the pane goes back to its own state on the rail.

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

// agentTransitionNotice is the word and severity a state change earns, or "" for
// a transition with nothing to say. Which of these actually reaches the user is
// the [notifications.agent] policy's decision, not this function's: working and
// idle have words here because they are configurable, and are silent by default
// because an agent starting is not news and the stall timer guesses at idle.
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

// noteAgentState folds one window's agent-state transition into the unread bit
// and hands it to the alert policy. Leaving done clears the bit; finishing under
// the user's own eyes counts as seen.
//
// It adopts the new state itself, so a caller applies the rest of the pane's
// agent fields first and lets this one land last. That ordering is what lets an
// alert read the message and harness that arrived with the state rather than the
// ones it replaced.
func (m *OS) noteAgentState(w *terminal.Window, to string) {
	if w == nil || w.AgentState == to {
		return
	}
	from := w.AgentState
	w.AgentState = to
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
	// A turn that finished under the user's own eyes has been seen.
	if focused {
		m.markAgentSeenSeq(w.ID, w.AgentCompletionSeq)
	}

	m.considerAgentAlert(w, from, to)
}

// markFocusedAgentSeen clears the unread bit of the window being focused, which
// is every route into a pane (click, rail, palette, notification jump) since
// they all land in FocusWindow.
func (m *OS) markFocusedAgentSeen(i int) {
	if i < 0 || i >= len(m.Windows) {
		return
	}
	w := m.Windows[i]
	if w == nil {
		return
	}
	if w.AgentState == "done" {
		m.markAgentSeen(w.ID)
	}
	m.markAgentSeenSeq(w.ID, w.AgentCompletionSeq)
}

// markAgentSeenSeq records that a pane was looked at with seq turns finished.
// Guarded by the current value, like markAgentSeen, so refocusing costs
// nothing.
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

// agentFinishedUnread reports whether a pane at rest finished a turn this
// client's user has not looked at.
func (m *OS) agentFinishedUnread(windowID, state string, seq uint64) bool {
	if seq == 0 || (state != "idle" && state != "unknown") {
		return false
	}
	return seq > m.SidebarAgentSeenSeq[windowID]
}

// railAgentState is the state and unread bit the rail draws for a pane: a
// pane with an ask-human question open reads as needs_input, an unread
// finished turn as an unread done, and anything else as itself.
func (m *OS) railAgentState(windowID, state string, seq uint64) (string, bool) {
	if _, ok := m.paneAsk(windowID); ok {
		return "needs_input", false
	}
	if m.agentFinishedUnread(windowID, state, seq) {
		return "done", false
	}
	return state, m.agentSeen(windowID)
}

// paneAsk is the ask-human question a pane of this machine has open in the
// Inbox, if it has one.
//
// Asking moves no agent state: ask-human is often run by a script with no
// agent in the pane at all. So the question was in the Inbox and the dock,
// and the pane that asked it had no mark on the rail, no row in the agents
// section and no match for "@n" in the palette. Every surface that reads a
// pane's state through railAgentState now reads it as needs_input while the
// question is open, which is what it is: the pane is waiting for the person.
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

// paneAskNote is what the rail's row for a pane says when the pane has an
// ask-human question open and reported nothing of its own: the question, as a
// question.
func (m *OS) paneAskNote(windowID, message, kind string) (string, string) {
	it, ok := m.paneAsk(windowID)
	if !ok || message != "" {
		return message, kind
	}
	return it.Summary, harness.PromptKindQuestion
}
