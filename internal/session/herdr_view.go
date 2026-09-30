package session

import (
	"cmp"
	"encoding/json"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/gitstate"
)

// herdr's records of tuios's objects: the shapes of herdr's
// src/api/schema/{workspaces,tabs,panes,agents,session}.rs at the version in
// herdrTargetVersion, built from tuios's session state. See herdr_api.go for
// the mapping.

// herdrWorkspace is herdr's WorkspaceInfo.
type herdrWorkspace struct {
	WorkspaceID string                  `json:"workspace_id"`
	Number      int                     `json:"number"`
	Label       string                  `json:"label"`
	Focused     bool                    `json:"focused"`
	PaneCount   int                     `json:"pane_count"`
	TabCount    int                     `json:"tab_count"`
	ActiveTabID string                  `json:"active_tab_id"`
	AgentStatus string                  `json:"agent_status"`
	Worktree    *herdrWorkspaceWorktree `json:"worktree,omitempty"`
}

// herdrWorkspaceWorktree is herdr's WorkspaceWorktreeInfo.
type herdrWorkspaceWorktree struct {
	RepoKey          string `json:"repo_key"`
	RepoName         string `json:"repo_name"`
	RepoRoot         string `json:"repo_root"`
	CheckoutPath     string `json:"checkout_path"`
	IsLinkedWorktree bool   `json:"is_linked_worktree"`
}

// herdrTab is herdr's TabInfo.
type herdrTab struct {
	TabID       string `json:"tab_id"`
	WorkspaceID string `json:"workspace_id"`
	Number      int    `json:"number"`
	Label       string `json:"label"`
	Focused     bool   `json:"focused"`
	PaneCount   int    `json:"pane_count"`
	AgentStatus string `json:"agent_status"`
}

// herdrPaneInfo is herdr's PaneInfo.
type herdrPaneInfo struct {
	PaneID        string             `json:"pane_id"`
	TerminalID    string             `json:"terminal_id"`
	WorkspaceID   string             `json:"workspace_id"`
	TabID         string             `json:"tab_id"`
	Focused       bool               `json:"focused"`
	Cwd           string             `json:"cwd,omitempty"`
	ForegroundCwd string             `json:"foreground_cwd,omitempty"`
	Label         string             `json:"label,omitempty"`
	Agent         string             `json:"agent,omitempty"`
	Title         string             `json:"title,omitempty"`
	TerminalTitle string             `json:"terminal_title,omitempty"`
	AgentStatus   string             `json:"agent_status"`
	Tokens        map[string]string  `json:"tokens,omitempty"`
	AgentSession  *herdrAgentSession `json:"agent_session,omitempty"`
	Scroll        *herdrScroll       `json:"scroll,omitempty"`
	Revision      uint64             `json:"revision"`
}

// herdrAgentSession is herdr's AgentSessionInfo.
type herdrAgentSession struct {
	Source string `json:"source"`
	Agent  string `json:"agent"`
	Kind   string `json:"kind"`
	Value  string `json:"value"`
}

// herdrScroll is herdr's PaneScrollInfo. tuios's panes are always read at
// the bottom through the socket, so offset_from_bottom is 0.
type herdrScroll struct {
	OffsetFromBottom    uint64 `json:"offset_from_bottom"`
	MaxOffsetFromBottom uint64 `json:"max_offset_from_bottom"`
	ViewportRows        uint64 `json:"viewport_rows"`
}

// herdrAgentInfo is herdr's AgentInfo, the snapshot's record of each pane
// that holds an agent.
type herdrAgentInfo struct {
	TerminalID    string             `json:"terminal_id"`
	Agent         string             `json:"agent,omitempty"`
	Title         string             `json:"title,omitempty"`
	TerminalTitle string             `json:"terminal_title,omitempty"`
	AgentStatus   string             `json:"agent_status"`
	Tokens        map[string]string  `json:"tokens,omitempty"`
	AgentSession  *herdrAgentSession `json:"agent_session,omitempty"`
	WorkspaceID   string             `json:"workspace_id"`
	TabID         string             `json:"tab_id"`
	PaneID        string             `json:"pane_id"`
	Focused       bool               `json:"focused"`
	StateChange   uint64             `json:"state_change_seq"`
	CompletionSeq uint64             `json:"completion_seq,omitempty"`
	Cwd           string             `json:"cwd,omitempty"`
	Revision      uint64             `json:"revision"`
}

// herdrRect is herdr's PaneLayoutRect.
type herdrRect struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

