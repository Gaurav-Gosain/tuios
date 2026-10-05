package app

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/pkg/fuzzy"
	"github.com/charmbracelet/x/ansi"
)

// The pane navigator, after tmux's choose-tree and fzf-pane-switch.
//
// The choose_tree action opens a full-screen overlay: a tree of sessions,
// their workspaces and their panes, on this machine and on the machines in
// [hosts], beside a preview of the highlighted pane. Typing after / filters
// the panes by their name, title, folder, running command and screen text.
// Enter goes to the highlighted row: it switches the session, the workspace
// and the pane.
//
// What it reads, and when. The tree opens at once from what this client
// already holds: the attached session's panes and the rail's cached listing of
// the others. One command then asks each daemon for the rest (each pane's
// folder and the last navTextLines lines of its screen) off the UI goroutine,
// and the rows fill in when it answers. Nothing is kept once the navigator
// closes, so there is no index to keep up to date.
//
// The navigator is the person's own client, so it reads their panes with the
// person's own connection. The CLI form, list-windows --all --text, reads the
// same text through capture-pane, under that verb's grants.

// navTextLines is how many of a pane's last lines are searched and previewed.
const navTextLines = 40

// navMaxCaptures caps the panes whose text one load reads, across every
// session and machine.
const navMaxCaptures = 150

// navCallTimeout bounds one verb call of a load. navLoadBudget bounds the
// whole load: a machine that is slow to answer costs its own rows and not the
// rest.
const (
	navCallTimeout = 2 * time.Second
	navLoadBudget  = 8 * time.Second
)

// navPane is one pane in the navigator.
type navPane struct {
	ID         string
	Name       string
	Title      string
	Command    string
	Cwd        string
	Workspace  int
	Focused    bool
	AgentState string
	// Text is the last lines of the pane's screen, oldest first. For a pane
	// this client draws, the preview reads the live screen instead.
	Text []string
}

// navSession is one session in the navigator.
type navSession struct {
	// Host is the machine the session is on. Empty is the machine this client
	// is attached to.
	Host  string
	Name  string
	Title string
	// Current is the session this client shows.
	Current bool
	// Note says why the session's panes are not listed: its machine is not
	// up, or it did not answer.
	Note           string
	WorkspaceNames map[int]string
	Panes          []navPane
	// Count is how many panes the session has, from the listing, for a
	// session whose panes are not known yet.
	Count int
}

// key is the session's identity in the navigator.
func (s *navSession) key() string { return s.Host + "\x00" + s.Name }

// workspaceLabel is a workspace's number, with its name when it has one.
func (s *navSession) workspaceLabel(ws int) string {
	n := strconv.Itoa(ws)
	if name := s.WorkspaceNames[ws]; name != "" {
		return n + " " + name
	}
	return n
}

// workspaces is the workspaces that hold a pane, in order.
func (s *navSession) workspaces() []int {
	var out []int
	for _, p := range s.Panes {
		if !slices.Contains(out, p.Workspace) {
			out = append(out, p.Workspace)
		}
	}
	slices.Sort(out)
	return out
}

// navRowKind is what a navigator row stands for.
type navRowKind int

const (
	navRowSession navRowKind = iota
	navRowWorkspace
	navRowPane
)

// navRow is one row of the list.
type navRow struct {
	Kind      navRowKind
	Session   int
	Workspace int
	Pane      int
	// Snippet is the screen line a search found the pane by, when it was
	// found by its text and not by its name.
	Snippet string
}

// navigatorState is the navigator while it is open.
type navigatorState struct {
	open     bool
	sessions []navSession
	// expanded holds the rows a person opened or closed, by key. A row not
	// in it has its default: the current session open, the others shut,
	// and every workspace of an open session open.
	expanded  map[string]bool
	query     string
	searching bool
	cursor    int
	scroll    int
	// loading is true while the detail load is out. gen tells its answer
	// from the answer to a navigator since closed.
	loading bool
	gen     uint64
}

