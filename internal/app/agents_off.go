package app

import (
	tea "charm.land/bubbletea/v2"
)

const agentsOffNotice = "Agent features are off. Turn them on in Settings."

func (m *OS) agentsOn() bool {
	return m.UserConfig == nil || m.UserConfig.Agents.On()
}

func (m *OS) refuseAgentsOff() bool {
	if m.agentsOn() {
		return false
	}
	m.ShowNotification(agentsOffNotice, "info", m.Settings.NotificationDuration)
	return true
}

func (m *OS) applyAgentsSwitch() tea.Cmd {
	off := !m.agentsOn()
	if !off {
		m.agentsSwitchedOff = false
		if m.inboxEvents == nil {
			m.MarkAllDirty()
			return m.startInboxWatch()
		}
		return nil
	}
	if m.agentsSwitchedOff {
		return nil
	}
	m.agentsSwitchedOff = true
	m.MarkAllDirty()
	if m.ShowInbox {
		m.CloseInbox()
	}
	if m.ShowAgentMail {
		m.CloseAgentMail()
	}
	m.endInboxWatch()
	m.inboxEvents = nil
	m.Inbox.Items = nil
	m.Inbox.ck.cards = nil
	m.Inbox.Live = false
	m.AgentMail.Messages = nil
	return nil
}