// herdrLayoutPane is herdr's PaneLayoutPane.
type herdrLayoutPane struct {
	PaneID  string    `json:"pane_id"`
	Focused bool      `json:"focused"`
	Rect    herdrRect `json:"rect"`
}

// herdrLayout is herdr's PaneLayoutSnapshot. tuios keeps a tiling tree or
// free-floating windows, not herdr's split tree, so splits is always empty
// and each pane carries its own rectangle.
type herdrLayout struct {
	WorkspaceID   string            `json:"workspace_id"`
	TabID         string            `json:"tab_id"`
	Zoomed        bool              `json:"zoomed"`
	Area          herdrRect         `json:"area"`
	FocusedPaneID string            `json:"focused_pane_id"`
	Panes         []herdrLayoutPane `json:"panes"`
	Splits        []struct{}        `json:"splits"`
}

// herdrSnapshot is herdr's SessionSnapshot.
type herdrSnapshot struct {
	Version            string           `json:"version"`
	Protocol           int              `json:"protocol"`
	FocusedWorkspaceID string           `json:"focused_workspace_id,omitempty"`
	FocusedTabID       string           `json:"focused_tab_id,omitempty"`
	FocusedPaneID      string           `json:"focused_pane_id,omitempty"`
	Workspaces         []herdrWorkspace `json:"workspaces"`
	Tabs               []herdrTab       `json:"tabs"`
	Panes              []herdrPaneInfo  `json:"panes"`
	Layouts            []herdrLayout    `json:"layouts"`
	Agents             []herdrAgentInfo `json:"agents"`
}

// herdrStatus is herdr's AgentStatus for a tuios agent state:
//
//	working       working
//	needs_input   blocked
//	idle          idle
//	done          done
//	errored       done: the agent stopped, and the person should look
//	unknown, none unknown
func herdrStatus(s AgentState) string {
	switch s {
	case AgentStateWorking:
		return "working"
	case AgentStateNeedsInput:
		return "blocked"
	case AgentStateIdle:
		return "idle"
	case AgentStateDone, AgentStateErrored:
		return "done"
	}
	return "unknown"
}

// herdrStatusRank orders statuses for a tab's or a workspace's summary the
// way herdr does (src/workspace/aggregate.rs): blocked, then a finished turn
// nobody has seen, then working, then idle, then unknown.
func herdrStatusRank(status string) int {
	switch status {
	case "blocked":
		return 4
	case "done":
		return 3
	case "working":
		return 2
	case "idle":
		return 1
	}
	return 0
}

// herdrAgentLabel is herdr's name for a tuios harness id. herdr names most
// harnesses the way tuios does. The few it names otherwise are mapped, so a
// client that keys prompt rules on herdr's names (Collie keys on claude)
// finds them.
func herdrAgentLabel(harness string) string {
	switch harness {
	case "claude-code":
		return "claude"
	case "gemini-cli":
		return "gemini"
	case "cursor-agent":
		return "cursor"
	case "antigravity":
		return "agy"
	case "qoder":
		return "qodercli"
	}
	return harness
}

// herdrView is one read of the daemon for the herdr socket: every session
// the caller may read, in herdr's shapes. It is built once per request from
// one state snapshot per session, so a session.snapshot costs one GetState
// per session and one cheap PTY read per pane.
type herdrView struct {
	workspaces []herdrWorkspace
	tabs       []herdrTab
	panes      []herdrPaneInfo
	layouts    []herdrLayout
	agents     []herdrAgentInfo

	focusedWorkspace, focusedTab, focusedPane string
}

// herdrOrderedSessions is every session in the order tuios lists them:
// oldest first, then by name. It reads no session state.
func (d *Daemon) herdrOrderedSessions() []*Session {
	sessions := d.manager.AllSessions()
	slices.SortFunc(sessions, func(a, b *Session) int {
		return cmp.Or(a.Created.Compare(b.Created), strings.Compare(a.Name(), b.Name()))
	})
	return sessions
}

// herdrSessions is every session, in the order tuios lists them, that the
// caller on cs may read. A caller outside every pane, and a pane holding
// admin, reads them all. Any other pane reads what list-windows would let it
// read: the grants check runs as for that verb, once per session.
func (d *Daemon) herdrSessions(cs *connState) []*Session {
	var out []*Session
	checkAll := true
	for _, sess := range d.herdrOrderedSessions() {
		if checkAll {
			if _, _, verr := d.admitVerb(cs, "list-windows", herdrSessionParams(sess)); verr != nil {
				continue
			}
			// A caller held to nothing new, or holding admin, reads every
			// session: the rest need no check of their own.
			if pa := cs.paneView.Load(); pa == nil || pa.grants.Has(GrantAdmin) {
				checkAll = false
			}
		}
		out = append(out, sess)
	}
	return out
}

