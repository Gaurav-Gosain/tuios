package app

import (
	tea "charm.land/bubbletea/v2"

	"cmp"
	"errors"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/listnav"
	"github.com/Gaurav-Gosain/tuios/internal/pathroot"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/pkg/fuzzy"
)

type gotoKind int

const (
	gotoAgent gotoKind = iota
	gotoSession
	gotoWorkspace
	gotoPane
)

var gotoKindNames = map[gotoKind][]string{
	gotoAgent:     {"agent", "agents"},
	gotoSession:   {"session", "sessions"},
	gotoWorkspace: {"workspace", "workspaces", "ws"},
	gotoPane:      {"pane", "panes", "window"},
}

var gotoSigils = map[byte]string{'@': "agent", '#': "session", '!': "workspace"}

const (
	gotoKeyKind  = "kind"
	gotoKeySpace = "space"
	gotoKeyWs    = "ws"
)

const gotoRecentMax = 16

type gotoPlace struct {
	host, session, window string
}

type gotoRow struct {
	kind                               gotoKind
	node                               sessiontree.Node
	host                               string
	session                            string
	sessionLabel                       string
	space                              string
	group                              string
	ws                                 int
	wsLabel                            string
	wsNumber                           string
	window                             string
	title                              string
	dir                                string
	panes                              int
	current                            bool
	state, harness, message, agentKind string
	doneSeen                           bool
	subagents                          int
	stateAt                            int64
}

func (r gotoRow) needsYou() bool {
	return r.kind == gotoAgent && sidebarAgentGroup(r.state, r.doneSeen) == sidebarGroupNeedsYou
}

type gotoPickerState struct {
	open        bool
	list        listnav.State
	killing     gotoRow
	tallest     int
	rows        []gotoRow
	filtered    []gotoRow
	filteredFor string
	built       bool
	parseErr    string
	sig         uint64

	here   gotoPlace
	recent []gotoPlace
}

// GotoPickerOpen reports whether the picker is up.
func (m *OS) GotoPickerOpen() bool { return m.gotoPicker.open }

// GotoPickerQuery is what is typed in the picker.
func (m *OS) GotoPickerQuery() string { return m.gotoPicker.list.Query.String() }

// OpenGotoPicker is the goto_picker action. Learn mode opens it too: the tab
// has one session, but its workspaces and panes are real places to go.
func (m *OS) OpenGotoPicker() { m.OpenGotoPickerWith("") }

// OpenGotoPickerWith opens the picker with query already typed. The keys
// that opened the session switcher, the workspace switcher and the window
// list open it on "# ", "! " and the panes of this session, so each finds
// what it found before in the one list.
func (m *OS) OpenGotoPickerWith(query string) {
	m.noteGotoPlace()
	g := &m.gotoPicker
	g.open = true
	g.list.Reset()
	g.list.Query.Set(query)
	g.killing = gotoRow{}
	g.tallest = 0
	g.rows, g.sig = m.gotoRows(), m.sidebarSignature()
	g.built = false
}

// CloseGotoPicker hides the picker.
func (m *OS) CloseGotoPicker() {
	g := &m.gotoPicker
	g.open = false
	g.list.Reset()
	g.killing = gotoRow{}
	g.rows, g.filtered, g.built = nil, nil, false
}

// SetGotoPickerQuery replaces the query and puts the cursor on the first row.
func (m *OS) SetGotoPickerQuery(q string) {
	g := &m.gotoPicker
	g.list.Query.Set(q)
	g.list.Refiltered(0)
}

