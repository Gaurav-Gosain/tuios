package app

import (
	"os"
	"runtime"
	"slices"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/integration"
)

func (m *OS) agentsSeen() bool {
	if !m.agentsOn() {
		return false
	}
	return m.SidebarAgentsSeen || m.agentIntegrationInstalled || m.agentsPresent()
}

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

func (m *OS) noteAgentsSeen() {
	if m.SidebarAgentsSeen || !m.agentsPresent() {
		return
	}
	m.SidebarAgentsSeen = true
	m.saveSidebarState()
}

func (m *OS) prefixMenuGroups() []config.KeybindingGroup {
	multi, multiOK := m.MultiCopyEligible()
	if !multiOK {
		multi = 0
	}
	groups := config.PrefixMenuGroups(m.KeybindRegistry, "", config.MenuState{
		Daemon:    m.IsDaemonSession,
		MultiCopy: multi,
		Spotlight: m.spotlight.on,
		Minimized: -1,
	})
	seen, agentsTab := m.agentsSeen(), m.agentsPageAvailable()
	out := groups[:0]
	for _, g := range groups {
		if !seen {
			g.Bindings = slices.DeleteFunc(g.Bindings, config.IsAgentPrefixKeybinding)
		}
		if !agentsTab {
			g.Bindings = slices.DeleteFunc(g.Bindings, config.IsAgentsSettingsPrefixKeybinding)
		}
		if len(m.transfers.rows) == 0 {
			g.Bindings = slices.DeleteFunc(g.Bindings, config.IsTransfersPrefixKeybinding)
		}
		if len(g.Bindings) > 0 {
			out = append(out, g)
		}
	}
	return out
}

type agentIntegrationMsg struct{}

func (m *OS) checkAgentIntegrationCmd() tea.Cmd {
	if m.SidebarAgentsSeen || runtime.GOOS == "js" {
		return nil
	}
	return func() tea.Msg {
		env := integration.SystemEnv()
		env.HookSocket = nil
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
