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
)

// The Agents section of the settings page.
//
// It is tuios doctor agents as rows: one per harness tuios can integrate with,
// saying whether the harness is on PATH and whether its integration is
// installed, out of date or not installed, and one per harness tuios has no
// integration for, with the reason. Enter on a row opens its actions, which are
// tuios integration install and uninstall: the same calls, on the same files.
// An action row names the file it changes, so choosing one is the confirmation.
//
// The report is read off the UI goroutine. It reads a file or two per harness
// and searches PATH for each, which is too slow for a frame, so the rows are
// drawn from the last report and a fresh one is asked for each time the page
// opens and after every action. Nothing here runs per frame but the drawing.
//
// The same report drives one notice per client run: a pane that runs a harness
// whose integration is out of date, or not installed where it could be, puts
// up a toast naming the fix, once per harness.
//
// A remote client does not get the tab. Its config file and its home are the
// server's, and installing hooks there on a visitor's say is not this page's
// to do. The browser build has no harnesses to read.

// agentsTabName is the tab's name, matched by OpenSettingsAt.
const agentsTabName = "Agents"

// paletteAgentsSettingsName is the palette's entry for the tab.
const paletteAgentsSettingsName = "Agents: settings, install and update integrations"

// agentsCommand is the program the hooks run, as tuios integration install
// writes it by default.
const agentsCommand = "tuios"

// agentsPageState is the Agents tab's state.
type agentsPageState struct {
	// overview is the last report, nil until one has arrived.
	overview *integration.Overview
	// loading is set while a report is being read.
	loading bool
	// stale asks for a fresh report at the next chance.
	stale bool
	// wasOpen is whether the settings page was open after the last message,
	// so the page opening can be told from it staying open.
	wasOpen bool
	// action is the harness whose action rows are open, "" for the list.
	action string
	// busy is set while an install or uninstall runs.
	busy bool
	// tab is the tab's index on the page as last built, -1 without it. The
	// footer reads it to show the tab's own keys.
	tab int
	// panesSeen holds the harness names panes reported that this client has
	// looked at, and noticed the harness ids it has decided the notice for.
	// Each is decided once a run.
	panesSeen map[string]bool
	noticed   map[string]bool
	// loadFor holds the harness names panes reported when the report in
	// flight was asked for. Only those are decided when it lands: a report
	// read before a pane started says nothing about an install made since.
	loadFor map[string]bool
	// noticeKeys maps a notice on the dock, by notification id, to its
	// dismissal key, so a dismissal can be stored. See agentNoticeKey.
	noticeKeys map[string]string
}

// agentsOverviewMsg carries a report read off the UI goroutine.
type agentsOverviewMsg struct {
	overview integration.Overview
}

// agentsActionMsg is the outcome of an install or uninstall the page ran.
type agentsActionMsg struct {
	harness string
	verb    agentVerb
	result  integration.Result
	err     error
	// steps is what an uninstall did, part by part.
	steps []integration.RemovalStep
}

// agentVerb is what an action row does.
type agentVerb int

const (
	agentInstall agentVerb = iota
	agentUpdate
	agentUninstall
)

// agentsPageAvailable reports whether this client has the Agents tab: a
// terminal on this machine, with the agent features on. A model built without
// a client kind (ClientUnknown) does not get it either: the kind is what says
// a person on this machine is looking, and pkg/tuios names ClientLocal or
// ClientSSH for every model it builds, so only the tests and the fuzzer build
// an unknown one.
func (m *OS) agentsPageAvailable() bool {
	return m.agentsOn() && m.Client == ClientLocal && !m.RemoteClient && runtime.GOOS != "js"
}

// agentsEnv is where the integrations live: this process's home and PATH, the
// same as the CLI reads.
func agentsEnv() integration.Env { return integration.SystemEnv() }

// loadAgentsOverviewCmd reads the report in a command.
func loadAgentsOverviewCmd() tea.Cmd {
	return func() tea.Msg {
		return agentsOverviewMsg{overview: integration.BuildOverview(agentsEnv(), agentsCommand)}
	}
}