// GotoPickerActivate goes to the selected row and closes the picker.
func (m *OS) GotoPickerActivate() {
	rows := m.gotoFiltered()
	g := &m.gotoPicker
	if g.list.Cursor < 0 || g.list.Cursor >= len(rows) {
		if name, ok := gotoNewSessionName(g.list.Query.String()); ok {
			m.CloseGotoPicker()
			m.OpenOrCreateSession(name)
			return
		}
		m.ShowNotification("Nothing to go to: no place matches "+strings.TrimSpace(g.list.Query.String()), "info", m.Settings.NotificationDuration)
		return
	}
	r := rows[g.list.Cursor]
	m.CloseGotoPicker()
	m.gotoRow(r)
}

func gotoNewSessionName(query string) (string, bool) {
	q := strings.TrimSpace(query)
	switch {
	case strings.HasPrefix(q, "#"):
		q = strings.TrimSpace(q[1:])
	case strings.HasPrefix(q, "kind:session "):
		q = strings.TrimSpace(strings.TrimPrefix(q, "kind:session "))
	default:
		return "", false
	}
	if q == "" || strings.ContainsAny(q, ":#@!") {
		return "", false
	}
	return q, true
}

func gotoPicker() *listPicker {
	return &listPicker{
		kind:    "goto",
		state:   func(m *OS) *listnav.State { return &m.gotoPicker.list },
		count:   func(m *OS) int { return len(m.gotoFiltered()) },
		visible: (*OS).gotoVisible,
		keys:    gotoPickerKeys,
		accept: func(m *OS) tea.Cmd {
			m.GotoPickerActivate()
			return m.TakeSidebarCmd()
		},
		close: func(m *OS) tea.Cmd {
			m.CloseGotoPicker()
			return nil
		},
		clearKey: true,
	}
}

func gotoPickerKeys(m *OS, key string) (tea.Cmd, bool) {
	g := &m.gotoPicker
	if g.killing.node.ID != "" {
		switch key {
		case "y", "Y", "enter":
			id := g.killing.node.ID
			g.killing = gotoRow{}
			m.KillOtherSession(id)
		case "n", "N", "esc":
			g.killing = gotoRow{}
		}
		return nil, true
	}
	if key != "ctrl+r" && key != "ctrl+d" {
		return nil, false
	}
	rows := m.gotoFiltered()
	if g.list.Cursor < 0 || g.list.Cursor >= len(rows) {
		return nil, true
	}
	r := rows[g.list.Cursor]
	note := func(s string) { m.ShowNotification(s, "info", m.Settings.NotificationDuration) }
	switch {
	case key == "ctrl+r" && r.kind == gotoSession:
		if r.host != "" {
			note("Switch to " + r.host + " to rename this session")
			return nil, true
		}
		m.BeginRenameSession(r.node.ID)
	case key == "ctrl+r" && r.kind == gotoWorkspace:
		if r.session != m.sidebarCurrentSessionID() {
			note("Switch to the session to rename its workspace")
			return nil, true
		}
		m.BeginRenameWorkspace(r.ws)
	case key == "ctrl+d" && r.kind == gotoSession:
		switch {
		case r.host != "":
			note("Switch to " + r.host + " to delete this session")
		case r.current:
			m.ShowNotification("Cannot delete the current session", "warning", m.Settings.NotificationDuration)
		default:
			g.killing = r
		}
	}
	return nil, true
}

func (m *OS) gotoRow(r gotoRow) {
	switch r.kind {
	case gotoAgent:
		m.inboxJump(session.AttentionItem{Session: r.session, Window: r.window, Host: r.host})
	case gotoSession:
		if !r.current {
			m.OpenSessionNode(r.node)
		}
	case gotoWorkspace:
		if r.session != m.sidebarCurrentSessionID() && !m.openSession("", r.session) {
			return
		}
		if r.ws != m.dockWorkspace() {
			m.SwitchToWorkspace(r.ws)
		}
	case gotoPane:
		m.sidebarFocusWindow(sidebarRowHit{
			Kind: sidebarRowWindow, SessionID: r.session, WindowID: r.window, WindowIndex: -1,
		})
	}
}

