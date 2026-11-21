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
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
)

type herdrWorkspace struct {
	WorkspaceID string                  `json:"workspace_id"`
	Number      int                     `json:"number"`
	Label       string                  `json:"label"`
	Focused     bool                    `json:"focused"`
	PaneCount   int                     `json:"pane_count"`
	TabCount    int                     `json:"tab_count"`
	ActiveTabID string                  `json:"active_tab_id"`
	AgentStatus string                  `json:"agent_status"`
	Tokens      map[string]string       `json:"tokens,omitempty"`
	Worktree    *herdrWorkspaceWorktree `json:"worktree,omitempty"`
}

type herdrWorkspaceWorktree struct {
	RepoKey          string `json:"repo_key"`
	RepoName         string `json:"repo_name"`
	RepoRoot         string `json:"repo_root"`
	CheckoutPath     string `json:"checkout_path"`
	IsLinkedWorktree bool   `json:"is_linked_worktree"`
}

type herdrTab struct {
	TabID       string `json:"tab_id"`
	WorkspaceID string `json:"workspace_id"`
	Number      int    `json:"number"`
	Label       string `json:"label"`
	Focused     bool   `json:"focused"`
	PaneCount   int    `json:"pane_count"`
	AgentStatus string `json:"agent_status"`
}

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

type herdrAgentSession struct {
	Source string `json:"source"`
	Agent  string `json:"agent"`
	Kind   string `json:"kind"`
	Value  string `json:"value"`
}

type herdrScroll struct {
	OffsetFromBottom    uint64 `json:"offset_from_bottom"`
	MaxOffsetFromBottom uint64 `json:"max_offset_from_bottom"`
	ViewportRows        uint64 `json:"viewport_rows"`
}

