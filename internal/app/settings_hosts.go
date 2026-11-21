package app

import (
	"context"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

const hostAddRowLabel = "Add a machine"

const hostTestRowLabel = "Test the links"

// HostTestDoneMsg carries the result of the settings page's link test back to
// the Update goroutine. The dialing happened in the Cmd that produced it.
type HostTestDoneMsg struct {
	Reports []federation.HostReport
}

func (m *OS) hostsCategory() settingsCategory {
	names := m.configuredHostNames()
	items := make([]settingItem, 0, len(names)+2)
	for _, name := range names {
		items = append(items, m.hostItem(name))
	}
	items = append(items, m.hostAddItem(), m.hostTestItem())
	return settingsCategory{Name: "Hosts", Items: items}
}

func (m *OS) configuredHostNames() []string {
	if m.UserConfig == nil || len(m.UserConfig.Hosts) == 0 {
		return nil
	}
	names := make([]string, 0, len(m.UserConfig.Hosts))
	for name := range m.UserConfig.Hosts {
		if name == config.LinkPolicyDefaultName {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (m *OS) hostItem(name string) settingItem {
	return settingItem{
		Label:       name,
		Desc:        m.hostRowStatus(name),
		Control:     controlString,
		Placeholder: "user@machine",
		Unset:       "(no address)",
		value: func(m *OS) string {
			if m.UserConfig == nil {
				return ""
			}
			return m.UserConfig.Hosts[name].Addr
		},
		setStr: func(m *OS, v string) {
			if strings.TrimSpace(v) == "" {
				m.removeHost(name)
				return
			}
			m.setHostAddr(name, v)
		},
	}
}

func (m *OS) hostRowStatus(name string) string {
	if r, ok := m.hostTests[name]; ok {
		return hostStateSentence(string(r.Status), r.Reason, r.Detail) +
			" Press enter to change the address. Clear it to remove the host."
	}
	for _, h := range m.FederationHosts {
		if h.Name != name {
			continue
		}
		return hostStateSentence(h.Status, h.Reason, "") +
			" Press enter to change the address. Clear it to remove the host."
	}
	return "The daemon has not reported this link yet. Press enter to change the address. Clear it to remove the host."
}

func hostStateSentence(status, reason, detail string) string {
	head := "The link state is " + status + "."
	switch federation.Status(status) {
	case federation.StatusUp:
		head = "The host answers."
	case federation.StatusNoDaemon:
		head = "The host is up and no tuios daemon runs on it."
	case federation.StatusNoBinary:
		head = "The host is up and the link cannot find tuios on it."
	case federation.StatusUnreachable:
		head = "The host does not answer."
	case federation.StatusIncompatible:
		head = "The host runs a tuios this one cannot talk to."
	case federation.StatusConnecting:
		head = "The link is starting."
	case federation.StatusReconnecting:
		head = "The link dropped and tuios is connecting again."
	case federation.StatusApproval:
		head = federation.SignInSentence
	}
	parts := []string{head}
	if strings.HasPrefix(reason, head) {
		parts = []string{reason}
	} else if reason != "" {
		parts = append(parts, reason)
	}
	if detail != "" {
		parts = append(parts, "The link reported: "+detail)
	}
	return strings.Join(parts, " ")
}

func (m *OS) hostAddItem() settingItem {
	return settingItem{
		Label:   hostAddRowLabel,
		Desc:    "Test a machine over ssh, install tuios there, and add it. Press enter.",
		Control: controlEnum,
		Options: []string{"open"},
		value:   func(_ *OS) string { return "open" },
		activate: func(m *OS) tea.Cmd {
			return m.OpenMachineSheet("")
		},
	}
}

const maxHostAddrCandidates = 8

func (m *OS) hostAddrCandidates() []string {
	if m.ConfigReadOnly {
		return nil
	}
	aliases := federation.ReadSSHAliases(federation.UserSSHConfigPath())
	if len(aliases) > maxHostAddrCandidates {
		aliases = aliases[:maxHostAddrCandidates]
	}
	return aliases
}

func tailnetOptionsFromFile() (federation.TailnetOptions, bool) {
	path, err := config.GetConfigPath()
	if err != nil {
		return federation.DefaultTailnetOptions(), false
	}
	ts, err := config.TailscaleInFile(path)
	if err != nil {
		return federation.DefaultTailnetOptions(), false
	}
	return ts.TailnetOptions(), ts.TailscaleEnabled()
}

func (m *OS) hostTestItem() settingItem {
	return settingItem{
		Label:   hostTestRowLabel,
		Desc:    "Dial every host now and show what each one said. Press enter.",
		Control: controlEnum,
		Options: []string{"run"},
		value: func(m *OS) string {
			if m.hostTestRunning {
				return "testing"
			}
			return "run"
		},
		activate: func(m *OS) tea.Cmd { return m.startHostTest() },
	}
}

func (m *OS) addHostFromRow(v string) {
	fields := strings.Fields(v)
	if len(fields) < 2 {
		m.ShowNotification("Type a name and an address, separated by a space.", "warning", m.Settings.NotificationWarningDuration)
		return
	}
	name, addr := fields[0], strings.Join(fields[1:], " ")
	if err := federation.ValidHostName(name); err != nil {
		m.ShowNotification(err.Error(), "error", m.Settings.NotificationErrorDuration)
		return
	}
	m.setHostAddr(name, addr)
}

func (m *OS) setHostAddr(name, addr string) {
	addr = strings.TrimSpace(addr)
	if err := federation.ValidHostName(name); err != nil {
		m.ShowNotification(err.Error(), "error", m.Settings.NotificationErrorDuration)
		return
	}
	if m.UserConfig == nil {
		m.UserConfig = config.DefaultConfig()
		m.ConfigReadOnly = true
	}
	if m.UserConfig.Hosts == nil {
		m.UserConfig.Hosts = map[string]config.HostConfig{}
	}
	entry := m.UserConfig.Hosts[name]
	entry.Addr = addr
	m.UserConfig.Hosts[name] = entry
	m.federationPolling = true
	delete(m.hostTests, name)
	m.noteHostToApply(name)
	m.ShowNotification("Host "+name+" now points at "+addr+".", "success", m.Settings.NotificationDuration)
}

func (m *OS) removeHost(name string) {
	if m.UserConfig == nil || m.UserConfig.Hosts == nil {
		return
	}
	delete(m.UserConfig.Hosts, name)
	delete(m.hostTests, name)
	if m.settingsList.Cursor > 0 {
		m.settingsList.Cursor--
	}
	m.ShowNotification("Host "+name+" is removed.", "success", m.Settings.NotificationDuration)
}

const hostTestBudget = 20 * time.Second

func (m *OS) startHostTest() tea.Cmd {
	if m.hostTestRunning {
		return nil
	}
	if m.ConfigReadOnly {
		m.ShowNotification("This session cannot test the links. It does not own the config file.", "warning", m.Settings.NotificationWarningDuration)
		return nil
	}
	hosts := m.federationHostsFromConfig()
	if len(hosts) == 0 {
		m.ShowNotification("No hosts are configured. Add one on the row above.", "warning", m.Settings.NotificationWarningDuration)
		return nil
	}
	m.hostTestRunning = true
	m.ShowNotification("The link test is running.", "info", m.Settings.NotificationDuration)
	return hostTestCmd(hosts)
}

func (m *OS) federationHostsFromConfig() []federation.Host {
	if m.UserConfig == nil {
		return nil
	}
	return session.HostsFromConfig(m.UserConfig)
}

func hostTestCmd(hosts []federation.Host) tea.Cmd {
	return func() tea.Msg {
		table, _ := federation.NewTable(hosts)
		mgr := federation.New(table, federation.Options{
			Dial:            federation.SSHDialer(os.Getenv("TUIOS_SSH")),
			ClientName:      "tuios-settings",
			VerbProtocol:    session.VerbProtocolVersion,
			MinVerbProtocol: session.MinVerbProtocolVersion,
		})
		ctx, cancel := context.WithTimeout(context.Background(), hostTestBudget)
		defer cancel()
		mgr.Start(ctx)
		reports := mgr.Reports(ctx)
		mgr.Stop()
		return HostTestDoneMsg{Reports: reports}
	}
}

func (m *OS) applyHostTest(msg HostTestDoneMsg) {
	m.hostTestRunning = false
	if m.hostTests == nil {
		m.hostTests = map[string]federation.HostReport{}
	}
	up := 0
	for _, r := range msg.Reports {
		m.hostTests[r.Host] = r
		if r.Up() {
			up++
		}
	}
	total := len(msg.Reports)
	switch {
	case total == 0:
		m.ShowNotification("No host answered the test.", "warning", m.Settings.NotificationWarningDuration)
	case up == total:
		m.ShowNotification("Every host answers.", "success", m.Settings.NotificationDuration)
	default:
		m.ShowNotification("Some hosts do not answer. Each row says why.", "warning", m.Settings.NotificationWarningDuration)
	}
}

func (m *OS) noteHostToApply(name string) {
	if m.ProcessingRemoteKeys || slices.Contains(m.hostsToApply, name) {
		return
	}
	m.hostsToApply = append(m.hostsToApply, name)
}

// HostApplyFailedMsg says the daemon did not apply a host the person
// changed on the settings page.
type HostApplyFailedMsg struct {
	Host string
	Err  error
}

func (m *OS) applyHostsCmd() func() tea.Msg {
	hosts := m.hostsToApply
	m.hostsToApply = nil
	if len(hosts) == 0 || m.AttachedHost != "" || (m.DaemonClient == nil && m.Inbox.call == nil) {
		return nil
	}
	call := m.inboxCaller()
	return func() tea.Msg {
		for _, h := range hosts {
			if _, err := call("apply-config", map[string]any{"host": h}, 5*time.Second); err != nil {
				return HostApplyFailedMsg{Host: h, Err: err}
			}
		}
		return nil
	}
}
