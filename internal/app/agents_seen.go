package app

import (
	"os"
	"runtime"
	"slices"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/integration"
)

// Some chrome only means something to a person who runs agents: the prefix
// menu's Inbox lines, the palette's agent entries and its "@ state" hint, and
// the agent rows of the Alerts settings. None of it is removed. It waits until
// an agent has been seen, so a person who never runs one is not shown controls
// for something that is not there.

// agentsSeen reports whether an agent has been seen: now or before in this
// client (the flag is persisted with the rail's state), in any session of the
// daemon this client can see, or through an agent integration installed on
// this machine.
func (m *OS) agentsSeen() bool {
	return m.SidebarAgentsSeen || m.agentIntegrationInstalled || m.agentsPresent()
}

// agentsPresent reports whether anything an agent leaves behind is in view
// right now: a pane with an agent state or a named harness in the attached
// session or in any session the client has listed, an Inbox item, or mail.
// Reads only what the client already holds; nothing here asks the daemon.
func (m *OS) agentsPresent() bool {
	for _, w := range m.Windows {
		if w != nil && (w.AgentState != "" || w.AgentHarness != "") {
			return true
		}
	}
	if len(m.Inbox.Items) > 0 || len(m.AgentMail.Messages) > 0 {
		return true
	}
	if c := m.DaemonClient; c != nil {
		for _, name := range c.AvailableSessionNames() {
			for _, w := range c.SessionWindows(name) {
				if w.AgentState != "" || w.AgentHarness != "" {
					return true
				}
			}
		}
	}
	return false
}

// noteAgentsSeen persists the flag the first time an agent is in view. Called
// from the paths an agent's state, an Inbox item or mail arrives by, so the
// render path never writes the state file.
func (m *OS) noteAgentsSeen() {
	if m.SidebarAgentsSeen || !m.agentsPresent() {
		return
	}
	m.SidebarAgentsSeen = true
	m.saveSidebarState()
}

// prefixMenuBindings is the which-key menu after the prefix key. The Inbox's
// three lines wait until an agent has been seen; the keys work either way.
func (m *OS) prefixMenuBindings() []config.Keybinding {
	bindings := config.GetPrefixKeybindings("", m.IsDaemonSession)
	if !m.agentsSeen() {
		bindings = slices.DeleteFunc(bindings, config.IsAgentPrefixKeybinding)
	}
	return bindings
}

// agentIntegrationMsg reports that a harness on this machine has tuios's
// hooks installed.
type agentIntegrationMsg struct{}

// checkAgentIntegrationCmd looks, once and off the UI goroutine, for an agent
// integration installed with `tuios integration install`. A harness whose
// configuration directory does not exist is skipped with one stat, so a
// machine with no agents on it pays a handful of stats at start.
func (m *OS) checkAgentIntegrationCmd() tea.Cmd {
	if m.SidebarAgentsSeen || runtime.GOOS == "js" {
		return nil
	}
	return func() tea.Msg {
		env := integration.SystemEnv()
		for _, t := range integration.Targets() {
			if fi, err := os.Stat(t.ConfigDir(env)); err != nil || !fi.IsDir() {
				continue
			}
			if t.Status(env, "tuios").Installed {
				return agentIntegrationMsg{}
			}
		}
		return nil
	}
}