func (m *OS) noteGotoPlace() {
	if m.FocusedWindow < 0 || m.FocusedWindow >= len(m.Windows) || m.Windows[m.FocusedWindow] == nil {
		return
	}
	here := gotoPlace{host: m.AttachedHost, session: m.sidebarCurrentSessionID(), window: m.Windows[m.FocusedWindow].ID}
	g := &m.gotoPicker
	if here == g.here {
		return
	}
	prev := g.here
	g.here = here
	if prev.window == "" {
		return
	}
	g.recent = slices.DeleteFunc(g.recent, func(p gotoPlace) bool { return p == prev || p == here })
	g.recent = slices.Insert(g.recent, 0, prev)
	if len(g.recent) > gotoRecentMax {
		g.recent = g.recent[:gotoRecentMax]
	}
}

func (m *OS) gotoRows() []gotoRow {
	tree := m.BuildSessionTree().Sessions
	var agents, sessions, workspaces, panes []gotoRow
	agentPane := map[string]bool{}
	nodeOf := map[string]sessiontree.Node{}
	for _, s := range tree {
		if s.Kind == sessiontree.KindSession {
			nodeOf[s.Host+"\x00"+s.ID] = s
		}
	}
	for _, e := range m.agentEntries(tree) {
		s := nodeOf[e.Host+"\x00"+e.SessionID]
		r := gotoSessionRow(s)
		r.kind = gotoAgent
		r.window, r.title = e.WindowID, e.Title
		r.state, r.harness, r.message, r.agentKind = e.State, e.Harness, e.Message, e.AgentKind
		r.doneSeen, r.subagents, r.stateAt = e.DoneSeen, e.Subagents, e.StateAt
		r.current = e.Focused
		for _, w := range s.Children {
			if w.ID == e.WindowID {
				r.ws, r.wsLabel = w.Workspace, m.gotoWsLabel(s, w.Workspace)
				r.wsNumber = strconv.Itoa(m.workspaceDisplayNumber(r.ws))
			}
		}
		agents = append(agents, r)
		agentPane[e.Host+"\x00"+e.SessionID+"\x00"+e.WindowID] = true
	}
	for _, s := range tree {
		if s.Kind != sessiontree.KindSession {
			continue
		}
		sessions = append(sessions, gotoSessionRow(s))
		if s.Host != "" || s.Global {
			continue
		}
		count := map[int]int{}
		var dirOf map[string]string
		if s.IsCurrent {
			dirOf = make(map[string]string, len(m.Windows))
			for _, w := range m.Windows {
				if d := w.CWD(); d != "" {
					dirOf[w.ID] = pathroot.ContractUserHome(d)
				}
			}
		}
		for _, w := range s.Children {
			count[w.Workspace]++
			if agentPane["\x00"+s.ID+"\x00"+w.ID] {
				continue
			}
			r := gotoSessionRow(s)
			r.kind = gotoPane
			r.window, r.title, r.dir = w.ID, w.Title, dirOf[w.ID]
			r.ws, r.wsLabel = w.Workspace, m.gotoWsLabel(s, w.Workspace)
			r.wsNumber = strconv.Itoa(m.workspaceDisplayNumber(r.ws))
			r.current = s.IsCurrent && w.IsCurrent
			panes = append(panes, r)
		}
		for _, n := range m.gotoWorkspaces(s, count) {
			r := gotoSessionRow(s)
			r.kind = gotoWorkspace
			r.ws, r.wsLabel, r.panes = n, m.gotoWsLabel(s, n), count[n]
			r.wsNumber = strconv.Itoa(m.workspaceDisplayNumber(r.ws))
			r.current = s.IsCurrent && n == m.dockWorkspace()
			workspaces = append(workspaces, r)
		}
	}
	return slices.Concat(agents, sessions, workspaces, panes)
}

