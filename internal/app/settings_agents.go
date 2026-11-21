package app

import (
	"cmp"
	"image/color"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/integration"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/sortx"
)

const agentsTabName = "Agents"

const paletteAgentsSettingsName = "Agents: settings, install and update integrations"

const agentsCommand = "tuios"

type agentsPageState struct {
	overview   *integration.Overview
	loading    bool
	stale      bool
	wasOpen    bool
	action     string
	busy       bool
	tab        int
	panesSeen  map[string]bool
	noticed    map[string]bool
	loadFor    map[string]bool
	noticeKeys map[string]string
}

type agentsOverviewMsg struct {
	overview integration.Overview
}

type agentsActionMsg struct {
	harness string
	verb    agentVerb
	result  integration.Result
	err     error
	steps   []integration.RemovalStep
}

type agentVerb int

const (
	agentInstall agentVerb = iota
	agentUpdate
	agentUninstall
)

func (m *OS) agentsPageAvailable() bool {
	return m.agentsOn() && m.Client == ClientLocal && !m.RemoteClient && runtime.GOOS != "js"
}

func agentsEnv() integration.Env { return integration.SystemEnv() }

func loadAgentsOverviewCmd() tea.Cmd {
	return func() tea.Msg {
		return agentsOverviewMsg{overview: integration.BuildOverview(agentsEnv(), agentsCommand)}
	}
}

func (m *OS) agentsSyncCmd() tea.Cmd {
	p := &m.agentsPage
	if !m.agentsPageAvailable() {
		return nil
	}
	if m.ShowSettings && !p.wasOpen {
		p.stale = true
	}
	if !m.ShowSettings && p.wasOpen {
		p.action = ""
	}
	p.wasOpen = m.ShowSettings
	if p.loading {
		return nil
	}
	unseen := m.agentPanesUnnoticed()
	if !p.stale && len(unseen) == 0 {
		return nil
	}
	p.loading = true
	p.stale = false
	p.loadFor = unseen
	return loadAgentsOverviewCmd()
}

func (m *OS) applyAgentsOverview(msg agentsOverviewMsg) {
	ov := msg.overview
	m.agentsPage.overview = &ov
	m.agentsPage.loading = false
	for _, s := range ov.Harnesses {
		if s.Installed {
			m.agentIntegrationInstalled = true
		}
	}
	m.noticeAgentIntegrations(m.agentsPage.loadFor)
	m.agentsPage.loadFor = nil
}

func (m *OS) agentPanesUnnoticed() map[string]bool {
	var out map[string]bool
	for _, w := range m.Windows {
		if w != nil && w.AgentHarness != "" && w.Host == "" && !m.agentsPage.panesSeen[w.AgentHarness] {
			if out == nil {
				out = map[string]bool{}
			}
			out[w.AgentHarness] = true
		}
	}
	return out
}

func (m *OS) noticeAgentIntegrations(names map[string]bool) {
	p := &m.agentsPage
	if p.overview == nil || len(names) == 0 {
		return
	}
	for _, w := range m.Windows {
		if w == nil || w.AgentHarness == "" || w.Host != "" || p.panesSeen[w.AgentHarness] || !names[w.AgentHarness] {
			continue
		}
		if p.panesSeen == nil {
			p.panesSeen, p.noticed = map[string]bool{}, map[string]bool{}
		}
		p.panesSeen[w.AgentHarness] = true
		st, ok := p.overview.Lookup(w.AgentHarness)
		if !ok || p.noticed[st.Harness] {
			continue
		}
		p.noticed[st.Harness] = true
		if st.Unreadable {
			continue
		}
		key := agentNoticeKey(st)
		if m.agentNoticesDismissed[key] {
			continue
		}
		if text := m.agentIntegrationNotice(st); text != "" {
			m.ShowNotificationFrom(text, "warning", m.Settings.NotificationWarningDuration, NotifTarget{AgentInstall: st.Harness})
			if n := len(m.Notifications); n > 0 && m.Notifications[n-1].Message == text {
				if p.noticeKeys == nil {
					p.noticeKeys = map[string]string{}
				}
				p.noticeKeys[m.Notifications[n-1].ID] = key
			}
		}
	}
}