// NavigatorLoadedMsg is the answer of a detail load.
type NavigatorLoadedMsg struct {
	Gen      uint64
	Sessions []navSession
}

// NavigatorOpen reports whether the navigator is up.
func (m *OS) NavigatorOpen() bool { return m.navigator.open }

// NavigatorSearching reports whether the keyboard is in the search line.
func (m *OS) NavigatorSearching() bool { return m.navigator.searching }

// NavigatorQuery is the search typed so far.
func (m *OS) NavigatorQuery() string { return m.navigator.query }

// OpenNavigator is the choose_tree action. It opens the tree on the current
// pane and starts the detail load.
func (m *OS) OpenNavigator() tea.Cmd {
	if m.learnOff(learnNoteSessions) {
		return nil
	}
	m.navigator.gen++
	m.navigator = navigatorState{
		open:     true,
		gen:      m.navigator.gen,
		expanded: map[string]bool{},
		sessions: m.navigatorSessions(),
	}
	m.navigatorCursorToCurrent()
	m.MarkAllDirty()
	if m.DaemonClient == nil {
		return nil
	}
	m.navigator.loading = true
	return m.navigatorLoad()
}

// CloseNavigator takes the navigator down and drops what it read.
func (m *OS) CloseNavigator() {
	if !m.navigator.open {
		return
	}
	m.navigator = navigatorState{gen: m.navigator.gen}
	m.MarkAllDirty()
}

// navigatorCursorToCurrent puts the cursor on the focused pane of the
// current session.
func (m *OS) navigatorCursorToCurrent() {
	for i, r := range m.navigatorRows() {
		if r.Kind != navRowPane {
			continue
		}
		s := &m.navigator.sessions[r.Session]
		if s.Current && s.Panes[r.Pane].Focused {
			m.navigator.cursor = i
			return
		}
	}
}

// navigatorSessions is the tree as this client holds it now: the attached
// session's panes from live state, the other sessions on the attached
// machine from the rail's cached listing, and the sessions of every other
// machine by name only.
func (m *OS) navigatorSessions() []navSession {
	var out []navSession
	current := m.navigatorCurrentSession()
	tree := m.BuildSessionTree()
	seen := false
	for _, n := range tree.Sessions {
		if isRemoteNode(n) {
			continue
		}
		if n.IsCurrent || n.ID == current.Name {
			current.Title = n.Title
			out = append(out, current)
			seen = true
			continue
		}
		s := navSession{Name: n.ID, Title: n.Title, Count: n.WindowCount}
		for _, w := range n.Children {
			s.Panes = append(s.Panes, navPane{ID: w.ID, Name: w.Title, Workspace: w.Workspace, AgentState: w.AgentState})
		}
		if len(s.Panes) > 0 {
			s.Count = len(s.Panes)
		}
		out = append(out, s)
	}
	if !seen {
		out = append([]navSession{current}, out...)
	}
	for _, h := range m.FederationHosts {
		if h.Name == m.attachedMachine() {
			continue
		}
		for _, fs := range h.Sessions {
			title := fs.Name
			if fs.DisplayName != "" {
				title = fs.DisplayName
			}
			s := navSession{Host: h.Name, Name: fs.Name, Title: title, Count: fs.WindowCount}
			if h.Status != string(federation.StatusUp) {
				s.Note = h.Name + " is not reachable"
			}
			out = append(out, s)
		}
	}
	return out
}

// navigatorCurrentSession is the attached session from live state, with each
// pane's screen text read from this client's own copy of it.
func (m *OS) navigatorCurrentSession() navSession {
	name := m.SessionName
	if name == "" {
		name = "local"
	}
	s := navSession{Name: name, Title: name, Current: true, WorkspaceNames: map[int]string{}}
	for ws, n := range m.WorkspaceNames {
		s.WorkspaceNames[ws] = n
	}
	for i, w := range m.Windows {
		if w == nil || isScratch(w) {
			continue
		}
		cwd := w.Cwd
		if cwd == "" {
			cwd = w.DaemonCwd
		}
		s.Panes = append(s.Panes, navPane{
			ID:         w.ID,
			Name:       m.getWindowDisplayName(w),
			Title:      w.Title(),
			Command:    w.ForegroundCmd,
			Cwd:        cwd,
			Workspace:  w.Workspace,
			Focused:    i == m.FocusedWindow,
			AgentState: w.AgentState,
			Text:       navScreenText(w, navTextLines),
		})
	}
	s.Count = len(s.Panes)
	return s
}