type herdrAgentInfo struct {
	TerminalID    string             `json:"terminal_id"`
	Name          string             `json:"name,omitempty"`
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

type herdrRect struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

type herdrLayoutPane struct {
	PaneID  string    `json:"pane_id"`
	Focused bool      `json:"focused"`
	Rect    herdrRect `json:"rect"`
}

type herdrLayout struct {
	WorkspaceID   string            `json:"workspace_id"`
	TabID         string            `json:"tab_id"`
	Zoomed        bool              `json:"zoomed"`
	Area          herdrRect         `json:"area"`
	FocusedPaneID string            `json:"focused_pane_id"`
	Panes         []herdrLayoutPane `json:"panes"`
	Splits        []struct{}        `json:"splits"`
}

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

func herdrStatus(s AgentState, unread bool) string {
	switch s {
	case AgentStateWorking:
		return "working"
	case AgentStateNeedsInput:
		return "blocked"
	case AgentStateIdle, AgentStateDone:
		if unread {
			return "done"
		}
		return "idle"
	case AgentStateErrored, AgentStateLimitReached:
		return "done"
	}
	return "unknown"
}

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

type herdrView struct {
	workspaces []herdrWorkspace
	tabs       []herdrTab
	panes      []herdrPaneInfo
	layouts    []herdrLayout
	agents     []herdrAgentInfo

	focusedWorkspace, focusedTab, focusedPane string
}

func (d *Daemon) herdrOrderedSessions() []*Session {
	sessions := d.manager.AllSessions()
	slices.SortFunc(sessions, func(a, b *Session) int {
		return cmp.Or(a.Created.Compare(b.Created), strings.Compare(a.Name(), b.Name()))
	})
	return sessions
}

func (d *Daemon) herdrSessions(cs *connState) []*Session {
	var out []*Session
	checkAll := true
	for _, sess := range d.herdrOrderedSessions() {
		if checkAll {
			if _, _, verr := d.admitVerb(cs, "list-windows", herdrSessionParams(sess)); verr != nil {
				continue
			}
			if pa := cs.paneView.Load(); pa == nil || pa.grants.Has(GrantAdmin) {
				checkAll = false
			}
		}
		out = append(out, sess)
	}
	return out
}

func herdrSessionParams(sess *Session) json.RawMessage {
	raw, _ := jsonx.Marshal(map[string]string{"session": sess.Name()})
	return raw
}

func (d *Daemon) herdrFocusedSession(sessions []*Session) *Session {
	var attached []*Session
	for _, s := range sessions {
		if d.findTUIClient(s.ID) != nil {
			attached = append(attached, s)
		}
	}
	if best := mostRecentSession(attached, false); best != nil {
		return best
	}
	return mostRecentSession(sessions, false)
}

func herdrListedWorkspace(st *SessionState, ws int) bool {
	return ws == st.CurrentWorkspace || st.WorkspaceNames[ws] != "" || herdrCount(st, ws) > 0
}

func herdrScratch(w *WindowState) bool {
	return w.Scratch || IsScratchWorkspace(w.Workspace)
}

func herdrCount(st *SessionState, ws int) int {
	n := 0
	for i := range st.Windows {
		if st.Windows[i].Workspace == ws && !herdrScratch(&st.Windows[i]) {
			n++
		}
	}
	return n
}

func herdrWorkspaceOrder(st *SessionState) []int {
	order := make([]int, 0, st.workspaceBound())
	for ws := 1; ws <= st.workspaceBound(); ws++ {
		order = append(order, ws)
	}
	return order
}

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

func herdrWorkspaceLabel(sess *Session, st *SessionState) string {
	return cmp.Or(st.DisplayName, sess.Name())
}

func (d *Daemon) addHerdrSession(v *herdrView, sess *Session, st *SessionState, number int, active bool) {
	wsID := herdrWorkspaceID(sess.ID)
	label := herdrWorkspaceLabel(sess, st)
	tabs := herdrOrderedTabs(st)
	activeTab := herdrTabID(sess.ID, st.WorkspaceTabKey(max(st.CurrentWorkspace, 1)))
	if IsScratchWorkspace(st.CurrentWorkspace) && len(tabs) > 0 {
		activeTab = herdrTabID(sess.ID, st.WorkspaceTabKey(tabs[0]))
	}
	w := herdrWorkspace{
		WorkspaceID: wsID, Number: number, Label: label, Focused: active,
		ActiveTabID: activeTab, AgentStatus: "unknown",
		Tokens: d.herdrWorkspaceMeta.tokens(sess.ID, time.Now()),
	}
	if wt := st.Worktree; wt != nil && wt.RepoRoot != "" {
		w.Worktree = &herdrWorkspaceWorktree{
			RepoKey: wt.RepoRoot, RepoName: wt.Repo, RepoRoot: wt.RepoRoot,
			CheckoutPath: wt.Path, IsLinkedWorktree: !wt.Main,
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

func herdrOrderedTabs(st *SessionState) []int {
	var out []int
	for _, ws := range herdrWorkspaceOrder(st) {
		if herdrListedWorkspace(st, ws) {
			out = append(out, ws)
		}
	}
	return out
}

func herdrTabLabel(name string, ws int) string {
	if name == "" {
		return strconv.Itoa(ws)
	}
	return name
}

func herdrTabRecord(sess *Session, st *SessionState, ws, _ int, active bool, panes []herdrPaneInfo) herdrTab {
	label := herdrTabLabel(st.WorkspaceNames[ws], ws)
	t := herdrTab{
		TabID: herdrTabID(sess.ID, st.WorkspaceTabKey(ws)), WorkspaceID: herdrWorkspaceID(sess.ID), Number: st.WorkspaceTabKey(ws),
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

func (d *Daemon) herdrPaneRecord(sess *Session, st *SessionState, win *WindowState, active bool) herdrPaneInfo {
	p := herdrPaneInfo{
		PaneID:        herdrPaneID(sess.ID, win.ID),
		TerminalID:    "term_" + herdrHex(win.PTYID),
		WorkspaceID:   herdrWorkspaceID(sess.ID),
		TabID:         herdrTabID(sess.ID, st.WorkspaceTabKey(max(win.Workspace, 1))),
		Focused:       active && win.ID == st.FocusedWindowID && win.Workspace == st.CurrentWorkspace,
		Cwd:           win.Cwd,
		ForegroundCwd: win.Cwd,
		Label:         win.CustomName,
		TerminalTitle: win.Title,
		AgentStatus:   "unknown",
	}
	if win.AgentHarness != "" && win.AgentState != AgentStateNone {
		p.Agent = herdrAgentLabel(win.AgentHarness)
		p.AgentStatus = herdrStatus(win.AgentState, sess.finishedUnread(win))
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

func herdrAgentFromPane(p herdrPaneInfo, win *WindowState) herdrAgentInfo {
	return herdrAgentInfo{
		TerminalID: p.TerminalID, Name: p.Label, Agent: p.Agent, Title: p.Title, TerminalTitle: p.TerminalTitle,
		AgentStatus: p.AgentStatus, Tokens: p.Tokens, AgentSession: p.AgentSession,
		WorkspaceID: p.WorkspaceID, TabID: p.TabID, PaneID: p.PaneID, Focused: p.Focused,
		StateChange: uint64(max(win.AgentStateAt, 0) / int64(time.Millisecond)), CompletionSeq: win.CompletionSeq,
		Cwd: p.Cwd, Revision: p.Revision,
	}
}

func herdrLayoutOf(sess *Session, st *SessionState, ws int, panes []herdrPaneInfo) herdrLayout {
	l := herdrLayout{
		WorkspaceID: herdrWorkspaceID(sess.ID), TabID: herdrTabID(sess.ID, st.WorkspaceTabKey(ws)),
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
