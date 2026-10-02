//go:build !slim

package app

import (
	"os"
	"runtime"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/integration"
)

// agentsSeen reports whether an agent has been seen: now or before in this
// client (the flag is persisted with the rail's state), in any session of the
// daemon this client can see, or through an agent integration installed on
// this machine.
func (m *OS) agentsSeen() bool {
	return m.SidebarAgentsSeen || m.agentIntegrationInstalled || m.agentsPresent()
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