// navScreenText is the last n non-blank-tailed lines of a pane's screen as
// plain text, read from the client's own copy.
func navScreenText(w *terminal.Window, n int) []string {
	if w == nil || w.Terminal == nil {
		return nil
	}
	w.RLockIO()
	defer w.RUnlockIO()
	width, height := w.Terminal.Width(), w.Terminal.Height()
	lines := make([]string, 0, height)
	var b strings.Builder
	for y := range height {
		b.Reset()
		for x := 0; x < width; x++ {
			c := w.Terminal.CellAt(x, y)
			switch {
			case c == nil:
				b.WriteByte(' ')
			case c.Content == "" && c.Width == 0:
				// The tail of a wide glyph.
			case c.Content == "":
				b.WriteByte(' ')
			default:
				b.WriteString(c.Content)
			}
		}
		lines = append(lines, strings.TrimRight(b.String(), " "))
	}
	return navLastLines(lines, n)
}

// navLastLines drops the blank lines at the end and keeps the last n.
func navLastLines(lines []string, n int) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

// navigatorLoad reads what the tree does not hold yet, off the UI goroutine:
// for every session but the current one, its panes with their folders and
// commands, its workspace names, and each pane's last lines.
func (m *OS) navigatorLoad() tea.Cmd {
	gen := m.navigator.gen
	build := m.DaemonClient.ClientVersion()
	attached := m.attachedMachine()
	type job struct {
		host, name string
	}
	var jobs []job
	for _, s := range m.navigator.sessions {
		if s.Current || s.Note != "" {
			continue
		}
		jobs = append(jobs, job{host: s.Host, name: s.Name})
	}
	if len(jobs) == 0 {
		return func() tea.Msg { return NavigatorLoadedMsg{Gen: gen} }
	}
	return func() tea.Msg {
		deadline := time.Now().Add(navLoadBudget)
		clients := map[string]*session.VerbClient{}
		defer func() {
			for _, c := range clients {
				_ = c.Close()
			}
		}()
		dial := func(host string) *session.VerbClient {
			machine := host
			if machine == "" {
				machine = attached
			}
			if c, ok := clients[machine]; ok {
				return c
			}
			var c *session.VerbClient
			var err error
			if machine == federation.LocalHostName {
				c, err = session.DialVerbClientAs(build)
			} else {
				c, _, err = session.DialVerbClientThroughHost(machine, build)
			}
			if err != nil {
				c = nil
			}
			clients[machine] = c
			return c
		}
		captures := 0
		out := make([]navSession, 0, len(jobs))
		for _, j := range jobs {
			if time.Now().After(deadline) {
				break
			}
			c := dial(j.host)
			if c == nil {
				out = append(out, navSession{Host: j.host, Name: j.name, Note: "Could not reach this session"})
				continue
			}
			s, ok := navLoadSession(c, j.host, j.name)
			if !ok {
				out = append(out, navSession{Host: j.host, Name: j.name, Note: "Could not read this session"})
				continue
			}
			for i := range s.Panes {
				if captures >= navMaxCaptures || time.Now().After(deadline) {
					break
				}
				captures++
				s.Panes[i].Text = navCapture(c, j.name, s.Panes[i].ID)
			}
			out = append(out, s)
		}
		return NavigatorLoadedMsg{Gen: gen, Sessions: out}
	}
}

