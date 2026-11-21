package app

import (
	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// InboxSettle settles the selected agent, or the marked ones, or takes a
// settled one off the shelf. handled is false with nothing selected. A row with no agent says why.
func (m *OS) InboxSettle() (tea.Cmd, bool) {
	if m.InboxAgentsMarks() > 0 {
		return m.bulkSettleCmd(), true
	}
	r, ok := m.cockpitSelected()
	if !ok {
		return nil, false
	}
	if r.agent < 0 {
		m.ShowNotification("Only an agent can be settled. Dismiss or snooze this row.", "warning", m.Settings.NotificationDuration)
		return nil, true
	}
	e := m.Inbox.ck.agents[r.agent]
	if why := m.settleRefusal(e, r); why != "" {
		m.ShowNotification(why, "info", m.Settings.NotificationDuration)
		return nil, true
	}
	if m.refuseRemoteAnswer("Settling") || !m.inboxCanMark("Settling") {
		return nil, true
	}
	if e.Settled {
		return m.settleCmd(e, "unsettle", ""), true
	}
	return m.settleCmd(e, "settle", ""), true
}

func (m *OS) settleRefusal(e sidebarAgentEntry, r ckRow) string {
	switch {
	case e.Host != "":
		return "This agent is on " + e.Host + ". Attach to " + e.Host + " to settle its agents."
	case e.Settled:
		return ""
	case r.group == ckSettled:
		return "This agent is at rest, so it is on the Settled shelf already."
	case r.group == ckNeeds:
		return "This agent needs you. Answer it or dismiss it, then settle it."
	}
	return ""
}

func (m *OS) settleCmd(e sidebarAgentEntry, action, item string) tea.Cmd {
	params := map[string]any{"action": action, "session": e.SessionID, "window": e.WindowID}
	if item != "" {
		params["id"] = item
	}
	return m.inboxMarkCmd(params, agentName(e), "")
}

func (m *OS) bulkSettleCmd() tea.Cmd {
	if m.refuseBulkRemote() || !m.inboxCanMark("Settling") {
		return nil
	}
	shown := map[string]ckRow{}
	for _, r := range m.cockpitRows() {
		if r.agent >= 0 {
			shown[agentKey(m.Inbox.ck.agents[r.agent])] = r
		}
	}
	var cmds []tea.Cmd
	left := 0
	for _, e := range m.markedAgents() {
		if e.Settled || m.settleRefusal(e, shown[agentKey(e)]) != "" {
			left++
			continue
		}
		cmds = append(cmds, m.settleCmd(e, "settle", ""))
	}
	if len(cmds) == 0 {
		m.ShowNotification("None of the marked agents can be settled. An agent that needs you is answered first.", "warning", m.Settings.NotificationDuration)
		return nil
	}
	if left > 0 {
		m.ShowNotification("An agent that needs you is not settled. Answer it first.", "warning", m.Settings.NotificationDuration)
	}
	m.Inbox.agents.marks = nil
	m.Inbox.agents.marksGen++
	return tea.Batch(cmds...)
}

func settleUndoKey(sessionName, window string) string {
	return "settle\x00" + sessionName + "\x00" + window
}

func (m *OS) noteSettled(msg InboxMarkedMsg) {
	m.rememberInboxUndo(settleUndoKey(msg.Session, msg.Window), msg.Who)
	if n := len(m.Inbox.life.undo); n > 0 {
		u := &m.Inbox.life.undo[n-1]
		u.session, u.window, u.item = msg.Session, msg.Window, msg.ID
	}
	m.showUndoOffer("Settled " + msg.Who + ". " + m.inboxKeyOr(config.ActionInboxUndo, "u") + " undoes.")
}

func (m *OS) settleLabel(r ckRow) string {
	if r.agent < 0 {
		return ""
	}
	e := m.Inbox.ck.agents[r.agent]
	switch {
	case e.Host != "":
		return ""
	case e.Settled:
		return "unsettle"
	case m.settleRefusal(e, r) == "":
		return "settle"
	}
	return ""
}

func (m *OS) drawSettleButton(c *ckCanvas, l ckLook, r ckRow, from, pill, y, top int) {
	label := m.settleLabel(r)
	key := m.inboxKey(config.ActionInboxSettle)
	if label == "" || key == "" {
		return
	}
	pal := l.pal
	key, label = " "+key+" ", " "+capitalize(label)+" "
	x0 := pill - 2 - ckWidth(ckSeg{t: key + label})
	if x0 < from+3 {
		return
	}
	x := c.text(x0, y, 0, ckSeg{t: key, fg: overlay.Readable(pal.AccentBright, pal.Card), bg: pal.Card, attrs: ckBold},
		ckSeg{t: label, fg: overlay.Readable(pal.Fg, l.cursor), bg: l.cursor})
	c.mark(pal, x0, y, x-x0, 1, ckOn)
	m.Inbox.ck.hits = append(m.Inbox.ck.hits, ckHit{x0: x0, y0: top + y, x1: x, y1: top + y + 1, kind: ckHitAction, action: "settle"})
}
