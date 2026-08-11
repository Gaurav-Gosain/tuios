package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/config"
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

// agentTransitionNotice is the word and severity a state change earns on the
// dock, or "" for the transitions that are not news. working is deliberately
// silent: an agent starting is not worth interrupting for, and a message per
// start would train the user to ignore the block that also carries the errors.
func agentTransitionNotice(to string) (string, string) {
	switch to {
	case "needs_input":
		return "needs input", "warning"
	case "errored":
		return "errored", "error"
	case "done":
		return "finished", "success"
	}
	return "", ""
}

// noteAgentState folds one window's agent-state transition into the unread bit
// and, when it wants a human, onto the dock as a message that jumps back here.
// Leaving done clears the bit; finishing under the user's own eyes counts as
// seen and says nothing, since a pane you are already looking at has nothing
// left to announce.
func (m *OS) noteAgentState(w *terminal.Window, to string) {
	if w == nil || w.AgentState == to {
		return
	}
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
		return
	}
	if word, sev := agentTransitionNotice(to); word != "" {
		name := printableTitle(m.railTitleShown(w))
		if name == "" {
			name = "pane"
		}
		m.ShowNotificationFrom(name+" "+word, sev, config.NotificationDuration,
			NotifTarget{SessionID: m.sidebarCurrentSessionID(), WindowID: w.ID})
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