func agentNoticeKey(st integration.Status) string {
	return st.Harness + ":" + st.State().String() + ":v" + strconv.Itoa(st.Version) + ":v" + strconv.Itoa(st.WantVersion)
}

func (m *OS) noteNoticesDismissed(gone []Notification) {
	keys := m.agentsPage.noticeKeys
	if len(keys) == 0 {
		return
	}
	changed := false
	for _, n := range gone {
		key, ok := keys[n.ID]
		if !ok {
			continue
		}
		delete(keys, n.ID)
		if m.agentNoticesDismissed == nil {
			m.agentNoticesDismissed = map[string]bool{}
		}
		if !m.agentNoticesDismissed[key] {
			m.agentNoticesDismissed[key] = true
			changed = true
		}
	}
	if changed {
		m.saveSidebarState()
	}
}

func (m *OS) agentIntegrationNotice(st integration.Status) string {
	verb := "install"
	switch st.State() {
	case integration.StateOutOfDate:
		verb = "update"
	case integration.StateNotInstalled:
	default:
		return ""
	}
	where := shortenHome(st.Path)
	if t, ok := integration.LookupTarget(st.Harness); ok {
		where = joinPaths(t.Paths(agentsEnv()))
	}
	how := "Click it to " + verb + " it in " + where + "."
	if key := contextMenuHint(m.KeybindRegistry, "prefix_jump_notif", &m.Settings, m.Mode); key != "" {
		how = key + " " + verb + "s it in " + where + "."
	}
	if verb == "update" {
		return st.Name + " integration is out of date. " + how
	}
	return st.Name + " integration is not installed. " + how
}

func (m *OS) installFromNotice(id string) {
	if m.refuseAgentsOff() {
		return
	}
	p := &m.agentsPage
	if !m.agentsPageAvailable() || p.overview == nil {
		m.ShowNotification("This client cannot change the agent integrations.", "info", m.Settings.NotificationDuration)
		return
	}
	st, ok := p.overview.Lookup(id)
	if !ok {
		return
	}
	if !st.NeedsAction() {
		m.ShowNotification(st.Name+" integration is already installed and current.", "info", m.Settings.NotificationDuration)
		return
	}
	if p.busy {
		m.ShowNotification("Another change is running. Wait for it to end.", "info", m.Settings.NotificationDuration)
		return
	}
	verb := agentInstall
	if st.State() == integration.StateOutOfDate {
		verb = agentUpdate
	}
	m.openIntegrationAsk(st, verb)
}

// OpenAgentsSettings opens the settings page on the Agents tab. The prefix
// key, the palette and the help come here.
func (m *OS) OpenAgentsSettings() {
	if m.refuseAgentsOff() {
		return
	}
	if !m.agentsPageAvailable() {
		m.ShowNotification("This client cannot change the agent integrations.", "info", m.Settings.NotificationDuration)
		return
	}
	m.agentsPage.action = ""
	m.OpenSettingsAt(agentsTabName)
}

func (m *OS) showAgentsTab(row int) {
	m.settingsSearch = settingsSearchState{}
	for i, c := range m.settingsCategories() {
		if c.Name == agentsTabName {
			m.SettingsCategory = i
			break
		}
	}
	m.settingsList.Cursor = row
	m.settingsList.Scroll = 0
}

// SettingsBack leaves the action rows for the list, and reports whether there
// were action rows to leave. Esc comes here before it closes the page.
func (m *OS) SettingsBack() bool {
	if m.agentsPage.action == "" {
		return false
	}
	if !m.onAgentsTab() {
		m.agentsPage.action = ""
		return false
	}
	id := m.agentsPage.action
	m.agentsPage.action = ""
	m.showAgentsTab(m.agentRowIndex(id))
	return true
}

var (
	agentsHints = []overlay.Hint{
		{Key: hintListMove, Label: "move"},
		{Key: "enter", Label: "choose"},
		{Key: "tab", Label: "section"},
		{Key: "/", Label: "search"},
		{Key: "esc", Label: "close"},
	}
	agentsActionHints = []overlay.Hint{
		{Key: hintListMove, Label: "move"},
		{Key: "enter", Label: "confirm"},
		{Key: "tab", Label: "section"},
		{Key: "esc", Label: "back"},
	}
)

