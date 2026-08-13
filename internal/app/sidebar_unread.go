package app

import (
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
		return "needs input", "warning"
	case "errored":
		return "errored", "error"
	case "done":
		return "finished", "success"
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

	m.considerAgentAlert(w, from, to)
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
