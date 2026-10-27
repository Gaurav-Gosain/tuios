package app

import (
	"cmp"
	"errors"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/pkg/fuzzy"
)

// The go-to picker (goto_picker): every place the client can go, ranked in
// one list. A place is an agent, a session, a workspace of a session, or a
// pane. The rows come from the tree the rail draws (BuildSessionTree) and the
// rail's agent model (agentEntries), so the picker and the rail cannot
// disagree about what exists. Enter goes through the paths those surfaces
// already use: inboxJump for an agent, OpenSessionNode for a session,
// openSession and SwitchToWorkspace for a workspace, sidebarFocusWindow for a
// pane.
//
// The query is the Agents view's search line (splitSelectorSearch): key:value
// terms are a selector, read by session.ParseSelector, and the other words are
// a fuzzy search. The picker adds three keys that name places rather than
// panes, kind:, space: and ws:, with the selector's syntax: a value is a glob,
// commas are OR, terms are AND. A leading @, # or ! is kind:agent,
// kind:session or kind:workspace.
//
// With nothing typed the list is the place the client was on last, so Enter
// goes back, then the agents that need you, then the other places the client
// was on recently, then every agent, session and workspace. Panes
// are listed once something is typed: there are many more of them than of
// anything else, and the ones worth going to with nothing typed are the recent
// ones, which are listed anyway.

// gotoKind is what a row is a place of.
type gotoKind int

const (
	gotoAgent gotoKind = iota
	gotoSession
	gotoWorkspace
	gotoPane
)

// gotoKindNames are the values kind: takes, by kind, the first being the one
// a row's tag shows. ws is kept as a short alias for typing.
var gotoKindNames = map[gotoKind][]string{
	gotoAgent:     {"agent", "agents"},
	gotoSession:   {"session", "sessions"},
	gotoWorkspace: {"workspace", "workspaces", "ws"},
	gotoPane:      {"pane", "panes", "window"},
}

// gotoSigils are the leading characters that stand for a kind: term.
var gotoSigils = map[byte]string{'@': "agent", '#': "session", '!': "workspace"}

// The keys the picker reads itself. Every other key goes to the selector.
const (
	gotoKeyKind  = "kind"
	gotoKeySpace = "space"
	gotoKeyWs    = "ws"
)

// gotoRecentMax bounds the places remembered.
const gotoRecentMax = 16

// gotoPlace is where the client was: a pane of a session.
type gotoPlace struct {
	host, session, window string
}

// gotoRow is one place the picker lists.
type gotoRow struct {
	kind gotoKind
	// node is the session row's tree node, which OpenSessionNode takes.
	node         sessiontree.Node
	host         string
	session      string
	sessionLabel string
	space        string
	group        string
	ws           int
	wsLabel      string
	window       string
	title        string
	panes        int
	current      bool
	// The agent's state, for an agent row.
	state, harness, message, agentKind string
	doneSeen                           bool
	subagents                          int
	stateAt                            int64
}

// needsYou reports whether the row is an agent blocked on a person.
func (r gotoRow) needsYou() bool {
	return r.kind == gotoAgent && sidebarAgentGroup(r.state, r.doneSeen) == sidebarGroupNeedsYou
}

// gotoPickerState is the picker while it is up, and the places the client has
// been, which outlive it.
type gotoPickerState struct {
	open     bool
	query    string
	selected int
	scroll   int
	rows     []gotoRow
	// filtered is the rows the query keeps, in order, built for filteredFor.
	filtered    []gotoRow
	filteredFor string
	built       bool
	parseErr    string
	// sig is the rail's signature the rows were built at. The listing of the
	// other sessions arrives off the UI goroutine, so a picker opened before
	// it lands lists their panes once it has.
	sig uint64

	here   gotoPlace
	recent []gotoPlace
}

// GotoPickerOpen reports whether the picker is up.
func (m *OS) GotoPickerOpen() bool { return m.gotoPicker.open }

// GotoPickerQuery is what is typed in the picker.
func (m *OS) GotoPickerQuery() string { return m.gotoPicker.query }

// OpenGotoPicker is the goto_picker action. Learn mode opens it too: the tab
// has one session, but its workspaces and panes are real places to go.
func (m *OS) OpenGotoPicker() {
	m.noteGotoPlace()
	g := &m.gotoPicker
	g.open = true
	g.query, g.selected, g.scroll = "", 0, 0
	g.rows, g.sig = m.gotoRows(), m.sidebarSignature()
	g.built = false
}

// CloseGotoPicker hides the picker.
func (m *OS) CloseGotoPicker() {
	g := &m.gotoPicker
	g.open = false
	g.query, g.selected, g.scroll = "", 0, 0
	g.rows, g.filtered, g.built = nil, nil, false
}

// SetGotoPickerQuery replaces the query and puts the cursor on the first row.
func (m *OS) SetGotoPickerQuery(q string) {
	g := &m.gotoPicker
	g.query, g.selected, g.scroll = q, 0, 0
}

// GotoPickerCount is how many rows the query keeps.
func (m *OS) GotoPickerCount() int { return len(m.gotoFiltered()) }

// GotoPickerMove steps the cursor by delta.
func (m *OS) GotoPickerMove(delta int) {
	g := &m.gotoPicker
	m.moveListSelection(&g.selected, &g.scroll, len(m.gotoFiltered()), listOverlayRows, delta)
}

// GotoPickerSelect puts the cursor on row idx of the filtered list.
func (m *OS) GotoPickerSelect(idx int) {
	if idx >= 0 && idx < len(m.gotoFiltered()) {
		m.gotoPicker.selected = idx
	}
}