func (m *OS) onAgentsTab() bool {
	return m.agentsPageAvailable() && !m.settingsSearch.open && m.agentsPage.tab >= 0 && m.SettingsCategory == m.agentsPage.tab
}

func (m *OS) agentsCategory() settingsCategory {
	cat := settingsCategory{Name: agentsTabName}
	p := &m.agentsPage
	if p.overview == nil {
		cat.Items = []settingItem{{
			Label:   "Reading the agents",
			Desc:    "tuios reads each agent's configuration. The rows come next.",
			Control: controlStatus,
			value:   func(*OS) string { return "wait" },
			ink:     func(pal overlay.Palette) color.Color { return pal.FgMute },
		}}
		return cat
	}
	if p.action != "" {
		if st, ok := p.overview.Lookup(p.action); ok {
			cat.Items = m.agentActionItems(st)
			return cat
		}
		p.action = ""
	}
	if !p.overview.TuiosOnPath {
		cat.Items = append(cat.Items, settingItem{
			Label:   "tuios",
			Desc:    `The hooks run "tuios agent-hook". They cannot start until tuios is on PATH. Put tuios on PATH.`,
			Control: controlStatus,
			value:   func(*OS) string { return "not on PATH" },
			ink:     func(pal overlay.Palette) color.Color { return pal.Warning },
		})
	}
	for _, st := range agentsInOrder(p.overview.Harnesses) {
		cat.Items = append(cat.Items, m.agentItem(st))
	}
	for _, u := range p.overview.Unsupported {
		cat.Items = append(cat.Items, settingItem{
			Label:   u.Harness,
			Desc:    "tuios has no integration for " + u.Harness + ". The reason: " + u.Reason + ".",
			Control: controlStatus,
			value:   func(*OS) string { return "no integration" },
			ink:     func(pal overlay.Palette) color.Color { return pal.FgMute },
		})
	}
	return cat
}

func agentsInOrder(all []integration.Status) []integration.Status {
	out := slices.Clone(all)
	here := func(s integration.Status) int {
		if s.Installed || s.BinaryPath != "" || s.ConfigDirExists {
			return 0
		}
		return 1
	}
	sortx.SortStableFunc(out, func(a, b integration.Status) int { return cmp.Compare(here(a), here(b)) })
	return out
}

func (m *OS) agentRowIndex(id string) int {
	ov := m.agentsPage.overview
	if ov == nil {
		return 0
	}
	i := slices.IndexFunc(agentsInOrder(ov.Harnesses), func(s integration.Status) bool { return s.Harness == id })
	if i < 0 {
		return 0
	}
	if !ov.TuiosOnPath {
		i++
	}
	return i
}

func agentStateWord(st integration.Status) string {
	if st.Unreadable {
		return "cannot read"
	}
	return st.State().String()
}

func agentStateInk(st integration.Status) func(overlay.Palette) color.Color {
	return func(pal overlay.Palette) color.Color {
		switch st.State() {
		case integration.StateInstalled:
			return pal.Success
		case integration.StateOutOfDate:
			return pal.Warning
		case integration.StateNotInstalled:
			return pal.FgDim
		}
		return pal.FgMute
	}
}

func (m *OS) agentItem(st integration.Status) settingItem {
	aside := "not on PATH"
	if st.BinaryPath != "" {
		aside = "on PATH"
	}
	return settingItem{
		Label:   st.Name,
		Aside:   aside,
		Desc:    agentRowDesc(st),
		Control: controlStatus,
		value:   func(*OS) string { return agentStateWord(st) },
		ink:     agentStateInk(st),
		activate: func(m *OS) tea.Cmd {
			if m.agentsPage.busy {
				m.ShowNotification("Another change is running. Wait for it to end.", "info", m.Settings.NotificationDuration)
				return nil
			}
			if st.Unreadable {
				m.ShowNotification("tuios cannot read the "+st.Name+" integration. Fix the file the line under the row names, then open this tab again.", "warning", m.Settings.NotificationWarningDuration)
				return nil
			}
			if parts, _ := installedParts(st, lookupTarget(st.Harness), agentsEnv()); st.State() == integration.StateNotRun && len(parts) == 0 {
				m.ShowNotification(st.Name+" has not run here. Run it once, then install.", "info", m.Settings.NotificationDuration)
				return nil
			}
			m.agentsPage.action = st.Harness
			m.showAgentsTab(0)
			return nil
		},
	}
}