// navLoadSession reads a session's panes and workspace names.
func navLoadSession(c *session.VerbClient, host, name string) (navSession, bool) {
	raw, err := c.CallWithTimeout("list-windows", map[string]any{"session": name}, navCallTimeout)
	if err != nil {
		return navSession{}, false
	}
	var list struct {
		Focused string `json:"focused_window_id"`
		Windows []struct {
			ID         string `json:"window_id"`
			Display    string `json:"display_name"`
			Title      string `json:"title"`
			Cwd        string `json:"cwd"`
			Command    string `json:"foreground_cmd"`
			Workspace  int    `json:"workspace"`
			Scratch    bool   `json:"scratch"`
			AgentState string `json:"agent_state"`
		} `json:"windows"`
	}
	if json.Unmarshal(raw, &list) != nil {
		return navSession{}, false
	}
	s := navSession{Host: host, Name: name, WorkspaceNames: map[int]string{}}
	for _, w := range list.Windows {
		if w.Scratch {
			continue
		}
		state := w.AgentState
		if state == "none" {
			state = ""
		}
		s.Panes = append(s.Panes, navPane{
			ID: w.ID, Name: w.Display, Title: w.Title, Cwd: w.Cwd, Command: w.Command,
			Workspace: w.Workspace, Focused: w.ID == list.Focused, AgentState: state,
		})
	}
	s.Count = len(s.Panes)
	if raw, err := c.CallWithTimeout("session-info", map[string]any{"session": name}, navCallTimeout); err == nil {
		var info struct {
			Names map[string]string `json:"workspace_names"`
		}
		if json.Unmarshal(raw, &info) == nil {
			for k, v := range info.Names {
				if n, err := strconv.Atoi(k); err == nil {
					s.WorkspaceNames[n] = v
				}
			}
		}
	}
	return s, true
}

// navCapture reads a pane's last lines with capture-pane, or nil.
func navCapture(c *session.VerbClient, sessionName, window string) []string {
	raw, err := c.CallWithTimeout("capture-pane", map[string]any{
		"session": sessionName, "window": window, "source": "recent", "lines": navTextLines,
	}, navCallTimeout)
	if err != nil {
		return nil
	}
	var res struct {
		Content string `json:"content"`
	}
	if json.Unmarshal(raw, &res) != nil {
		return nil
	}
	lines := strings.Split(ansi.Strip(res.Content), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \r")
	}
	return navLastLines(lines, navTextLines)
}

// ApplyNavigatorLoaded merges a detail load into the tree. The cursor stays
// on the row it was on.
func (m *OS) ApplyNavigatorLoaded(msg NavigatorLoadedMsg) {
	nav := &m.navigator
	if !nav.open || msg.Gen != nav.gen {
		return
	}
	nav.loading = false
	before, had := m.navigatorSelectedKey()
	for _, loaded := range msg.Sessions {
		for i := range nav.sessions {
			s := &nav.sessions[i]
			if s.key() != loaded.key() {
				continue
			}
			title, note := s.Title, loaded.Note
			if note != "" && len(s.Panes) > 0 {
				// Keep the cached rows. The note says they could not be
				// read fresh.
				s.Note = ""
				break
			}
			*s = loaded
			s.Title = title
			s.Note = note
			break
		}
	}
	if had {
		m.navigatorSelectKey(before)
	}
	m.MarkAllDirty()
}

// navRowKey names a row so the cursor can find it again after the tree
// changes under it.
func (m *OS) navRowKey(r navRow) string {
	s := &m.navigator.sessions[r.Session]
	switch r.Kind {
	case navRowWorkspace:
		return s.key() + "\x00ws" + strconv.Itoa(r.Workspace)
	case navRowPane:
		return s.key() + "\x00pane" + s.Panes[r.Pane].ID
	}
	return s.key()
}

// navigatorSelectedKey is the key of the row under the cursor.
func (m *OS) navigatorSelectedKey() (string, bool) {
	rows := m.navigatorRows()
	if m.navigator.cursor < 0 || m.navigator.cursor >= len(rows) {
		return "", false
	}
	return m.navRowKey(rows[m.navigator.cursor]), true
}

