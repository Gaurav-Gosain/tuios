package app

import (
	tea "charm.land/bubbletea/v2"
)

// The agent switch on the client: [agents] enabled = false.
//
// With the agent features off the client shows none of their chrome. The
// rail has no agents section, and the layout closes up over it. The Inbox,
// agent mail and the agent alerts stay shut, and the Inbox watcher does not
// run. The prefix menu, the palette, the help and the settings leave out
// their agent lines, through agentsSeen. A key bound to an agent action
// shows one line that says the features are off, and does nothing else.
//
// The daemon reads the same file and turns off its own half: detection, the
// agent verbs and the hooks. See session/agents_switch.go.

// agentsOffNotice is the line a key or a click on an agent feature shows
// while the features are off.
const agentsOffNotice = "Agent features are off. Turn them on in Settings."

// agentsOn reports whether the agent features are on in this client's
// config. A client with no config has them on, the default.
func (m *OS) agentsOn() bool {
	return m.UserConfig == nil || m.UserConfig.Agents.On()
}

// refuseAgentsOff shows the notice and reports true while the agent features
// are off. An agent action calls it first and stops when it is true.
func (m *OS) refuseAgentsOff() bool {
	if m.agentsOn() {
		return false
	}
	m.ShowNotification(agentsOffNotice, "info", m.Settings.NotificationDuration)
	return true
}

// applyAgentsSwitch brings the client in line with the switch after the
// config changed: from the settings page, or from the file. Off closes the
// Inbox and the mailbox, stops the Inbox watcher and drops what it held. On
// starts the watcher again. A call that changes nothing does nothing.
func (m *OS) applyAgentsSwitch() tea.Cmd {
	off := !m.agentsOn()
	if !off {
		m.agentsSwitchedOff = false
		// A client that started with the features off never started the
		// watcher, so on means start it whenever it is not running, not
		// only when the switch moved.
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
	m.Inbox.Live = false
	m.AgentMail.Messages = nil
	return nil
}