// herdrSessionParams names a session for a verb call.
func herdrSessionParams(sess *Session) json.RawMessage {
	raw, _ := json.Marshal(map[string]string{"session": sess.Name()})
	return raw
}

// herdrFocusedSession is the session herdr would call the active workspace:
// the one an attached client showed last, else the one used last.
func (d *Daemon) herdrFocusedSession(sessions []*Session) *Session {
	var best, bestAttached *Session
	for _, s := range sessions {
		if best == nil || s.LastActive().After(best.LastActive()) {
			best = s
		}
		if d.findTUIClient(s.ID) != nil && (bestAttached == nil || s.LastActive().After(bestAttached.LastActive())) {
			bestAttached = s
		}
	}
	if bestAttached != nil {
		return bestAttached
	}
	return best
}

// herdrListedWorkspace reports whether workspace ws of a session is one of
// its herdr tabs. herdr lists only the tabs that exist, and tuios's
// workspaces are fixed slots, so a slot is a tab when it holds a window, has
// a name, or is the one showing.
func herdrListedWorkspace(st *SessionState, ws int) bool {
	return ws == st.CurrentWorkspace || st.WorkspaceNames[ws] != "" || herdrCount(st, ws) > 0
}

// herdrScratch reports whether a window is a pane of a scratch group: marked
// scratch, or on a scratch workspace. herdr has nothing like one, so it is
// not a pane to a herdr client: not in the snapshot, the lists or the
// events, and not found by its id.
func herdrScratch(w *WindowState) bool {
	return w.Scratch || IsScratchWorkspace(w.Workspace)
}

// herdrCount is how many windows other than scratch terminals workspace ws
// holds.
func herdrCount(st *SessionState, ws int) int {
	n := 0
	for i := range st.Windows {
		if st.Windows[i].Workspace == ws && !herdrScratch(&st.Windows[i]) {
			n++
		}
	}
	return n
}

// herdrWorkspaceOrder is a session's workspaces in display order.
func herdrWorkspaceOrder(st *SessionState) []int {
	bound := st.workspaceBound()
	seen := make(map[int]bool, bound)
	order := make([]int, 0, bound)
	for _, ws := range st.WorkspaceOrder {
		if ws >= 1 && ws <= bound && !seen[ws] {
			seen[ws] = true
			order = append(order, ws)
		}
	}
	for ws := 1; ws <= bound; ws++ {
		if !seen[ws] {
			order = append(order, ws)
		}
	}
	return order
}

// buildHerdrView reads the sessions into herdr's records.
func (d *Daemon) buildHerdrView(cs *connState) *herdrView {
	sessions := d.herdrSessions(cs)
	v := &herdrView{
		workspaces: []herdrWorkspace{}, tabs: []herdrTab{}, panes: []herdrPaneInfo{},
		layouts: []herdrLayout{}, agents: []herdrAgentInfo{},
	}
	focused := d.herdrFocusedSession(sessions)
	for i, sess := range sessions {
		d.addHerdrSession(v, sess, sess.GetState(), i+1, sess == focused)
	}
	return v
}