func gotoSessionRow(s sessiontree.Node) gotoRow {
	r := gotoRow{
		kind: gotoSession, node: s, host: s.Host, session: s.ID,
		sessionLabel: s.Title, space: s.Space, panes: s.WindowCount, current: s.IsCurrent,
	}
	if s.Host != "" {
		r.session = remoteSessionName(s)
	}
	if s.Worktree != nil {
		r.group = s.Worktree.Group
	}
	return r
}

func (m *OS) gotoWorkspaces(s sessiontree.Node, count map[int]int) []int {
	if s.IsCurrent {
		return m.gotoThisSessionWorkspaces()
	}
	out := make([]int, 0, len(count))
	for n := range count {
		if n > 0 {
			out = append(out, n)
		}
	}
	slices.Sort(out)
	return out
}

func (m *OS) gotoWsLabel(s sessiontree.Node, ws int) string {
	if s.IsCurrent {
		return m.WorkspaceLabel(ws)
	}
	return strconv.Itoa(m.workspaceDisplayNumber(ws))
}

type gotoTerm struct {
	key    string
	values []string
}

type gotoQuery struct {
	terms []gotoTerm
	sel   *session.Selector
	text  string
}

func parseGotoQuery(q string) (gotoQuery, error) {
	var out gotoQuery
	q = strings.TrimSpace(q)
	if q != "" {
		if kind, ok := gotoSigils[q[0]]; ok {
			out.terms = append(out.terms, gotoTerm{key: gotoKeyKind, values: []string{kind}})
			q = q[1:]
		}
	}
	selText, text := splitSelectorSearch(q)
	var own, rest []string
	for f := range strings.FieldsSeq(selText) {
		key, _, _ := strings.Cut(f, ":")
		switch strings.ToLower(key) {
		case gotoKeyKind, gotoKeySpace, gotoKeyWs:
			own = append(own, f)
		default:
			rest = append(rest, f)
		}
	}
	selText = strings.Join(rest, " ")
	out.text = text
	for _, f := range own {
		key, raw, _ := strings.Cut(f, ":")
		key = strings.ToLower(key)
		var values []string
		for v := range strings.SplitSeq(raw, ",") {
			if v = strings.TrimSpace(v); v == "" {
				continue
			}
			if key == gotoKeyKind {
				k, ok := gotoKindOf(v)
				if !ok {
					return out, errors.New("kind " + strconv.Quote(v) + " is not a kind. The kinds are agent, session, workspace, pane")
				}
				v = gotoKindNames[k][0]
			} else if _, err := path.Match(v, ""); err != nil {
				return out, errors.New("term " + strconv.Quote(f) + ": " + strconv.Quote(v) + " is not a valid glob")
			}
			values = append(values, v)
		}
		if len(values) == 0 {
			return out, errors.New("term " + strconv.Quote(f) + " has no value")
		}
		out.terms = append(out.terms, gotoTerm{key: key, values: values})
	}
	if selText != "" {
		home, _ := os.UserHomeDir()
		sel, err := session.ParseSelector(selText, home)
		if err != nil {
			msg := err.Error()
			if strings.Contains(msg, "The keys are") {
				msg += ", " + gotoKeyKind + ", " + gotoKeySpace + ", " + gotoKeyWs
			}
			return out, errors.New(msg)
		}
		out.sel = sel
	}
	return out, nil
}

func gotoKindOf(v string) (gotoKind, bool) {
	v = strings.ToLower(v)
	for k, names := range gotoKindNames {
		if slices.Contains(names, v) {
			return k, true
		}
	}
	return 0, false
}

