package app

import (
	"context"
	"os"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// The Hosts section of the settings page.
//
// It is the [hosts] config table as rows: one per machine, showing the address
// and what the link is doing right now, plus a row that adds one and a row that
// dials them all.
//
// The rows are built from the config on every draw rather than declared once,
// because the set is the user's and changes while the page is open. Everything
// else on the page is a fixed list of options, so this is the one section whose
// row count moves; nothing new was needed to draw it, because a category's rows
// are already resolved per call.
//
// The status on each row comes from the daemon, through the snapshot the rail
// already polls. The settings page starts no link and holds no link. What it
// can do is write the config file, which the daemon follows.
//
// Stage 1 has not changed here: only listings cross a link. There is no row on
// this page that starts, changes or stops anything on another machine.

// hostAddRowLabel is the label of the row that adds a machine. It is matched by
// name in the tests, so it is spelled once.
const hostAddRowLabel = "Add a host"

// hostTestRowLabel is the label of the row that dials every configured host.
const hostTestRowLabel = "Test the links"

// HostTestDoneMsg carries the result of the settings page's link test back to
// the Update goroutine. The dialing happened in the Cmd that produced it.
type HostTestDoneMsg struct {
	Reports []federation.HostReport
}

// hostsCategory is the Hosts tab.
func (m *OS) hostsCategory() settingsCategory {
	names := m.configuredHostNames()
	items := make([]settingItem, 0, len(names)+2)
	for _, name := range names {
		items = append(items, m.hostItem(name))
	}
	items = append(items, m.hostAddItem(), m.hostTestItem())
	return settingsCategory{Name: "Hosts", Items: items}
}

// configuredHostNames is the [hosts] table's names, in order.
func (m *OS) configuredHostNames() []string {
	if m.UserConfig == nil || len(m.UserConfig.Hosts) == 0 {
		return nil
	}
	names := make([]string, 0, len(m.UserConfig.Hosts))
	for name := range m.UserConfig.Hosts {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// hostItem is one machine's row. The value is its address, so editing the row
// moves the machine and clearing the row removes it.
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

// hostRowStatus is the sentence under a host row: what the link is doing, and
// why when it is not working.
//
// A host that is not up keeps its row and says why. Hiding it would leave the
// user looking for a machine they know they configured.
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

// hostStateSentence turns a link state into one plain sentence.
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
	}
	parts := []string{head}
	if reason != "" && reason != head {
		parts = append(parts, reason)
	}
	if detail != "" {
		// The detail comes from ssh or from the other machine. It is labelled
		// so a reader cannot mistake it for something tuios said.
		parts = append(parts, "The link reported: "+detail)
	}
	return strings.Join(parts, " ")
}

// hostAddItem is the row that adds a machine. One field takes both the name and
// the address, because the page has one text control and a second row that only
// half-adds a host is worse than typing two words.
func (m *OS) hostAddItem() settingItem {
	desc := "Type a name and an address, separated by a space. Press enter."
	if candidates := m.hostAddrCandidates(); len(candidates) > 0 {
		desc += " Your ssh config names: " + strings.Join(candidates, ", ") + "."
	}
	return settingItem{
		Label:       hostAddRowLabel,
		Desc:        desc,
		Control:     controlString,
		Placeholder: "build gaurav@buildbox",
		Unset:       "(name and address)",
		value:       func(_ *OS) string { return "" },
		setStr:      func(m *OS, v string) { m.addHostFromRow(v) },
	}
}

// maxHostAddrCandidates bounds the aliases shown on the add row. The row has
// one description line, and a machine with a generated ssh config has hundreds.
const maxHostAddrCandidates = 8

// hostAddrCandidates are the ssh_config aliases offered as addresses.
//
// Only the Host names are read. No key file and no known_hosts is opened, and
// nothing here is added on its own: a host exists because the user named it.
// See internal/federation's sshalias.go.
func (m *OS) hostAddrCandidates() []string {
	if m.ConfigReadOnly {
		// A served session must not read the serving machine's ssh config on
		// behalf of whoever connected to it.
		return nil
	}
	aliases := federation.ReadSSHAliases(federation.UserSSHConfigPath())
	if len(aliases) > maxHostAddrCandidates {
		aliases = aliases[:maxHostAddrCandidates]
	}
	return aliases
}

// hostTestItem is the row that dials every configured host.
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

// addHostFromRow reads "name address" out of the add row and writes it.
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

// setHostAddr writes one host into this client's config. The file write is
// persistSettings', which the settings page already runs after a commit, and
// the daemon follows the file, so the link opens without a restart.
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
	// The rail stops polling once the daemon reports no hosts, which is the
	// default install. Adding the first host has to start it again, or the
	// status column on this page would stay empty for ever.
	m.federationPolling = true
	delete(m.hostTests, name)
	m.ShowNotification("Host "+name+" now points at "+addr+".", "success", m.Settings.NotificationDuration)
}

// removeHost drops one host from this client's config.
func (m *OS) removeHost(name string) {
	if m.UserConfig == nil || m.UserConfig.Hosts == nil {
		return
	}
	delete(m.UserConfig.Hosts, name)
	delete(m.hostTests, name)
	// The row the selection was on is gone, so the selection is pulled back
	// onto a row that exists rather than left past the end of a shorter list.
	if m.SettingsSelected > 0 {
		m.SettingsSelected--
	}
	m.ShowNotification("Host "+name+" is removed.", "success", m.Settings.NotificationDuration)
}

// hostTestBudget bounds one run of the test row.
const hostTestBudget = 20 * time.Second

// startHostTest dials every configured host off the Update goroutine.
//
// It runs ssh in this process rather than reading the daemon's links, for the
// reason `tuios hosts test` does: a link the daemon gave up on is retried a
// minute apart, so its last state is not an answer to "is it working now".
//
// A read-only session does not run it. That session is served to somebody else,
// and running ssh from the serving machine on their behalf is not this page's
// to do.
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

// federationHostsFromConfig is this client's [hosts] table as dialable hosts.
func (m *OS) federationHostsFromConfig() []federation.Host {
	if m.UserConfig == nil {
		return nil
	}
	return session.HostsFromConfig(m.UserConfig)
}

// hostTestCmd dials every host once and reports what each said. It touches the
// network only inside the returned function, which is the rule this whole
// feature is most at risk of breaking.
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

// applyHostTest stores what the test found. It runs on the Update goroutine and
// does no I/O.
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