// agentsSyncCmd is asked after every message. It asks for a fresh report when
// the settings page opens, and when a pane runs a harness this run has not
// looked at yet. The notice for that harness is decided when the fresh report
// lands, so an install made in a shell meanwhile is seen. The work per message
// is a pass over the panes.
func (m *OS) agentsSyncCmd() tea.Cmd {
	p := &m.agentsPage
	if !m.agentsPageAvailable() {
		return nil
	}
	if m.ShowSettings && !p.wasOpen {
		p.stale = true
	}
	if !m.ShowSettings && p.wasOpen {
		// The action rows are a step inside one visit to the page.
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

// applyAgentsOverview stores a report. It runs on the Update goroutine.
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

// agentPanesUnnoticed is the harness names panes on this machine report that
// this run has not looked at yet, nil when there are none.
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

// noticeAgentIntegrations decides, once a run, for each harness name in names
// that a pane still runs, and puts up a toast when its integration is out of
// date, or not installed where it could be. A harness with no integration, or
// one that is current, is marked and left alone.
func (m *OS) noticeAgentIntegrations(names map[string]bool) {
	p := &m.agentsPage
	if p.overview == nil || len(names) == 0 {
		return
	}
	for _, w := range m.Windows {
		// A pane on another machine runs that machine's harness, whose
		// integration is not the one installed here.
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
		// A file tuios could not read says nothing about what to install.
		if st.Unreadable {
			continue
		}
		key := agentNoticeKey(st)
		if m.agentNoticesDismissed[key] {
			continue
		}
		if text := agentIntegrationNotice(st); text != "" {
			m.ShowNotification(text, "warning", m.Settings.NotificationWarningDuration)
			if n := len(m.Notifications); n > 0 && m.Notifications[n-1].Message == text {
				if p.noticeKeys == nil {
					p.noticeKeys = map[string]string{}
				}
				p.noticeKeys[m.Notifications[n-1].ID] = key
			}
		}
	}
}

// agentNoticeKey names one notice for the record of dismissals: the harness,
// the state, and the version installed. A dismissed notice stays dismissed
// across attaches until one of them changes, so a newer tuios that makes the
// integration out of date again says so again.
func agentNoticeKey(st integration.Status) string {
	return st.Harness + ":" + st.State().String() + ":v" + strconv.Itoa(st.Version) + ":v" + strconv.Itoa(st.WantVersion)
}

// noteNoticesDismissed stores the dismissal of each integration notice among
// gone, so it does not come back on the next attach. Only a click on the
// dismiss end of a notice that was drawn comes here. Esc clears the whole dock
// in every mode, also when the person pressed it for the pane, before the
// notice was even drawn, and a click on the body opens the message: both
// count for this run only, through noticed. A notice that timed out is not
// stored either.
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

// agentIntegrationNotice is the toast for a harness, "" when none is due.
func agentIntegrationNotice(st integration.Status) string {
	switch st.State() {
	case integration.StateOutOfDate:
		return st.Name + " integration is out of date. Open Settings, Agents to update it."
	case integration.StateNotInstalled:
		return st.Name + " integration is not installed. Open Settings, Agents to install it."
	}
	return ""
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

// showAgentsTab puts the page on the Agents tab, from a search result or a
// row that opens the action rows, with the cursor on row.
func (m *OS) showAgentsTab(row int) {
	m.settingsSearch = settingsSearchState{}
	for i, c := range m.settingsCategories() {
		if c.Name == agentsTabName {
			m.SettingsCategory = i
			break
		}
	}
	m.SettingsSelected = row
	m.SettingsScroll = 0
}

// SettingsBack leaves the action rows for the list, and reports whether there
// were action rows to leave. Esc comes here before it closes the page.
func (m *OS) SettingsBack() bool {
	if m.agentsPage.action == "" {
		return false
	}
	if !m.onAgentsTab() {
		// The action rows are on a tab the page no longer shows. Esc there
		// is about the page, so it closes it, and the rows go with it.
		m.agentsPage.action = ""
		return false
	}
	id := m.agentsPage.action
	m.agentsPage.action = ""
	m.showAgentsTab(m.agentRowIndex(id))
	return true
}

// agentsHints is the footer on the Agents tab's list, and agentsActionHints
// on its action rows. The rows have nothing to change with the arrows, and
// esc goes back from the action rows.
var (
	agentsHints = []overlay.Hint{
		{Key: "↑↓", Label: "move"},
		{Key: "enter", Label: "choose"},
		{Key: "tab", Label: "section"},
		{Key: "/", Label: "search"},
		{Key: "esc", Label: "close"},
	}
	agentsActionHints = []overlay.Hint{
		{Key: "↑↓", Label: "move"},
		{Key: "enter", Label: "confirm"},
		{Key: "tab", Label: "section"},
		{Key: "esc", Label: "back"},
	}
)

// onAgentsTab reports whether the page shows the Agents tab's rows.
func (m *OS) onAgentsTab() bool {
	return m.agentsPageAvailable() && !m.settingsSearch.open && m.agentsPage.tab >= 0 && m.SettingsCategory == m.agentsPage.tab
}

// agentsCategory is the Agents tab.
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

// agentsInOrder puts the harnesses this machine has first: installed, on
// PATH, or run here. The rest follow. Each half keeps the CLI's order.
func agentsInOrder(all []integration.Status) []integration.Status {
	out := slices.Clone(all)
	here := func(s integration.Status) int {
		if s.Installed || s.BinaryPath != "" || s.ConfigDirExists {
			return 0
		}
		return 1
	}
	slices.SortStableFunc(out, func(a, b integration.Status) int { return cmp.Compare(here(a), here(b)) })
	return out
}

// agentRowIndex is the list row of a harness, 0 when it has none.
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

// agentStateWord is the value a harness row shows.
func agentStateWord(st integration.Status) string {
	if st.Unreadable {
		return "cannot read"
	}
	return st.State().String()
}

// agentStateInk is the colour of the value a harness row shows.
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

// agentItem is one harness's row in the list.
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

// lookupTarget is integration.LookupTarget without the found flag.
func lookupTarget(id string) *integration.Target {
	t, _ := integration.LookupTarget(id)
	return t
}

// agentRowDesc is the line under a harness row.
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

// agentActionItems are the rows that open on a harness: what can be done, each
// naming the file it changes, and the way back.
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

// installedParts lists the parts of the integration that are installed, as
// the uninstall row names them, and the files they are in: the hooks, the MCP
// server and the status line, each only when it is there. Uninstall removes
// every part, so it is offered when any one of them is installed.
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

// agentActionItem is one action row. Its value is the file it changes.
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

// verbWord is the verb as the rows say it.
func verbWord(v agentVerb) string {
	switch v {
	case agentUpdate:
		return "update"
	case agentUninstall:
		return "uninstall"
	}
	return "install"
}

// joinWords lists words the way a sentence does: "a", "a and b", "a, b and c".
func joinWords(words []string) string {
	switch len(words) {
	case 0:
		return ""
	case 1:
		return words[0]
	}
	return strings.Join(words[:len(words)-1], ", ") + " and " + words[len(words)-1]
}

// joinPaths names files the way a person reads them.
func joinPaths(paths []string) string {
	shown := make([]string, len(paths))
	for i, p := range paths {
		shown[i] = shortenHome(p)
	}
	return joinWords(shown)
}

// runAgentAction runs an install or uninstall off the UI goroutine. They are
// the calls tuios integration install and uninstall make. An update keeps the
// program the installed hooks already run, so an install made with --command
// keeps its path. A fresh install runs the CLI's default, tuios.
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
		return msg
	}
}

// applyAgentAction says what an action did, goes back to the list, and asks
// for a fresh report. It runs on the Update goroutine.
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
	// The fix is in: a later pane running the harness has nothing to say.
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

// agentActionOutcome is the sentence an action leaves, and its level.
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
	done := "installed in "
	if msg.verb == agentUpdate {
		done = "updated in "
	}
	text := name + " integration is " + done + shortenHome(msg.result.Path) + "."
	if msg.result.Backup != "" {
		text += " The previous copy is in " + shortenHome(msg.result.Backup) + "."
	}
	for _, n := range msg.result.Notes {
		text += " Note: " + n
	}
	return text, "success"
}