// addHerdrSession adds one session, as a herdr workspace numbered number, and
// its tabs, panes and agents.
func (d *Daemon) addHerdrSession(v *herdrView, sess *Session, st *SessionState, number int, active bool) {
	wsID := herdrWorkspaceID(sess.ID)
	label := st.DisplayName
	if label == "" {
		label = sess.Name()
	}
	tabs := herdrOrderedTabs(st)
	activeTab := herdrTabID(sess.ID, max(st.CurrentWorkspace, 1))
	if IsScratchWorkspace(st.CurrentWorkspace) && len(tabs) > 0 {
		// A scratch workspace is showing, and it is not a tab. The tab the
		// person comes back to is the first one.
		activeTab = herdrTabID(sess.ID, tabs[0])
	}
	w := herdrWorkspace{
		WorkspaceID: wsID, Number: number, Label: label, Focused: active,
		ActiveTabID: activeTab, AgentStatus: "unknown",
	}
	if wt := st.Worktree; wt != nil && wt.RepoRoot != "" {
		w.Worktree = &herdrWorkspaceWorktree{
			RepoKey: wt.RepoRoot, RepoName: wt.Repo, RepoRoot: wt.RepoRoot,
			CheckoutPath: wt.Path, IsLinkedWorktree: true,
		}
	}

	panesByTab := make(map[int][]herdrPaneInfo)
	first := len(v.panes)
	for i := range st.Windows {
		win := &st.Windows[i]
		if herdrScratch(win) {
			continue
		}
		w.PaneCount++
		p := d.herdrPaneRecord(sess, st, win, active)
		panesByTab[win.Workspace] = append(panesByTab[win.Workspace], p)
		v.panes = append(v.panes, p)
		if p.Focused {
			v.focusedPane = p.PaneID
		}
		if p.Agent != "" {
			v.agents = append(v.agents, herdrAgentFromPane(p, win))
		}
		if herdrStatusRank(p.AgentStatus) > herdrStatusRank(w.AgentStatus) {
			w.AgentStatus = p.AgentStatus
		}
	}

	if w.Worktree == nil {
		// herdr says which repository a workspace sits in whenever it sits
		// in one, its main checkout included. The session's directory is
		// its focused pane's, else its first pane's.
		dir := ""
		for _, p := range v.panes[first:] {
			if dir == "" || p.Focused {
				dir = p.Cwd
			}
		}
		w.Worktree = herdrCheckoutOf(dir)
	}

	for n, ws := range tabs {
		t := herdrTabRecord(sess, st, ws, n+1, active, panesByTab[ws])
		v.tabs = append(v.tabs, t)
		v.layouts = append(v.layouts, herdrLayoutOf(sess, st, ws, panesByTab[ws]))
		w.TabCount++
	}
	if active {
		v.focusedWorkspace, v.focusedTab = wsID, activeTab
	}
	v.workspaces = append(v.workspaces, w)
}

// herdrOrderedTabs is a session's listed workspaces in display order.
func herdrOrderedTabs(st *SessionState) []int {
	var out []int
	for _, ws := range herdrWorkspaceOrder(st) {
		if herdrListedWorkspace(st, ws) {
			out = append(out, ws)
		}
	}
	return out
}

// herdrTabRecord is herdr's TabInfo for workspace ws, the n-th listed tab.
// herdr's number is the tab's own and stays through a move, as a tuios
// workspace number does, so it is the workspace number. An unnamed
// workspace's label is its number, which is what herdr shows for an
// unnamed tab.
func herdrTabRecord(sess *Session, st *SessionState, ws, _ int, active bool, panes []herdrPaneInfo) herdrTab {
	label := st.WorkspaceNames[ws]
	if label == "" {
		label = strconv.Itoa(ws)
	}
	t := herdrTab{
		TabID: herdrTabID(sess.ID, ws), WorkspaceID: herdrWorkspaceID(sess.ID), Number: ws,
		Label: label, Focused: active && ws == st.CurrentWorkspace, PaneCount: len(panes),
		AgentStatus: "unknown",
	}
	for _, p := range panes {
		if herdrStatusRank(p.AgentStatus) > herdrStatusRank(t.AgentStatus) {
			t.AgentStatus = p.AgentStatus
		}
	}
	return t
}

// herdrPaneRecord is herdr's PaneInfo for one window.
func (d *Daemon) herdrPaneRecord(sess *Session, st *SessionState, win *WindowState, active bool) herdrPaneInfo {
	p := herdrPaneInfo{
		PaneID:        herdrPaneID(sess.ID, win.ID),
		TerminalID:    "term_" + herdrHex(win.PTYID),
		WorkspaceID:   herdrWorkspaceID(sess.ID),
		TabID:         herdrTabID(sess.ID, max(win.Workspace, 1)),
		Focused:       active && win.ID == st.FocusedWindowID && win.Workspace == st.CurrentWorkspace,
		Cwd:           win.Cwd,
		ForegroundCwd: win.Cwd,
		Label:         win.CustomName,
		TerminalTitle: win.Title,
		AgentStatus:   "unknown",
	}
	if win.AgentHarness != "" && win.AgentState != AgentStateNone {
		p.Agent = herdrAgentLabel(win.AgentHarness)
		p.AgentStatus = herdrStatus(win.AgentState)
	}
	if len(win.AgentMeta) > 0 {
		p.Tokens = make(map[string]string, len(win.AgentMeta))
		for _, tok := range win.AgentMeta {
			if tok.Key == "title" {
				p.Title = tok.Value
				continue
			}
			p.Tokens[tok.Key] = tok.Value
		}
		if len(p.Tokens) == 0 {
			p.Tokens = nil
		}
	}
	if win.AgentSessionID != "" {
		agent := herdrAgentLabel(win.AgentSessionHarness)
		kind := "id"
		if strings.HasPrefix(win.AgentSessionID, "/") {
			kind = "path"
		}
		p.AgentSession = &herdrAgentSession{Source: "tuios:" + win.AgentSessionHarness, Agent: agent, Kind: kind, Value: win.AgentSessionID}
	}
	if pty := sess.GetPTY(win.PTYID); pty != nil {
		if p.Cwd == "" {
			// The session reads directories on a timer, and a pane made a
			// moment ago is not read yet. herdr always says where a pane is.
			if cwd, ok := pty.ProcessCwd(); ok {
				p.Cwd, p.ForegroundCwd = cwd, cwd
			}
		}
		rev, back, rows := pty.herdrFacts()
		p.Revision = rev
		p.Scroll = &herdrScroll{MaxOffsetFromBottom: uint64(back), ViewportRows: uint64(rows)}
	}
	return p
}