// navigatorSelectKey puts the cursor on the row with key, when it is listed.
func (m *OS) navigatorSelectKey(key string) {
	for i, r := range m.navigatorRows() {
		if m.navRowKey(r) == key {
			m.navigator.cursor = i
			return
		}
	}
}

// navExpanded reports whether a session or a workspace row is open.
func (m *OS) navExpanded(key string, def bool) bool {
	if v, ok := m.navigator.expanded[key]; ok {
		return v
	}
	return def
}

// navigatorRows is the list as it is drawn: the tree, or with a search the
// panes that match, best first.
func (m *OS) navigatorRows() []navRow {
	if strings.TrimSpace(m.navigator.query) != "" {
		return m.navigatorSearchRows()
	}
	var rows []navRow
	for si := range m.navigator.sessions {
		s := &m.navigator.sessions[si]
		rows = append(rows, navRow{Kind: navRowSession, Session: si})
		if !m.navExpanded(s.key(), s.Current) {
			continue
		}
		for _, ws := range s.workspaces() {
			rows = append(rows, navRow{Kind: navRowWorkspace, Session: si, Workspace: ws})
			if !m.navExpanded(s.key()+"\x00ws"+strconv.Itoa(ws), true) {
				continue
			}
			for pi, p := range s.Panes {
				if p.Workspace == ws {
					rows = append(rows, navRow{Kind: navRowPane, Session: si, Workspace: ws, Pane: pi})
				}
			}
		}
	}
	return rows
}

// navigatorSearchRows is every pane that matches the query, best first. A
// pane's name, title, command, folder, session and workspace are matched
// fuzzily. Its screen text is matched as typed, ignoring case: a fuzzy match
// over a screen of text finds nearly anything. A session whose panes are not
// known is listed when its name matches.
func (m *OS) navigatorSearchRows() []navRow {
	q := strings.TrimSpace(m.navigator.query)
	lq := strings.ToLower(q)
	type scored struct {
		row   navRow
		score int
	}
	var hits []scored
	var mt fuzzy.Matcher
	for si := range m.navigator.sessions {
		s := &m.navigator.sessions[si]
		if len(s.Panes) == 0 {
			if r, ok := mt.Find(q, s.Title+" "+s.Name+" "+s.Host); ok {
				hits = append(hits, scored{navRow{Kind: navRowSession, Session: si}, r.Score})
			}
			continue
		}
		for pi, p := range s.Panes {
			fields := strings.Join([]string{p.Name, p.Title, p.Command, p.Cwd, s.Title, s.Host, s.WorkspaceNames[p.Workspace]}, " ")
			row := navRow{Kind: navRowPane, Session: si, Workspace: p.Workspace, Pane: pi}
			if r, ok := mt.Find(q, fields); ok {
				hits = append(hits, scored{row, r.Score + 1<<20})
				continue
			}
			for i := len(p.Text) - 1; i >= 0; i-- {
				if strings.Contains(strings.ToLower(p.Text[i]), lq) {
					row.Snippet = strings.TrimSpace(p.Text[i])
					hits = append(hits, scored{row, 0})
					break
				}
			}
		}
	}
	slices.SortStableFunc(hits, func(a, b scored) int { return b.score - a.score })
	rows := make([]navRow, len(hits))
	for i, h := range hits {
		rows[i] = h.row
	}
	return rows
}

// NavigatorRowCount is how many rows the list has.
func (m *OS) NavigatorRowCount() int { return len(m.navigatorRows()) }

// NavigatorMove moves the cursor by delta rows.
func (m *OS) NavigatorMove(delta int) {
	n := m.NavigatorRowCount()
	if n == 0 {
		return
	}
	m.moveListSelection(&m.navigator.cursor, &m.navigator.scroll, n, m.navigatorVisibleRows(), delta)
	m.MarkAllDirty()
}

// NavigatorSelect puts the cursor on row i.
func (m *OS) NavigatorSelect(i int) {
	if i >= 0 && i < m.NavigatorRowCount() {
		m.navigator.cursor = i
		m.MarkAllDirty()
	}
}

