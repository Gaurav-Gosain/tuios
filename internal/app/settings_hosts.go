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
// There is no row on this page that starts, changes or stops anything on
// another machine.

// hostTestRowLabel is the label of the row that dials every configured host.
// It is the English label, matched by name in the tests.
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
	return m.settingsCat("Hosts", items)
}

// configuredHostNames is the [hosts] table's names, in order.
func (m *OS) configuredHostNames() []string {
	if m.UserConfig == nil || len(m.UserConfig.Hosts) == 0 {
		return nil
	}
	names := make([]string, 0, len(m.UserConfig.Hosts))
	for name := range m.UserConfig.Hosts {
		// [hosts."*"] is the policy for every machine linking in, not a
		// machine, so it has no address to edit here.
		if name == config.LinkPolicyDefaultName {
			continue
		}
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
		Placeholder: m.loc().Row("hosts.placeholder"),
		Unset:       m.loc().Row("hosts.unset"),
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
	edit := " " + m.loc().Row("hosts.edit_hint")
	if r, ok := m.hostTests[name]; ok {
		return m.hostStateSentence(string(r.Status), r.Reason, r.Detail) + edit
	}
	for _, h := range m.FederationHosts {
		if h.Name != name {
			continue
		}
		return m.hostStateSentence(h.Status, h.Reason, "") + edit
	}
	return m.loc().Row("hosts.state.unreported") + edit
}

// hostStateSentence turns a link state into one plain sentence.
func (m *OS) hostStateSentence(status, reason, detail string) string {
	loc := m.loc()
	head := loc.Rowf("hosts.state.raw", "status", status)
	switch federation.Status(status) {
	case federation.StatusUp:
		head = loc.Row("hosts.state.up")
	case federation.StatusNoDaemon:
		head = loc.Row("hosts.state.no_daemon")
	case federation.StatusNoBinary:
		head = loc.Row("hosts.state.no_binary")
	case federation.StatusUnreachable:
		head = loc.Row("hosts.state.unreachable")
	case federation.StatusIncompatible:
		head = loc.Row("hosts.state.incompatible")
	case federation.StatusConnecting:
		head = loc.Row("hosts.state.connecting")
	case federation.StatusReconnecting:
		head = loc.Row("hosts.state.reconnecting")
	case federation.StatusApproval:
		head = federation.SignInSentence
	}
	parts := []string{head}
	if strings.HasPrefix(reason, head) {
		// The link's own reason starts with the same sentence and goes on to
		// say what to do, so it stands alone.
		parts = []string{reason}
	} else if reason != "" {
		parts = append(parts, reason)
	}
	if detail != "" {
		// The detail comes from ssh or from the other machine. It is labelled
		// so a reader cannot mistake it for something tuios said.
		parts = append(parts, loc.Rowf("hosts.state.reported", "detail", detail))
	}
	return strings.Join(parts, " ")
}

// hostAddItem is the row that opens the add sheet, which tests the machine,
// installs tuios there when it is missing, and adds it. See machine_sheet.go.
func (m *OS) hostAddItem() settingItem {
	return settingItem{
		Label:   m.loc().Row("hosts.add.label"),
		Desc:    m.loc().Row("hosts.add.desc"),
		Control: controlEnum,
		Options: []string{"open"},
		value:   func(m *OS) string { return m.loc().Value("hosts.open") },
		activate: func(m *OS) tea.Cmd {
			return m.OpenMachineSheet("")
		},
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

// tailnetOptionsFromFile is the [tailscale] table, and whether it asks for
// tailnet machines to be offered at all.
//
// A config that cannot be found or will not parse gives the defaults rather
// than nothing: the suggestion list is a convenience, and losing it as well
// as everything else a broken config costs would be piling on.
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

// hostTestItem is the row that dials every configured host.
func (m *OS) hostTestItem() settingItem {
	return settingItem{
		Label:   hostTestRowLabel,
		Desc:    m.loc().Row("hosts.test.desc"),
		Control: controlEnum,
		Options: []string{"run"},
		value: func(m *OS) string {
			if m.hostTestRunning {
				return m.loc().Value("hosts.testing")
			}
			return m.loc().Value("hosts.run")
		},
		activate: func(m *OS) tea.Cmd { return m.startHostTest() },
	}
}

// addHostFromRow reads "name address" out of the add row and writes it.
func (m *OS) addHostFromRow(v string) {
	fields := strings.Fields(v)
	if len(fields) < 2 {
		m.ShowNotification(m.loc().Notify("hosts.type_name_address"), "warning", m.Settings.NotificationWarningDuration)
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
	m.noteHostToApply(name)
	m.ShowNotification(m.loc().Notifyf("hosts.saved", "name", name, "addr", addr), "success", m.Settings.NotificationDuration)
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
	if m.settingsList.Cursor > 0 {
		m.settingsList.Cursor--
	}
	m.ShowNotification(m.loc().Notifyf("hosts.removed", "name", name), "success", m.Settings.NotificationDuration)
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
		m.ShowNotification(m.loc().Notify("hosts.test_denied"), "warning", m.Settings.NotificationWarningDuration)
		return nil
	}
	hosts := m.federationHostsFromConfig()
	if len(hosts) == 0 {
		m.ShowNotification(m.loc().Notify("hosts.none_configured"), "warning", m.Settings.NotificationWarningDuration)
		return nil
	}
	m.hostTestRunning = true
	m.ShowNotification(m.loc().Notify("hosts.test_running"), "info", m.Settings.NotificationDuration)
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
		m.ShowNotification(m.loc().Notify("hosts.test_none_answered"), "warning", m.Settings.NotificationWarningDuration)
	case up == total:
		m.ShowNotification(m.loc().Notify("hosts.test_all_answer"), "success", m.Settings.NotificationDuration)
	default:
		m.ShowNotification(m.loc().Notify("hosts.test_some_no_answer"), "warning", m.Settings.NotificationWarningDuration)
	}
}

// noteHostToApply records a host the person changed at this client's keyboard,
// for the save to apply. A change that send-keys typed is not recorded: it
// waits for tuios config apply like any change a pane makes.
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

// applyHostsCmd returns the calls that apply the recorded hosts, to run once
// the file is written, and clears the record. Each call applies one host's
// entry and nothing else in the file.
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