func (q gotoQuery) match(r gotoRow) bool {
	for _, t := range q.terms {
		var subject string
		switch t.key {
		case gotoKeyKind:
			subject = gotoKindNames[r.kind][0]
		case gotoKeySpace:
			subject = r.space
		case gotoKeyWs:
			if r.kind == gotoSession {
				return false
			}
			if !globAny(t.values, r.wsLabel) && !globAny(t.values, r.wsNumber) {
				return false
			}
			continue
		}
		if !globAny(t.values, subject) {
			return false
		}
	}
	if q.sel != nil {
		target := session.SelectorTarget{Host: r.host, Session: r.session, Group: r.group}
		switch r.kind {
		case gotoAgent:
			target.Name, target.State, target.Harness, target.NeedsYou = r.title, r.state, r.harness, r.needsYou()
		case gotoSession:
			target.Name = r.sessionLabel
		case gotoWorkspace:
			target.Name = r.wsLabel
		case gotoPane:
			target.Name = r.title
		}
		if !q.sel.Match(target) {
			return false
		}
	}
	return true
}

func globAny(globs []string, s string) bool {
	if s == "" {
		return false
	}
	for _, g := range globs {
		if ok, _ := path.Match(g, s); ok {
			return true
		}
	}
	return false
}

func gotoSearchText(r gotoRow) string {
	switch r.kind {
	case gotoAgent:
		return r.title + " " + r.harness + " " + r.sessionLabel + " " + r.wsLabel + " " + r.message
	case gotoSession:
		return r.sessionLabel + " " + r.session + " " + r.space + " " + r.host
	case gotoWorkspace:
		return r.sessionLabel + " " + r.wsLabel
	}
	return r.sessionLabel + " " + r.wsLabel + " " + r.title + " " + r.dir
}

func (m *OS) gotoFiltered() []gotoRow {
	g := &m.gotoPicker
	if sig := m.sidebarSignature(); sig != g.sig {
		g.rows, g.sig, g.built = m.gotoRows(), sig, false
	}
	if g.built && g.filteredFor == g.list.Query.String() {
		return g.filtered
	}
	g.filtered, g.parseErr = nil, ""
	g.filteredFor, g.built = g.list.Query.String(), true
	q, err := parseGotoQuery(g.list.Query.String())
	if err != nil {
		g.parseErr = err.Error()
		return nil
	}
	everything := strings.TrimSpace(g.list.Query.String()) == ""
	type scored struct {
		row   gotoRow
		rank  gotoRankKey
		score int
	}
	var kept []scored
	for i, r := range g.rows {
		if everything && r.kind == gotoPane && m.gotoRecentAt(r) < 0 {
			continue
		}
		if !q.match(r) {
			continue
		}
		score := 0
		if q.text != "" {
			s, ok := fuzzy.Score(q.text, gotoSearchText(r))
			if !ok {
				continue
			}
			score = s
		}
		kept = append(kept, scored{row: r, rank: m.gotoRank(r, i), score: score})
	}
	slices.SortStableFunc(kept, func(a, b scored) int {
		if a.score != b.score {
			return b.score - a.score
		}
		if a.rank.tier != b.rank.tier {
			return a.rank.tier - b.rank.tier
		}
		return cmp.Compare(a.rank.at, b.rank.at)
	})
	g.filtered = make([]gotoRow, len(kept))
	for i, k := range kept {
		g.filtered[i] = k.row
	}
	return g.filtered
}

type gotoRankKey struct {
	tier int
	at   int64
}

func (m *OS) gotoRank(r gotoRow, i int) gotoRankKey {
	at := m.gotoRecentAt(r)
	switch {
	case at == 0:
		return gotoRankKey{0, 0}
	case r.needsYou():
		return gotoRankKey{1, r.stateAt}
	case at > 0:
		return gotoRankKey{2, int64(at)}
	}
	return gotoRankKey{3, int64(i)}
}

func (m *OS) gotoRecentAt(r gotoRow) int {
	if r.kind != gotoAgent && r.kind != gotoPane {
		return -1
	}
	g := &m.gotoPicker
	at := 0
	for _, p := range g.recent {
		if p == g.here {
			continue
		}
		if p.session == r.session && p.window == r.window && p.host == r.host {
			return at
		}
		at++
	}
	return -1
}