func lookupTarget(id string) *integration.Target {
	t, _ := integration.LookupTarget(id)
	return t
}

func agentRowDesc(st integration.Status) string {
	var parts []string
	switch st.State() {
	case integration.StateInstalled:
		parts = append(parts, "Installed and current (v"+strconv.Itoa(st.Version)+") in "+shortenHome(st.Path)+".")
		if st.OtherProgram {
			parts = append(parts, "The hooks run "+shortenHome(st.Program)+".")
		}
		parts = append(parts, "Press enter to uninstall it.")
	case integration.StateOutOfDate:
		parts = append(parts, "Out of date: v"+strconv.Itoa(st.Version)+" is installed and this tuios installs v"+strconv.Itoa(st.WantVersion)+". Press enter to update it.")
		if st.Program != "" && st.Program != agentsCommand {
			parts = append(parts, "The update keeps "+shortenHome(st.Program)+" as the program the hooks run.")
		}
	case integration.StateNotInstalled:
		parts = append(parts, "Not installed. Press enter to install it in "+shortenHome(st.Path)+".")
	default:
		parts = append(parts, "Not installed. "+st.Name+" has not run here. Run it once, then install.")
	}
	if !st.Installed {
		var other []string
		if st.MCP != nil && st.MCP.Installed {
			other = append(other, "the MCP server")
		}
		if st.StatusLine != nil && st.StatusLine.Installed {
			other = append(other, "the status line")
		}
		if len(other) > 0 {
			parts = append(parts, "tuios still has "+joinWords(other)+" installed. Press enter to remove it.")
		}
	}
	if st.Reports == integration.ReportsSession {
		parts = append(parts, "It reports the session id. The state comes from screen rules.")
	}
	for _, n := range st.Notes {
		parts = append(parts, "Note: "+n)
	}
	return strings.Join(parts, " ")
}

func (m *OS) agentActionItems(st integration.Status) []settingItem {
	t, _ := integration.LookupTarget(st.Harness)
	env := agentsEnv()
	var items []settingItem
	if st.NeedsAction() && t != nil {
		verb, label := agentInstall, "Install "+st.Name
		if st.State() == integration.StateOutOfDate {
			verb, label = agentUpdate, "Update "+st.Name
		}
		paths := t.Paths(env)
		items = append(items, m.agentActionItem(st, verb, label,
			"This writes the hook entries to "+joinPaths(paths)+". Press enter to "+verbWord(verb)+" the integration.", paths))
	}
	if parts, paths := installedParts(st, t, env); len(parts) > 0 {
		items = append(items, m.agentActionItem(st, agentUninstall, "Uninstall "+st.Name,
			"This removes "+joinWords(parts)+" that tuios wrote, from "+joinPaths(paths)+". Your own entries stay. Press enter to uninstall.", paths))
	}
	items = append(items, settingItem{
		Label:    "Back",
		Desc:     "Go back to the list. Nothing changes.",
		Control:  controlStatus,
		value:    func(*OS) string { return "esc" },
		ink:      func(pal overlay.Palette) color.Color { return pal.FgMute },
		activate: func(m *OS) tea.Cmd { m.SettingsBack(); return nil },
	})
	return items
}

func installedParts(st integration.Status, t *integration.Target, env integration.Env) (parts, paths []string) {
	if t == nil {
		return nil, nil
	}
	add := func(part string, files ...string) {
		parts = append(parts, part)
		for _, f := range files {
			if !slices.Contains(paths, f) {
				paths = append(paths, f)
			}
		}
	}
	if st.Installed {
		add("the hooks", t.Paths(env)...)
	}
	if st.MCP != nil && st.MCP.Installed {
		add("the MCP server", st.MCP.Path)
	}
	if st.StatusLine != nil && st.StatusLine.Installed {
		add("the status line", t.Path(env))
	}
	return parts, paths
}