// NavigatorFold opens (open true) or shuts the session or workspace row
// under the cursor. On a pane row, shutting goes to its workspace row, and on
// a shut row or a pane it does nothing more. It does nothing in a search.
func (m *OS) NavigatorFold(open bool) {
	if m.navigator.query != "" {
		return
	}
	rows := m.navigatorRows()
	if m.navigator.cursor < 0 || m.navigator.cursor >= len(rows) {
		return
	}
	r := rows[m.navigator.cursor]
	s := &m.navigator.sessions[r.Session]
	key := m.navRowKey(r)
	switch r.Kind {
	case navRowPane:
		if !open {
			m.navigatorSelectKey(s.key() + "\x00ws" + strconv.Itoa(r.Workspace))
		}
	case navRowWorkspace:
		if !open && !m.navExpanded(key, true) {
			m.navigatorSelectKey(s.key())
			break
		}
		m.navigator.expanded[key] = open
	case navRowSession:
		m.navigator.expanded[key] = open
	}
	m.MarkAllDirty()
}

// NavigatorToggle opens a shut row and shuts an open one.
func (m *OS) NavigatorToggle() {
	rows := m.navigatorRows()
	if m.navigator.query != "" || m.navigator.cursor < 0 || m.navigator.cursor >= len(rows) {
		return
	}
	r := rows[m.navigator.cursor]
	if r.Kind == navRowPane {
		return
	}
	def := r.Kind == navRowWorkspace || m.navigator.sessions[r.Session].Current
	key := m.navRowKey(r)
	m.navigator.expanded[key] = !m.navExpanded(key, def)
	m.MarkAllDirty()
}

// NavigatorSearch moves the keyboard to the search line, or back to the list.
func (m *OS) NavigatorSearch(on bool) {
	m.navigator.searching = on
	m.MarkAllDirty()
}

// NavigatorSetQuery changes the search. The cursor goes to the best match.
func (m *OS) NavigatorSetQuery(q string) {
	m.navigator.query = q
	m.navigator.cursor, m.navigator.scroll = 0, 0
	if q == "" {
		m.navigatorCursorToCurrent()
	}
	m.MarkAllDirty()
}

// NavigatorActivate goes to row i: a session row switches to the session, a
// workspace row to the workspace in it, and a pane row to the pane. The
// navigator closes first.
func (m *OS) NavigatorActivate(i int) {
	rows := m.navigatorRows()
	if i < 0 || i >= len(rows) {
		return
	}
	r := rows[i]
	s := m.navigator.sessions[r.Session]
	var paneID string
	if r.Kind == navRowPane {
		paneID = s.Panes[r.Pane].ID
	}
	m.CloseNavigator()
	m.sidebarLeaveForJump()
	if !s.Current {
		if s.Host != "" && !m.hostIsUp(s.Host) {
			m.reportSwitchFailure(&hostUnavailableError{host: s.Host})
			return
		}
		if !m.openSession(s.Host, s.Name) {
			return
		}
	}
	switch r.Kind {
	case navRowWorkspace:
		if r.Workspace > 0 && r.Workspace != m.CurrentWorkspace {
			m.SwitchToWorkspace(r.Workspace)
		}
	case navRowPane:
		m.focusWindowByID(paneID)
	}
}

// NavigatorClickFolds folds the session or workspace row i on a click, and
// reports whether it did. A pane row is not folded: a click goes to it.
func (m *OS) NavigatorClickFolds(i int) bool {
	rows := m.navigatorRows()
	if m.navigator.query != "" || i < 0 || i >= len(rows) || rows[i].Kind == navRowPane {
		return false
	}
	m.navigator.cursor = i
	m.NavigatorToggle()
	return true
}

// NavigatorActivateSelected goes to the row under the cursor.
func (m *OS) NavigatorActivateSelected() { m.NavigatorActivate(m.navigator.cursor) }
