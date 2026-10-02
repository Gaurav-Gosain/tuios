//go:build slim

package app

import tea "charm.land/bubbletea/v2"

// tuios-slim has no hosts, so the settings page has no Hosts category. This
// stands in for settings_hosts.go. slimHidden leaves the empty category out.

func (m *OS) hostsCategory() settingsCategory { return settingsCategory{Name: "Hosts"} }

func (m *OS) applyHostsCmd() tea.Cmd { return nil }

// HostTestDoneMsg and HostApplyFailedMsg are never sent in tuios-slim.
type (
	HostTestDoneMsg    struct{}
	HostApplyFailedMsg struct {
		Host string
		Err  error
	}
)

func (m *OS) applyHostTest(HostTestDoneMsg) {}