func (m *OS) agentActionItem(st integration.Status, verb agentVerb, label, desc string, paths []string) settingItem {
	file := filepath.Base(paths[0])
	return settingItem{
		Label:   label,
		Desc:    desc,
		Control: controlStatus,
		value: func(m *OS) string {
			if m.agentsPage.busy {
				return "working"
			}
			return file
		},
		ink: func(pal overlay.Palette) color.Color {
			if verb == agentUninstall {
				return pal.Warn
			}
			return pal.Accent
		},
		activate: func(m *OS) tea.Cmd { return m.runAgentAction(st, verb) },
	}
}

func verbWord(v agentVerb) string {
	switch v {
	case agentUpdate:
		return "update"
	case agentUninstall:
		return "uninstall"
	}
	return "install"
}

func joinWords(words []string) string {
	switch len(words) {
	case 0:
		return ""
	case 1:
		return words[0]
	}
	return strings.Join(words[:len(words)-1], ", ") + " and " + words[len(words)-1]
}

func joinPaths(paths []string) string {
	shown := make([]string, len(paths))
	for i, p := range paths {
		shown[i] = shortenHome(p)
	}
	return joinWords(shown)
}

func (m *OS) runAgentAction(st integration.Status, verb agentVerb) tea.Cmd {
	if m.agentsPage.busy {
		return nil
	}
	t, ok := integration.LookupTarget(st.Harness)
	if !ok {
		return nil
	}
	m.agentsPage.busy = true
	return func() tea.Msg {
		env := agentsEnv()
		msg := agentsActionMsg{harness: t.ID, verb: verb}
		if verb == agentUninstall {
			msg.steps = t.UninstallAll(env)
			return msg
		}
		msg.result, msg.err = t.Install(env, st.InstallProgram(agentsCommand))
		if msg.err == nil && t.SupportsSkill() {
			_, _ = t.InstallSkill(env)
		}
		return msg
	}
}

func (m *OS) applyAgentAction(msg agentsActionMsg) tea.Cmd {
	p := &m.agentsPage
	p.busy = false
	name := msg.harness
	if t, ok := integration.LookupTarget(msg.harness); ok {
		name = t.Name
	}
	text, level := agentActionOutcome(name, msg)
	m.ShowNotification(text, level, m.Settings.NotificationDuration)
	if p.action == msg.harness {
		p.action = ""
		if m.ShowSettings && !m.settingsSearch.open {
			m.showAgentsTab(m.agentRowIndex(msg.harness))
		}
	}
	if p.noticed == nil {
		p.panesSeen, p.noticed = map[string]bool{}, map[string]bool{}
	}
	p.noticed[msg.harness] = true
	p.stale = true
	if p.loading {
		return nil
	}
	p.loading = true
	p.stale = false
	return loadAgentsOverviewCmd()
}

func agentActionOutcome(name string, msg agentsActionMsg) (string, string) {
	if msg.verb == agentUninstall {
		var failed []string
		removed := ""
		for _, s := range msg.steps {
			switch {
			case s.Err != nil:
				what := "the integration"
				if s.Part != "" {
					what = "the " + s.Part
				}
				failed = append(failed, "Could not remove "+what+": "+s.Err.Error())
			case s.Part == "" && s.Result.Changed:
				removed = name + " integration is removed from " + shortenHome(s.Result.Path) + "."
			}
		}
		if len(failed) > 0 {
			return strings.Join(append([]string{name + ":"}, failed...), " "), "error"
		}
		if removed == "" {
			return "Nothing of tuios's is installed for " + name + ".", "info"
		}
		return removed, "success"
	}
	if msg.err != nil {
		return "Could not " + verbWord(msg.verb) + " the " + name + " integration. " + msg.err.Error(), "error"
	}
	if !msg.result.Changed {
		return name + " integration is already installed and current.", "info"
	}
	done := "Installed in "
	if msg.verb == agentUpdate {
		done = "Updated in "
	}
	text := done + shortenHome(msg.result.Path) + "."
	if t, ok := integration.LookupTarget(msg.harness); ok && t.Reports != integration.ReportsSession {
		text += " Restart " + name + " to make it report its state."
	}
	if msg.result.Backup != "" {
		text += " The previous copy is in " + shortenHome(msg.result.Backup) + "."
	}
	for _, n := range msg.result.Notes {
		text += " Note: " + n
	}
	return text, "success"
}