// GotoPickerActivate goes to the selected row and closes the picker.
func (m *OS) GotoPickerActivate() {
	rows := m.gotoFiltered()
	g := &m.gotoPicker
	if g.selected < 0 || g.selected >= len(rows) {
		return
	}
	r := rows[g.selected]
	m.CloseGotoPicker()
	m.gotoRow(r)
}

// gotoRow goes to a row's place.
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

// noteGotoPlace records where the client is. When that changed, where it was
// goes to the front of the recent places. It runs once a message, after
// reconcilePrevFocus, so every way of moving is seen: a session switch, a
// workspace switch, a click, a key.
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

// gotoRows lists every place, in the order the rest of the list keeps: agents,
// sessions, workspaces, panes, each in the rail's order.
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
		// Workspaces and panes are listed for the attached machine's sessions,
		// which a switch over this connection reaches. A session on another
		// machine is gone to by its own row.
		if s.Host != "" || s.Global {
			continue
		}
		count := map[int]int{}
		for _, w := range s.Children {
			count[w.Workspace]++
			if agentPane["\x00"+s.ID+"\x00"+w.ID] {
				continue
			}
			r := gotoSessionRow(s)
			r.kind = gotoPane
			r.window, r.title = w.ID, w.Title
			r.ws, r.wsLabel = w.Workspace, m.gotoWsLabel(s, w.Workspace)
			r.current = s.IsCurrent && w.IsCurrent
			panes = append(panes, r)
		}
		for _, n := range m.gotoWorkspaces(s, count) {
			r := gotoSessionRow(s)
			r.kind = gotoWorkspace
			r.ws, r.wsLabel, r.panes = n, m.gotoWsLabel(s, n), count[n]
			r.current = s.IsCurrent && n == m.dockWorkspace()
			workspaces = append(workspaces, r)
		}
	}
	return slices.Concat(agents, sessions, workspaces, panes)
}

// gotoSessionRow is the row of a session, which every row of a place in it
// starts from.
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

// gotoWorkspaces is a session's workspaces worth going to: for the attached
// session the workspace switcher's, in the dock's order, and for another
// session the ones holding a pane.
func (m *OS) gotoWorkspaces(s sessiontree.Node, count map[int]int) []int {
	if s.IsCurrent {
		var out []int
		for _, w := range m.buildWorkspaceItems() {
			out = append(out, w.Number)
		}
		return out
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

// gotoWsLabel names a workspace of s: its name when the client knows one,
// which it does for the attached session, otherwise its number.
func (m *OS) gotoWsLabel(s sessiontree.Node, ws int) string {
	if s.IsCurrent {
		return m.WorkspaceLabel(ws)
	}
	return strconv.Itoa(ws)
}

// gotoTerm is one kind:, space: or ws: term: any of its globs may match.
type gotoTerm struct {
	key    string
	values []string
}

// gotoQuery is a parsed query.
type gotoQuery struct {
	terms []gotoTerm
	sel   *session.Selector
	text  string
}

// parseGotoQuery reads a query: a leading sigil, the picker's own terms, the
// selector's terms and the fuzzy words.
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

// gotoKindOf reads a kind: value.
func gotoKindOf(v string) (gotoKind, bool) {
	v = strings.ToLower(v)
	for k, names := range gotoKindNames {
		if slices.Contains(names, v) {
			return k, true
		}
	}
	return 0, false
}

// match reports whether a row passes every term of the query. A term a row
// cannot answer, such as ws: on a session or harness: on a pane, excludes it.
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
			if !globAny(t.values, r.wsLabel) && !globAny(t.values, strconv.Itoa(r.ws)) {
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

// globAny reports whether any glob matches s. An empty s matches none.
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

// gotoSearchText is what the fuzzy words are matched against: the names a
// row shows.
func gotoSearchText(r gotoRow) string {
	switch r.kind {
	case gotoAgent:
		return r.title + " " + r.harness + " " + r.sessionLabel + " " + r.wsLabel + " " + r.message
	case gotoSession:
		return r.sessionLabel + " " + r.session + " " + r.space + " " + r.host
	case gotoWorkspace:
		return r.sessionLabel + " " + r.wsLabel
	}
	return r.sessionLabel + " " + r.wsLabel + " " + r.title
}

// gotoFiltered is the rows the query keeps, in the order they are listed.
func (m *OS) gotoFiltered() []gotoRow {
	g := &m.gotoPicker
	if sig := m.sidebarSignature(); sig != g.sig {
		g.rows, g.sig, g.built = m.gotoRows(), sig, false
	}
	if g.built && g.filteredFor == g.query {
		return g.filtered
	}
	g.filtered, g.parseErr = nil, ""
	g.filteredFor, g.built = g.query, true
	q, err := parseGotoQuery(g.query)
	if err != nil {
		g.parseErr = err.Error()
		return nil
	}
	everything := strings.TrimSpace(g.query) == ""
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

// gotoRankKey orders the rows the fuzzy words score alike: by tier, then by
// at within it.
type gotoRankKey struct {
	tier int
	at   int64
}

// gotoRank orders the rows with no fuzzy words to rank them: the place the
// client left last, so Enter on an empty query goes back, then agents that
// need you, the longest waiting first, then the other recent places, the most
// recent first, then the rest in the order gotoRows lists them.
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

// gotoRecentAt is where a row's pane is in the recent places, counting from 0
// for the place left last, -1 when it is not one or is where the client is
// now.
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