// herdrAgentFromPane is herdr's AgentInfo for a pane that holds an agent.
func herdrAgentFromPane(p herdrPaneInfo, win *WindowState) herdrAgentInfo {
	return herdrAgentInfo{
		TerminalID: p.TerminalID, Agent: p.Agent, Title: p.Title, TerminalTitle: p.TerminalTitle,
		AgentStatus: p.AgentStatus, Tokens: p.Tokens, AgentSession: p.AgentSession,
		WorkspaceID: p.WorkspaceID, TabID: p.TabID, PaneID: p.PaneID, Focused: p.Focused,
		StateChange: uint64(max(win.AgentStateAt, 0) / int64(time.Millisecond)), CompletionSeq: win.CompletionSeq,
		Cwd: p.Cwd, Revision: p.Revision,
	}
}

// herdrLayoutOf is herdr's PaneLayoutSnapshot for one workspace: each
// visible pane's rectangle in the session's area.
func herdrLayoutOf(sess *Session, st *SessionState, ws int, panes []herdrPaneInfo) herdrLayout {
	l := herdrLayout{
		WorkspaceID: herdrWorkspaceID(sess.ID), TabID: herdrTabID(sess.ID, ws),
		Area:  herdrRect{Width: st.Width, Height: st.Height},
		Panes: []herdrLayoutPane{}, Splits: []struct{}{},
	}
	byID := make(map[string]*WindowState, len(st.Windows))
	for i := range st.Windows {
		byID[herdrPaneID(sess.ID, st.Windows[i].ID)] = &st.Windows[i]
	}
	focus := st.WorkspaceFocus[ws]
	if ws == st.CurrentWorkspace && st.FocusedWindowID != "" {
		focus = st.FocusedWindowID
	}
	for _, p := range panes {
		win := byID[p.PaneID]
		if win == nil || win.Minimized {
			continue
		}
		if win.Zoomed {
			l.Zoomed = true
		}
		if win.ID == focus {
			l.FocusedPaneID = p.PaneID
		}
		l.Panes = append(l.Panes, herdrLayoutPane{
			PaneID: p.PaneID, Focused: p.PaneID == l.FocusedPaneID,
			Rect: herdrRect{X: win.X, Y: win.Y, Width: win.Width, Height: win.Height},
		})
	}
	sort.SliceStable(l.Panes, func(i, j int) bool {
		a, b := l.Panes[i].Rect, l.Panes[j].Rect
		return a.Y < b.Y || (a.Y == b.Y && a.X < b.X)
	})
	return l
}

// herdrFacts is what a pane record says about the terminal: the bytes the
// pane has produced, as herdr's revision, which changes whenever the pane
// prints; the scrollback rows above the screen; and the screen's rows. Each
// is one read under its own lock, so a snapshot stays cheap.
func (p *PTY) herdrFacts() (revision uint64, scrollback, rows int) {
	p.outputMu.RLock()
	revision = uint64(max(p.outputSeq, 0))
	p.outputMu.RUnlock()
	p.terminalMu.RLock()
	if p.terminal != nil {
		scrollback = p.terminal.ScrollbackLen()
	}
	rows = p.height
	p.terminalMu.RUnlock()
	return revision, scrollback, rows
}

// herdrCheckoutOf is herdr's record of the git checkout dir is in, nil for
// none. It only reads files, as the rail's git line does.
func herdrCheckoutOf(dir string) *herdrWorkspaceWorktree {
	if dir == "" {
		return nil
	}
	gitdir, common, root, ok := gitstate.Locate(dir)
	if !ok {
		return nil
	}
	repoRoot := common
	if filepath.Base(common) == ".git" {
		repoRoot = filepath.Dir(common)
	}
	return &herdrWorkspaceWorktree{
		RepoKey: repoRoot, RepoName: filepath.Base(repoRoot), RepoRoot: repoRoot,
		CheckoutPath: root, IsLinkedWorktree: gitdir != common,
	}
}
