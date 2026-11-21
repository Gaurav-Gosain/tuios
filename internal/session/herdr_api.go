package session

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/worktree"
)

const (
	herdrTargetVersion  = "0.9.5"
	herdrTargetProtocol = 22
)

const (
	herdrReadDefaultLines = 80
	herdrReadMaxLines     = 1000
)

type herdrError struct{ code, msg string }

//go:noinline
func herdrErr(code, msg string) *herdrError { return &herdrError{code: code, msg: msg} }

func (e *herdrIDError) herdr() *herdrError { return herdrErr(e.code, e.msg) }

type herdrIn struct {
	WorkspaceID  string   `json:"workspace_id"`
	TabID        string   `json:"tab_id"`
	PaneID       string   `json:"pane_id"`
	TargetPaneID string   `json:"target_pane_id"`
	CallerPaneID string   `json:"caller_pane_id"`
	Target       string   `json:"target"`
	Label        *string  `json:"label"`
	Cwd          string   `json:"cwd"`
	Focus        bool     `json:"focus"`
	Text         string   `json:"text"`
	Keys         []string `json:"keys"`
	Source       string   `json:"source"`
	Format       string   `json:"format"`
	Lines        *int     `json:"lines"`
	Direction    string   `json:"direction"`
	InsertIndex  *int     `json:"insert_index"`
	Branch       string   `json:"branch"`
	Base         string   `json:"base"`
	Path         string   `json:"path"`
	Force        bool     `json:"force"`
	Until        []string `json:"until"`
	TimeoutMS    *int64   `json:"timeout_ms"`
	Match        *struct {
		Type  string `json:"type"`
		Value string `json:"value"`
	} `json:"match"`
	Wait *struct {
		Until     []string `json:"until"`
		TimeoutMS *int64   `json:"timeout_ms"`
	} `json:"wait"`
	MatchEvent    map[string]any      `json:"match_event"`
	Subscriptions []herdrSubscription `json:"subscriptions"`
	Env           map[string]string   `json:"env"`
	Mode          string              `json:"mode"`
	SourcePaneID  string              `json:"source_pane_id"`
	Name          string              `json:"name"`
	Kind          string              `json:"kind"`
	Args          []string            `json:"args"`
	Amount        *float64            `json:"amount"`
	Destination   herdrMoveDest       `json:"destination"`
	Title         string              `json:"title"`
	Tokens        map[string]*string  `json:"tokens"`
	Seq           *uint64             `json:"seq"`
	TTLMS         *int64              `json:"ttl_ms"`
}

func (in *herdrIn) label() string {
	if in.Label == nil {
		return ""
	}
	return *in.Label
}

type herdrResult struct {
	Type          string                 `json:"type"`
	Version       string                 `json:"version,omitempty"`
	Protocol      int                    `json:"protocol,omitempty"`
	Server        string                 `json:"server,omitempty"`
	ServerVersion string                 `json:"server_version,omitempty"`
	Snapshot      *herdrSnapshot         `json:"snapshot,omitempty"`
	Workspace     *herdrWorkspace        `json:"workspace,omitempty"`
	Workspaces    *[]herdrWorkspace      `json:"workspaces,omitempty"`
	Tab           *herdrTab              `json:"tab,omitempty"`
	Tabs          *[]herdrTab            `json:"tabs,omitempty"`
	Pane          *herdrPaneInfo         `json:"pane,omitempty"`
	RootPane      *herdrPaneInfo         `json:"root_pane,omitempty"`
	Panes         *[]herdrPaneInfo       `json:"panes,omitempty"`
	Agent         *herdrAgentInfo        `json:"agent,omitempty"`
	Agents        *[]herdrAgentInfo      `json:"agents,omitempty"`
	Layout        *herdrLayout           `json:"layout,omitempty"`
	Read          *herdrReadRecord       `json:"read,omitempty"`
	Source        *herdrWorktreeSource   `json:"source,omitempty"`
	Worktree      *herdrWorktreeRecord   `json:"worktree,omitempty"`
	Worktrees     *[]herdrWorktreeRecord `json:"worktrees,omitempty"`
	AlreadyOpen   *bool                  `json:"already_open,omitempty"`
	WorkspaceID   string                 `json:"workspace_id,omitempty"`
	PaneID        string                 `json:"pane_id,omitempty"`
	Path          string                 `json:"path,omitempty"`
	Forced        *bool                  `json:"forced,omitempty"`
	Revision      *uint64                `json:"revision,omitempty"`
	MatchedLine   *string                `json:"matched_line,omitempty"`
	Event         *herdrEvent            `json:"event,omitempty"`
	ProcessInfo   *herdrProcessInfo      `json:"process_info,omitempty"`
	Neighbor      *herdrNeighbor         `json:"neighbor,omitempty"`
	Edges         *herdrEdges            `json:"edges,omitempty"`
	Focus         *herdrFocusMove        `json:"focus,omitempty"`
	Swap          *herdrSwap             `json:"swap,omitempty"`
	Zoom          *herdrZoom             `json:"zoom,omitempty"`
	Argv          []string               `json:"argv,omitempty"`
	Resize        *herdrResize           `json:"resize,omitempty"`
	Status        string                 `json:"status,omitempty"`
	Diagnostics   *[]string              `json:"diagnostics,omitempty"`
	Explain       map[string]any         `json:"explain,omitempty"`
	MoveResult    *herdrMove             `json:"move_result,omitempty"`
	Changed       *bool                  `json:"changed,omitempty"`
	Reason        string                 `json:"reason,omitempty"`
	Manifests     *[]herdrManifestInfo   `json:"manifests,omitempty"`
}

var herdrAck = &herdrResult{Type: "ok"}

type herdrReadRecord struct {
	PaneID      string `json:"pane_id"`
	WorkspaceID string `json:"workspace_id"`
	TabID       string `json:"tab_id"`
	Source      string `json:"source"`
	Format      string `json:"format"`
	Text        string `json:"text"`
	Revision    uint64 `json:"revision"`
	Truncated   bool   `json:"truncated"`
}

type herdrWorktreeSource struct {
	RepoKey           string `json:"repo_key"`
	RepoName          string `json:"repo_name"`
	RepoRoot          string `json:"repo_root"`
	SourceCheckout    string `json:"source_checkout_path"`
	SourceWorkspaceID string `json:"source_workspace_id,omitempty"`
}

type herdrArgs struct {
	Session      string            `json:"session,omitempty"`
	Window       string            `json:"window,omitempty"`
	Name         *string           `json:"name,omitempty"`
	Workspace    int               `json:"workspace,omitempty"`
	ToSession    string            `json:"to_session,omitempty"`
	Focus        *bool             `json:"focus,omitempty"`
	Cwd          string            `json:"cwd,omitempty"`
	Text         string            `json:"text,omitempty"`
	Keys         string            `json:"keys,omitempty"`
	Literal      bool              `json:"literal,omitempty"`
	Paste        bool              `json:"paste,omitempty"`
	Submit       bool              `json:"submit,omitempty"`
	AllowBlocked bool              `json:"allow_blocked,omitempty"`
	Source       string            `json:"source,omitempty"`
	Styled       bool              `json:"styled,omitempty"`
	Lines        int               `json:"lines,omitempty"`
	Direction    string            `json:"direction,omitempty"`
	Side         string            `json:"side,omitempty"`
	Beside       string            `json:"beside,omitempty"`
	Order        []int             `json:"order,omitempty"`
	Condition    string            `json:"condition,omitempty"`
	Pattern      string            `json:"pattern,omitempty"`
	Until        string            `json:"until,omitempty"`
	Timeout      *int64            `json:"timeout,omitempty"`
	Repo         string            `json:"repo,omitempty"`
	Branch       string            `json:"branch,omitempty"`
	Base         string            `json:"base,omitempty"`
	Force        bool              `json:"force,omitempty"`
	Command      string            `json:"command,omitempty"`
	Env          map[string]string `json:"env,omitempty"`
}

type herdrMethod struct {
	run      func(d *Daemon, cs *connState, in *herdrIn) (*herdrResult, *herdrError)
	required []string
}

func herdrMethods() map[string]herdrMethod {
	herdrMethodsOnce.Do(func() { herdrMethodsMap = buildHerdrMethods() })
	return herdrMethodsMap
}

var (
	herdrMethodsOnce sync.Once
	herdrMethodsMap  map[string]herdrMethod
)

func buildHerdrMethods() map[string]herdrMethod {
	pane := []string{"pane_id"}
	tab := []string{"tab_id"}
	ws := []string{"workspace_id"}
	target := []string{"target"}
	return map[string]herdrMethod{
		"ping":             {run: (*Daemon).herdrPing},
		"session.snapshot": {run: (*Daemon).herdrSnapshot},

		"workspace.list":   {run: (*Daemon).herdrWorkspaceList},
		"workspace.get":    {run: (*Daemon).herdrWorkspaceGet, required: ws},
		"workspace.create": {run: (*Daemon).herdrWorkspaceCreate},
		"workspace.rename": {run: (*Daemon).herdrWorkspaceRename, required: []string{"workspace_id", "label"}},
		"workspace.close":  {run: (*Daemon).herdrWorkspaceClose, required: ws},

		"tab.list":   {run: (*Daemon).herdrTabList},
		"tab.get":    {run: (*Daemon).herdrTabGet, required: tab},
		"tab.create": {run: (*Daemon).herdrTabCreate},
		"tab.rename": {run: (*Daemon).herdrTabRename, required: []string{"tab_id", "label"}},
		"tab.focus":  {run: (*Daemon).herdrTabFocus, required: tab},
		"tab.move":   {run: (*Daemon).herdrTabMove, required: []string{"tab_id", "insert_index"}},
		"tab.close":  {run: (*Daemon).herdrTabClose, required: tab},

		"pane.list":            {run: (*Daemon).herdrPaneList},
		"pane.get":             {run: (*Daemon).herdrPaneGet, required: pane},
		"pane.current":         {run: (*Daemon).herdrPaneCurrent},
		"pane.layout":          {run: (*Daemon).herdrPaneLayout},
		"pane.read":            {run: (*Daemon).herdrPaneRead, required: []string{"pane_id", "source"}},
		"pane.send_text":       {run: (*Daemon).herdrPaneSend, required: []string{"pane_id", "text"}},
		"pane.send_keys":       {run: (*Daemon).herdrPaneSend, required: []string{"pane_id", "keys"}},
		"pane.send_input":      {run: (*Daemon).herdrPaneSend, required: pane},
		"pane.rename":          {run: (*Daemon).herdrPaneRename, required: pane},
		"pane.focus":           {run: (*Daemon).herdrPaneFocus, required: pane},
		"pane.split":           {run: (*Daemon).herdrPaneSplit, required: []string{"direction"}},
		"pane.close":           {run: (*Daemon).herdrPaneClose, required: pane},
		"pane.wait_for_output": {run: (*Daemon).herdrPaneWaitForOutput, required: []string{"pane_id", "source", "match"}},

		"agent.list":      {run: (*Daemon).herdrAgentList},
		"agent.get":       {run: (*Daemon).herdrAgentGet, required: target},
		"agent.read":      {run: (*Daemon).herdrAgentRead, required: []string{"target", "source"}},
		"agent.send_keys": {run: (*Daemon).herdrAgentSendKeys, required: []string{"target", "keys"}},
		"agent.focus":     {run: (*Daemon).herdrAgentFocus, required: target},
		"agent.prompt":    {run: (*Daemon).herdrAgentPrompt, required: []string{"target", "text"}},
		"agent.wait":      {run: (*Daemon).herdrAgentWait, required: target},

		"worktree.list":   {run: (*Daemon).herdrWorktreeList},
		"worktree.create": {run: (*Daemon).herdrWorktreeCreate},
		"worktree.open":   {run: (*Daemon).herdrWorktreeOpen},
		"worktree.remove": {run: (*Daemon).herdrWorktreeRemove, required: ws},

		"events.wait": {run: (*Daemon).herdrEventsWait, required: []string{"match_event"}},

		"pane.process_info":         {run: (*Daemon).herdrPaneProcessInfo},
		"pane.neighbor":             {run: (*Daemon).herdrPaneNeighbor, required: []string{"direction"}},
		"pane.edges":                {run: (*Daemon).herdrPaneEdges},
		"pane.focus_direction":      {run: (*Daemon).herdrPaneFocusDirection, required: []string{"direction"}},
		"pane.swap":                 {run: (*Daemon).herdrPaneSwap},
		"pane.zoom":                 {run: (*Daemon).herdrPaneZoom},
		"workspace.focus":           {run: (*Daemon).herdrWorkspaceFocus, required: ws},
		"agent.start":               {run: (*Daemon).herdrAgentStart, required: []string{"name", "kind", "pane_id"}},
		"pane.resize":               {run: (*Daemon).herdrPaneResize, required: []string{"direction"}},
		"server.reload_config":      {run: (*Daemon).herdrReloadConfig},
		"server.agent_manifests":    {run: (*Daemon).herdrAgentManifests},
		"agent.rename":              {run: (*Daemon).herdrAgentRename, required: target},
		"pane.move":                 {run: (*Daemon).herdrPaneMove, required: []string{"pane_id", "destination"}},
		"workspace.report_metadata": {run: (*Daemon).herdrWorkspaceReportMetadata, required: []string{"workspace_id", "source", "tokens"}},
		"agent.explain":             {run: (*Daemon).herdrAgentExplain, required: target},

		"client.window_title.set":   {run: herdrClientTitle(false), required: []string{"title"}},
		"client.window_title.clear": {run: herdrClientTitle(true)},

		"release_notes.dismiss":        {run: (*Daemon).herdrDismissReleaseNotes, required: []string{"version"}},
		"product_announcement.dismiss": {run: (*Daemon).herdrDismissAnnouncement, required: []string{"version", "id"}},
	}
}

var herdrUnsupported = []string{
	"server.stop", "server.live_handoff", "server.ssh_agent.register",
	"server.reload_agent_manifests",
	"command.invoke",
	"client_shell.surface.set",
	"workspace.move", "workspace.move_block",
	"agent.view.set", "agent.view.clear",
	"layout.export", "layout.apply", "layout.set_split_ratio",
	"pane.scroll",
	"pane.clear", "pane.edit_scrollback", "pane.selection.read", "pane.copy_motion",
	"pane.copy_search", "pane.input.set", "pane.link.activate", "pane.link.resolve",
	"integration.list", "integration.install", "integration.uninstall",
}

func (d *Daemon) herdrAPICall(cs *connState, method string, params json.RawMessage) (any, *herdrError, bool) {
	m, ok := herdrMethods()[method]
	if !ok {
		if out, herr, handled := d.herdrPluginCall(cs, method, params); handled {
			return out, herr, true
		}
		if slices.Contains(herdrUnsupported, method) {
			return nil, herdrErr("unsupported", "tuios does not answer "+method+". See the herdr compatibility section of docs/AGENT_STATE.md for what it answers"), true
		}
		return nil, nil, false
	}
	if herr := d.herdrAllowed(cs, method); herr != nil {
		return nil, herr, true
	}
	in := &herdrIn{}
	if herr := herdrDecode(params, in, m.required...); herr != nil {
		return nil, herr, true
	}
	switch method {
	case "pane.send_text":
		in.Keys = nil
	case "pane.send_keys":
		in.Text = ""
	}
	out, herr := m.run(d, cs, in)
	if herr != nil {
		return nil, herr, true
	}
	return out, nil, true
}

func herdrDecode(params json.RawMessage, p any, required ...string) *herdrError {
	if len(params) == 0 || string(params) == "null" {
		params = json.RawMessage("{}")
	}
	var fields map[string]json.RawMessage
	if err := jsonx.Unmarshal(params, &fields); err != nil {
		return herdrErr("invalid_request", "invalid request: params must be an object")
	}
	for _, name := range required {
		if raw, ok := fields[name]; !ok || string(raw) == "null" {
			return herdrErr("invalid_request", "invalid request: missing field `"+name+"`")
		}
	}
	if err := jsonx.Unmarshal(params, p); err != nil {
		return herdrErr("invalid_request", "invalid request: "+err.Error())
	}
	return nil
}

func herdrFromVerb(verr *verbError, fail string) *herdrError {
	msg := verr.Message
	if verr.Hint != nil && verr.Hint.Detail != "" && verr.Code == ErrVerbForbidden {
		msg += " " + verr.Hint.Detail
	}
	if verr.Hint != nil && verr.Hint.Command != "" && verr.Code == ErrVerbNeedsClient {
		msg = strings.TrimSuffix(msg, ".") + ". Attach one with " + verr.Hint.Command + ", then try again"
	}
	code := fail
	switch verr.Code {
	case ErrVerbSessionNotFound:
		code = "workspace_not_found"
	case ErrVerbWindowNotFound, ErrVerbNoWindows, ErrVerbPTYNotFound:
		code = "pane_not_found"
	case ErrVerbForbidden, ErrVerbNotHuman:
		code = "forbidden"
	case ErrVerbInvalidParams:
		code = "invalid_params"
	case ErrVerbTimeout:
		code = "timeout"
	case ErrVerbAgentBlocked:
		code = "agent_blocked"
	case ErrVerbNotReady:
		code = "agent_not_idle"
	case ErrVerbNeedsClient:
		code = "no_client"
	}
	return herdrErr(code, msg)
}

func (d *Daemon) herdrVerb(cs *connState, verb string, args herdrArgs, fail string) (map[string]any, *herdrError) {
	out, verr := d.callVerb(cs, verb, args)
	if verr != nil {
		return nil, herdrFromVerb(verr, fail)
	}
	m, _ := out.(map[string]any)
	return m, nil
}

func herdrString(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

func ptr[T any](v T) *T { return new(v) }

func herdrVersion() string { return herdrTargetVersion + "+tuios" }

func (d *Daemon) herdrPing(_ *connState, _ *herdrIn) (*herdrResult, *herdrError) {
	return &herdrResult{Type: "pong", Version: herdrVersion(), Protocol: herdrTargetProtocol, Server: "tuios", ServerVersion: d.version}, nil
}

func (d *Daemon) herdrSnapshot(cs *connState, _ *herdrIn) (*herdrResult, *herdrError) {
	v := d.buildHerdrView(cs)
	return &herdrResult{Type: "session_snapshot", Snapshot: &herdrSnapshot{
		Version: herdrVersion(), Protocol: herdrTargetProtocol,
		FocusedWorkspaceID: v.focusedWorkspace, FocusedTabID: v.focusedTab, FocusedPaneID: v.focusedPane,
		Workspaces: v.workspaces, Tabs: v.tabs, Panes: v.panes, Layouts: v.layouts, Agents: v.agents,
	}}, nil
}

func (d *Daemon) herdrSessionView(cs *connState, sess *Session) (*herdrView, *herdrError) {
	if _, _, verr := d.admitVerb(cs, "list-windows", herdrSessionParams(sess)); verr != nil {
		return nil, herdrFromVerb(verr, "forbidden")
	}
	sessions := d.herdrOrderedSessions()
	v := &herdrView{}
	d.addHerdrSession(v, sess, sess.GetState(), slices.Index(sessions, sess)+1, d.herdrFocusedSession(sessions) == sess)
	return v, nil
}

func (d *Daemon) herdrPaneView(cs *connState, id string) (*Session, WindowState, *herdrPaneInfo, *herdrView, *herdrError) {
	sess, win, ierr := d.herdrFindPane(id)
	if ierr != nil {
		return nil, WindowState{}, nil, nil, ierr.herdr()
	}
	v, herr := d.herdrSessionView(cs, sess)
	if herr != nil {
		return nil, WindowState{}, nil, nil, herr
	}
	if p := v.pane(herdrPaneID(sess.ID, win.ID)); p != nil {
		return sess, win, p, v, nil
	}
	return nil, WindowState{}, nil, nil, herdrNotFound("pane", id).herdr()
}

func (v *herdrView) pane(id string) *herdrPaneInfo {
	for i := range v.panes {
		if v.panes[i].PaneID == id {
			return &v.panes[i]
		}
	}
	return nil
}

func (v *herdrView) tab(id string) *herdrTab {
	for i := range v.tabs {
		if v.tabs[i].TabID == id {
			return &v.tabs[i]
		}
	}
	return nil
}

func (d *Daemon) herdrPaneResult(cs *connState, kind, id string) (*herdrResult, *herdrError) {
	_, _, p, _, herr := d.herdrPaneView(cs, id)
	if herr != nil {
		return nil, herr
	}
	return &herdrResult{Type: kind, Pane: p}, nil
}

func herdrWin(sess *Session, windowID string) herdrArgs {
	return herdrArgs{Session: sess.Name(), Window: windowID}
}

func (d *Daemon) herdrWorkspaceList(cs *connState, _ *herdrIn) (*herdrResult, *herdrError) {
	return &herdrResult{Type: "workspace_list", Workspaces: &d.buildHerdrView(cs).workspaces}, nil
}

func (d *Daemon) herdrWorkspaceGet(cs *connState, in *herdrIn) (*herdrResult, *herdrError) {
	return d.herdrWorkspaceInfo(cs, in.WorkspaceID)
}

func (d *Daemon) herdrWorkspaceInfo(cs *connState, id string) (*herdrResult, *herdrError) {
	sess, ierr := d.herdrFindSession(id)
	if ierr != nil {
		return nil, ierr.herdr()
	}
	v, herr := d.herdrSessionView(cs, sess)
	if herr != nil {
		return nil, herr
	}
	return &herdrResult{Type: "workspace_info", Workspace: &v.workspaces[0]}, nil
}

func (d *Daemon) herdrWorkspaceCreate(cs *connState, in *herdrIn) (*herdrResult, *herdrError) {
	return d.herdrNewSession(cs, herdrArgs{Cwd: in.Cwd, Env: in.Env}, in.label(), "workspace_create_failed", &herdrResult{Type: "workspace_created"})
}

func (d *Daemon) herdrNewSession(cs *connState, args herdrArgs, label, fail string, res *herdrResult) (*herdrResult, *herdrError) {
	out, herr := d.herdrVerb(cs, "new-session", args, fail)
	if herr != nil {
		return nil, herr
	}
	return d.herdrLabelled(cs, herdrString(out, "session"), herdrString(out, "window_id"), label, fail, res)
}

func (d *Daemon) herdrLabelled(cs *connState, name, windowID, label, fail string, res *herdrResult) (*herdrResult, *herdrError) {
	sess := d.manager.GetSession(name)
	if sess == nil {
		return nil, herdrErr(fail, "the new session is gone")
	}
	if label != "" {
		if _, herr := d.herdrVerb(cs, "set-session-name", herdrArgs{Session: name, Name: &label}, fail); herr != nil {
			return nil, herr
		}
	}
	if res.Worktree != nil {
		res.Worktree.OpenWorkspaceID = herdrWorkspaceID(sess.ID)
	}
	return d.herdrCreated(cs, sess, windowID, res)
}

func (d *Daemon) herdrCreated(cs *connState, sess *Session, windowID string, res *herdrResult) (*herdrResult, *herdrError) {
	v, herr := d.herdrSessionView(cs, sess)
	if herr != nil {
		return nil, herr
	}
	res.RootPane = v.pane(herdrPaneID(sess.ID, windowID))
	if res.RootPane == nil {
		return nil, herdrErr("pane_not_found", "the new pane closed before it could be read")
	}
	res.Tab = v.tab(res.RootPane.TabID)
	if res.Type != "tab_created" {
		res.Workspace = &v.workspaces[0]
	}
	return res, nil
}

func (d *Daemon) herdrWorkspaceRename(cs *connState, in *herdrIn) (*herdrResult, *herdrError) {
	sess, ierr := d.herdrFindSession(in.WorkspaceID)
	if ierr != nil {
		return nil, ierr.herdr()
	}
	if _, herr := d.herdrVerb(cs, "set-session-name", herdrArgs{Session: sess.Name(), Name: in.Label}, "workspace_rename_failed"); herr != nil {
		return nil, herr
	}
	return d.herdrWorkspaceInfo(cs, in.WorkspaceID)
}

func (d *Daemon) herdrWorkspaceClose(cs *connState, in *herdrIn) (*herdrResult, *herdrError) {
	sess, ierr := d.herdrFindSession(in.WorkspaceID)
	if ierr != nil {
		return nil, ierr.herdr()
	}
	if _, herr := d.herdrVerb(cs, "kill-session", herdrArgs{Session: sess.Name()}, "workspace_close_failed"); herr != nil {
		return nil, herr
	}
	return herdrAck, nil
}

func (d *Daemon) herdrTabList(cs *connState, in *herdrIn) (*herdrResult, *herdrError) {
	if in.WorkspaceID == "" {
		return &herdrResult{Type: "tab_list", Tabs: &d.buildHerdrView(cs).tabs}, nil
	}
	sess, ierr := d.herdrFindSession(in.WorkspaceID)
	if ierr != nil {
		return nil, ierr.herdr()
	}
	v, herr := d.herdrSessionView(cs, sess)
	if herr != nil {
		return nil, herr
	}
	return &herdrResult{Type: "tab_list", Tabs: &v.tabs}, nil
}

func (d *Daemon) herdrTabGet(cs *connState, in *herdrIn) (*herdrResult, *herdrError) {
	return d.herdrTabInfo(cs, in.TabID)
}

func (d *Daemon) herdrTabInfo(cs *connState, id string) (*herdrResult, *herdrError) {
	sess, ws, ierr := d.herdrFindTab(id)
	if ierr != nil {
		return nil, ierr.herdr()
	}
	v, herr := d.herdrSessionView(cs, sess)
	if herr != nil {
		return nil, herr
	}
	if t := v.tab(herdrTabID(sess.ID, sess.GetState().WorkspaceTabKey(ws))); t != nil {
		return &herdrResult{Type: "tab_info", Tab: t}, nil
	}
	return nil, herdrNotFound("tab", id).herdr()
}

func (d *Daemon) herdrTabCreate(cs *connState, in *herdrIn) (*herdrResult, *herdrError) {
	sess, herr := d.herdrSessionOrFocused(cs, in.WorkspaceID)
	if herr != nil {
		return nil, herr
	}
	st := sess.GetState()
	ws := 0
	for _, n := range herdrWorkspaceOrder(st) {
		if !herdrListedWorkspace(st, n) {
			ws = n
			break
		}
	}
	if ws == 0 {
		return nil, herdrErr("tab_create_failed", fmt.Sprintf("every one of the %d workspaces of this session is in use. Close a tab first", st.workspaceBound()))
	}
	const fail = "tab_create_failed"
	label := in.label()
	if label != "" {
		if _, herr := d.herdrVerb(cs, "set-workspace-name", herdrArgs{Session: sess.Name(), Workspace: ws, Name: &label}, fail); herr != nil {
			return nil, herr
		}
	}
	out, herr := d.herdrVerb(cs, "new-window", herdrArgs{Session: sess.Name(), Workspace: ws, Focus: &in.Focus, Cwd: in.Cwd, Env: in.Env}, fail)
	if herr != nil {
		if label != "" {
			_, _ = d.herdrVerb(cs, "set-workspace-name", herdrArgs{Session: sess.Name(), Workspace: ws, Name: new("")}, fail)
		}
		return nil, herr
	}
	if in.Focus {
		if _, herr := d.herdrVerb(cs, "select-workspace", herdrArgs{Session: sess.Name(), Workspace: ws}, fail); herr != nil {
			return nil, herr
		}
	}
	return d.herdrCreated(cs, sess, herdrString(out, "window_id"), &herdrResult{Type: "tab_created"})
}

func (d *Daemon) herdrSessionOrFocused(cs *connState, id string) (*Session, *herdrError) {
	if id != "" {
		sess, ierr := d.herdrFindSession(id)
		if ierr != nil {
			return nil, ierr.herdr()
		}
		return sess, nil
	}
	sess := d.herdrFocusedSession(d.herdrSessions(cs))
	if sess == nil {
		return nil, herdrErr("workspace_not_found", "there is no workspace")
	}
	return sess, nil
}

func (d *Daemon) herdrTabRename(cs *connState, in *herdrIn) (*herdrResult, *herdrError) {
	return d.herdrTabVerb(cs, in.TabID, "set-workspace-name", herdrArgs{Name: in.Label}, "tab_rename_failed")
}

func (d *Daemon) herdrTabFocus(cs *connState, in *herdrIn) (*herdrResult, *herdrError) {
	return d.herdrTabVerb(cs, in.TabID, "select-workspace", herdrArgs{}, "tab_focus_failed")
}

func (d *Daemon) herdrTabVerb(cs *connState, id, verb string, args herdrArgs, fail string) (*herdrResult, *herdrError) {
	sess, ws, ierr := d.herdrFindTab(id)
	if ierr != nil {
		return nil, ierr.herdr()
	}
	args.Session, args.Workspace = sess.Name(), ws
	if _, herr := d.herdrVerb(cs, verb, args, fail); herr != nil {
		return nil, herr
	}
	return d.herdrTabInfo(cs, id)
}

func (d *Daemon) herdrTabMove(cs *connState, in *herdrIn) (*herdrResult, *herdrError) {
	if *in.InsertIndex < 0 {
		return nil, herdrErr("invalid_request", "invalid request: insert_index must not be negative")
	}
	sess, ws, ierr := d.herdrFindTab(in.TabID)
	if ierr != nil {
		return nil, ierr.herdr()
	}
	order := herdrOrderedTabs(sess.GetState())
	from := slices.Index(order, ws)
	at := min(*in.InsertIndex, len(order))
	order = slices.Insert(order, at, ws)
	if at <= from {
		from++
	}
	order = slices.Delete(order, from, from+1)
	if _, herr := d.herdrVerb(cs, "set-workspace-order", herdrArgs{Session: sess.Name(), Order: order}, "tab_move_failed"); herr != nil {
		return nil, herr
	}
	v, herr := d.herdrSessionView(cs, sess)
	if herr != nil {
		return nil, herr
	}
	return &herdrResult{Type: "tab_list", Tabs: &v.tabs}, nil
}

func (d *Daemon) herdrTabClose(cs *connState, in *herdrIn) (*herdrResult, *herdrError) {
	sess, ws, ierr := d.herdrFindTab(in.TabID)
	if ierr != nil {
		return nil, ierr.herdr()
	}
	const fail = "tab_close_failed"
	st := sess.GetState()
	if st.WorkspaceNames[ws] != "" {
		if _, herr := d.herdrVerb(cs, "set-workspace-name", herdrArgs{Session: sess.Name(), Workspace: ws, Name: new("")}, fail); herr != nil {
			return nil, herr
		}
	}
	for _, w := range st.Windows {
		if w.Workspace != ws || herdrScratch(&w) {
			continue
		}
		if _, verr := d.callVerb(cs, "close-window", herdrWin(sess, w.ID)); verr != nil && verr.Code != ErrVerbWindowNotFound {
			return nil, herdrFromVerb(verr, fail)
		}
	}
	if st = sess.GetState(); st.CurrentWorkspace == ws {
		if next := herdrNeighbourTab(st, ws); next != 0 {
			if _, herr := d.herdrVerb(cs, "select-workspace", herdrArgs{Session: sess.Name(), Workspace: next}, fail); herr != nil {
				return nil, herr
			}
		}
	}
	return herdrAck, nil
}

func herdrNeighbourTab(st *SessionState, ws int) int {
	order := herdrWorkspaceOrder(st)
	at := slices.Index(order, ws)
	holds := func(n int) bool { return herdrCount(st, n) > 0 }
	for i := at - 1; i >= 0; i-- {
		if holds(order[i]) {
			return order[i]
		}
	}
	for i := at + 1; i < len(order); i++ {
		if holds(order[i]) {
			return order[i]
		}
	}
	return 0
}

func (d *Daemon) herdrPaneList(cs *connState, in *herdrIn) (*herdrResult, *herdrError) {
	if in.WorkspaceID == "" {
		return &herdrResult{Type: "pane_list", Panes: &d.buildHerdrView(cs).panes}, nil
	}
	sess, ierr := d.herdrFindSession(in.WorkspaceID)
	if ierr != nil {
		return nil, ierr.herdr()
	}
	v, herr := d.herdrSessionView(cs, sess)
	if herr != nil {
		return nil, herr
	}
	return &herdrResult{Type: "pane_list", Panes: &v.panes}, nil
}

func (d *Daemon) herdrPaneGet(cs *connState, in *herdrIn) (*herdrResult, *herdrError) {
	return d.herdrPaneResult(cs, "pane_info", in.PaneID)
}

func (d *Daemon) herdrPaneCurrent(cs *connState, in *herdrIn) (*herdrResult, *herdrError) {
	id := in.CallerPaneID
	if id == "" {
		if fromPane, window := d.peerPane(cs); fromPane {
			id = window
		}
	}
	if id == "" {
		if id = d.buildHerdrView(cs).focusedPane; id == "" {
			return nil, herdrErr("pane_not_found", "pane not found")
		}
	}
	return d.herdrPaneResult(cs, "pane_current", id)
}

func (d *Daemon) herdrPaneLayout(cs *connState, in *herdrIn) (*herdrResult, *herdrError) {
	id := in.PaneID
	if id == "" {
		id = d.buildHerdrView(cs).focusedPane
	}
	_, _, pane, v, herr := d.herdrPaneView(cs, id)
	if herr != nil {
		return nil, herr
	}
	for i := range v.layouts {
		if v.layouts[i].TabID == pane.TabID {
			return &herdrResult{Type: "pane_layout", Layout: &v.layouts[i]}, nil
		}
	}
	return nil, herdrErr("layout_not_found", "no layout for tab "+pane.TabID)
}

func (d *Daemon) herdrPaneRead(cs *connState, in *herdrIn) (*herdrResult, *herdrError) {
	return d.herdrRead(cs, in.PaneID, in)
}

func (d *Daemon) herdrRead(cs *connState, id string, in *herdrIn) (*herdrResult, *herdrError) {
	source := herdrSource(in.Source)
	if source == "" {
		return nil, herdrErr("invalid_request", "invalid request: unknown variant `"+in.Source+"`, expected one of `visible`, `recent`, `recent_unwrapped`, `detection`")
	}
	format := in.Format
	switch format {
	case "":
		format = "text"
	case "text", "ansi":
	default:
		return nil, herdrErr("invalid_request", "invalid request: unknown variant `"+format+"`, expected `text` or `ansi`")
	}
	sess, win, pane, _, herr := d.herdrPaneView(cs, id)
	if herr != nil {
		return nil, herr
	}
	lines := 0
	if in.Lines != nil {
		lines = min(max(*in.Lines, 0), herdrReadMaxLines)
	} else if source == "recent" {
		lines = herdrReadDefaultLines
	}
	args := herdrWin(sess, win.ID)
	args.Source, args.Styled = source, format == "ansi"
	if lines > 0 {
		args.Lines = lines + 1
	}
	out, herr := d.herdrVerb(cs, "capture-pane", args, "read_failed")
	if herr != nil {
		return nil, herr
	}
	text := herdrString(out, "content")
	truncated := false
	if lines > 0 {
		rows := strings.SplitAfter(text, "\n")
		if rows[len(rows)-1] == "" {
			rows = rows[:len(rows)-1]
		}
		if len(rows) > lines {
			truncated = true
			text = strings.Join(rows[len(rows)-lines:], "")
		}
	}
	return &herdrResult{Type: "pane_read", Read: &herdrReadRecord{
		PaneID: pane.PaneID, WorkspaceID: pane.WorkspaceID, TabID: pane.TabID,
		Source: in.Source, Format: format, Text: text, Revision: pane.Revision, Truncated: truncated,
	}}, nil
}

func herdrSource(s string) string {
	switch s {
	case "visible", "detection":
		return "visible"
	case "recent", "recent_unwrapped":
		return "recent"
	}
	return ""
}

func (d *Daemon) herdrPaneSend(cs *connState, in *herdrIn) (*herdrResult, *herdrError) {
	return d.herdrSend(cs, in.PaneID, in.Text, in.Keys, "pane_send_failed")
}

func (d *Daemon) herdrSend(cs *connState, id, text string, keys []string, fail string) (*herdrResult, *herdrError) {
	runs, bad := herdrKeyRuns(keys)
	if bad != "" {
		return nil, herdrErr("invalid_key", "unsupported key "+bad)
	}
	sess, win, ierr := d.herdrFindPane(id)
	if ierr != nil {
		return nil, ierr.herdr()
	}
	if inner, ok := strings.CutPrefix(text, bracketedPasteStart); ok {
		text = strings.TrimSuffix(inner, bracketedPasteEnd)
	}
	if text != "" {
		args := herdrWin(sess, win.ID)
		args.Text, args.Paste, args.AllowBlocked = text, true, true
		if _, herr := d.herdrVerb(cs, "send-text", args, fail); herr != nil {
			return nil, herr
		}
	}
	for _, r := range runs {
		args := herdrWin(sess, win.ID)
		args.Keys, args.Literal, args.AllowBlocked = r.keys, r.literal, true
		if _, verr := d.callVerb(cs, "send-keys", args); verr != nil {
			if verr.Code == ErrVerbInvalidParams {
				return nil, herdrErr("invalid_key", verr.Message)
			}
			return nil, herdrFromVerb(verr, fail)
		}
	}
	return herdrAck, nil
}

func (d *Daemon) herdrPaneRename(cs *connState, in *herdrIn) (*herdrResult, *herdrError) {
	sess, win, ierr := d.herdrFindPane(in.PaneID)
	if ierr != nil {
		return nil, ierr.herdr()
	}
	args := herdrWin(sess, win.ID)
	args.Name = new(strings.TrimSpace(in.label()))
	if _, herr := d.herdrVerb(cs, "set-window", args, "pane_rename_failed"); herr != nil {
		return nil, herr
	}
	return d.herdrPaneResult(cs, "pane_info", in.PaneID)
}

func (d *Daemon) herdrPaneFocus(cs *connState, in *herdrIn) (*herdrResult, *herdrError) {
	return d.herdrFocusPane(cs, in.PaneID)
}

func (d *Daemon) herdrFocusPane(cs *connState, id string) (*herdrResult, *herdrError) {
	sess, win, ierr := d.herdrFindPane(id)
	if ierr != nil {
		return nil, ierr.herdr()
	}
	if _, herr := d.herdrVerb(cs, "focus-window", herdrWin(sess, win.ID), "pane_focus_failed"); herr != nil {
		return nil, herr
	}
	if d.findTUIClient(sess.ID) == nil {
		if herr := d.herdrAdmit(cs, "select-workspace", sess); herr != nil {
			return nil, herr
		}
		if _, herr := d.herdrShowSession(cs, sess, "pane_focus_failed"); herr != nil {
			return nil, herr
		}
	}
	sess.MarkCompletionSeen(win.ID)
	return d.herdrPaneResult(cs, "pane_info", id)
}

func (d *Daemon) herdrPaneSplit(cs *connState, in *herdrIn) (*herdrResult, *herdrError) {
	if in.Direction != "right" && in.Direction != "down" {
		return nil, herdrErr("invalid_request", "invalid request: unknown variant `"+in.Direction+"`, expected `right` or `down`")
	}
	target := in.TargetPaneID
	if target == "" {
		if ref := d.callerPaneRef(cs); ref != nil && in.WorkspaceID == "" {
			if sess := d.sessionHoldingWindow(ref.Window); sess != nil {
				target = herdrPaneID(sess.ID, ref.Window)
			}
		}
	}
	if target == "" {
		sess, herr := d.herdrSessionOrFocused(cs, in.WorkspaceID)
		if herr != nil {
			return nil, herr
		}
		st := sess.GetState()
		if st.FocusedWindowID == "" {
			return nil, herdrErr("pane_not_found", "the workspace has no focused pane")
		}
		target = herdrPaneID(sess.ID, st.FocusedWindowID)
	}
	sess, win, ierr := d.herdrFindPane(target)
	if ierr != nil {
		return nil, ierr.herdr()
	}
	args := herdrWin(sess, win.ID)
	args.Side, args.Focus, args.Cwd, args.Env = in.Direction, &in.Focus, in.Cwd, in.Env
	out, herr := d.herdrVerb(cs, "split-window", args, "pane_split_failed")
	if herr != nil {
		return nil, herr
	}
	return d.herdrPaneResult(cs, "pane_info", herdrPaneID(sess.ID, herdrString(out, "window_id")))
}

func (d *Daemon) herdrPaneClose(cs *connState, in *herdrIn) (*herdrResult, *herdrError) {
	sess, win, ierr := d.herdrFindPane(in.PaneID)
	if ierr != nil {
		return nil, ierr.herdr()
	}
	if _, herr := d.herdrVerb(cs, "close-window", herdrWin(sess, win.ID), "pane_close_failed"); herr != nil {
		return nil, herr
	}
	return herdrAck, nil
}

func (d *Daemon) herdrPaneWaitForOutput(cs *connState, in *herdrIn) (*herdrResult, *herdrError) {
	pattern := ""
	switch in.Match.Type {
	case "substring":
		pattern = regexp.QuoteMeta(in.Match.Value)
	case "regex":
		if _, err := regexp.Compile(in.Match.Value); err != nil {
			return nil, herdrErr("invalid_regex", err.Error())
		}
		pattern = in.Match.Value
	default:
		return nil, herdrErr("invalid_request", "invalid request: unknown variant `"+in.Match.Type+"`, expected `substring` or `regex`")
	}
	sess, win, ierr := d.herdrFindPane(in.PaneID)
	if ierr != nil {
		return nil, ierr.herdr()
	}
	args := herdrWin(sess, win.ID)
	args.Condition, args.Pattern, args.Source, args.Timeout = "window-output", pattern, herdrSource(in.Source), in.TimeoutMS
	if args.Source == "" {
		args.Source = "recent"
	}
	if _, herr := d.herdrVerb(cs, "wait-for", args, "wait_failed"); herr != nil {
		return nil, herr
	}
	in.Format = "text"
	res, herr := d.herdrRead(cs, in.PaneID, in)
	if herr != nil {
		return nil, herr
	}
	re := regexp.MustCompile(pattern)
	var matched *string
	for line := range strings.SplitSeq(res.Read.Text, "\n") {
		if re.MatchString(line) {
			matched = new(line)
		}
	}
	return &herdrResult{Type: "output_matched", PaneID: res.Read.PaneID, Revision: &res.Read.Revision, MatchedLine: matched, Read: res.Read}, nil
}

func (d *Daemon) herdrAgentList(cs *connState, _ *herdrIn) (*herdrResult, *herdrError) {
	return &herdrResult{Type: "agent_list", Agents: &d.buildHerdrView(cs).agents}, nil
}

func (d *Daemon) herdrAgentTarget(cs *connState, target string) (string, *herdrError) {
	if _, _, ierr := d.herdrFindPane(target); ierr == nil {
		return target, nil
	}
	found := ""
	for _, p := range d.buildHerdrView(cs).panes {
		if p.TerminalID == target || (p.Agent != "" && (p.Label == target || p.Agent == target)) {
			if found != "" {
				return "", herdrErr("agent_not_found", "more than one agent matches "+echoName(target)+". Name it by pane id")
			}
			found = p.PaneID
		}
	}
	if found == "" {
		return "", herdrErr("agent_not_found", "agent "+echoName(target)+" not found")
	}
	return found, nil
}

func (d *Daemon) herdrAgentResult(cs *connState, kind, id string) (*herdrResult, *herdrError) {
	_, _, pane, v, herr := d.herdrPaneView(cs, id)
	if herr != nil {
		return nil, herr
	}
	for i := range v.agents {
		if v.agents[i].PaneID == pane.PaneID {
			return &herdrResult{Type: kind, Agent: &v.agents[i]}, nil
		}
	}
	return nil, herdrErr("agent_not_found", "pane "+pane.PaneID+" holds no agent")
}

func (d *Daemon) herdrAgent(cs *connState, in *herdrIn, run func(id string) (*herdrResult, *herdrError)) (*herdrResult, *herdrError) {
	id, herr := d.herdrAgentTarget(cs, in.Target)
	if herr != nil {
		return nil, herr
	}
	return run(id)
}

func (d *Daemon) herdrAgentGet(cs *connState, in *herdrIn) (*herdrResult, *herdrError) {
	return d.herdrAgent(cs, in, func(id string) (*herdrResult, *herdrError) { return d.herdrAgentResult(cs, "agent_info", id) })
}

func (d *Daemon) herdrAgentRead(cs *connState, in *herdrIn) (*herdrResult, *herdrError) {
	return d.herdrAgent(cs, in, func(id string) (*herdrResult, *herdrError) { return d.herdrRead(cs, id, in) })
}

func (d *Daemon) herdrAgentSendKeys(cs *connState, in *herdrIn) (*herdrResult, *herdrError) {
	return d.herdrAgent(cs, in, func(id string) (*herdrResult, *herdrError) {
		return d.herdrSend(cs, id, "", in.Keys, "agent_send_keys_failed")
	})
}

func (d *Daemon) herdrAgentFocus(cs *connState, in *herdrIn) (*herdrResult, *herdrError) {
	return d.herdrAgent(cs, in, func(id string) (*herdrResult, *herdrError) { return d.herdrFocusPane(cs, id) })
}

func herdrStatesFor(statuses []string) (string, *herdrError) {
	var states []string
	for _, s := range statuses {
		switch s {
		case "idle", "done":
			states = append(states, string(AgentStateIdle), string(AgentStateDone))
			if s == "done" {
				states = append(states, string(AgentStateErrored), string(AgentStateLimitReached))
			}
		case "working":
			states = append(states, string(AgentStateWorking))
		case "blocked":
			states = append(states, string(AgentStateNeedsInput))
		case "unknown":
			states = append(states, string(AgentStateUnknown))
		default:
			return "", herdrErr("invalid_request", "invalid request: unknown variant `"+s+"`, expected one of `idle`, `working`, `blocked`, `done`, `unknown`")
		}
	}
	return strings.Join(states, ","), nil
}

func (d *Daemon) herdrWaitAgent(cs *connState, id string, until []string, timeoutMS *int64) *herdrError {
	if len(until) == 0 {
		until = []string{"idle", "done"}
	}
	states, herr := herdrStatesFor(until)
	if herr != nil {
		return herr
	}
	sess, win, ierr := d.herdrFindPane(id)
	if ierr != nil {
		return ierr.herdr()
	}
	args := herdrWin(sess, win.ID)
	args.Condition, args.Until, args.Timeout = "agent-state", states, timeoutMS
	raw, err := jsonx.Marshal(args)
	if err != nil {
		return herdrErr("internal_error", "could not encode params")
	}
	if _, _, verr := d.admitVerb(cs, "wait-for", raw); verr != nil {
		return herdrFromVerb(verr, "wait_failed")
	}
	timeout := defaultWaitTimeout
	if timeoutMS != nil {
		if *timeoutMS < 0 || *timeoutMS > maxWaitTimeoutMS {
			return herdrFromVerb(invalidParam("timeout", fmt.Sprintf("timeout is milliseconds from 1 to %d (24 hours)", maxWaitTimeoutMS)), "wait_failed")
		}
		if *timeoutMS > 0 {
			timeout = time.Duration(*timeoutMS) * time.Millisecond
		}
	}
	deadline, stop := d.waitDeadline(cs, timeout)
	defer stop()
	sub := d.events.subscribe(eventFilter{window: win.ID, types: map[string]bool{
		EventAgentState: true, EventAgentSeen: true, EventWindowClosed: true,
	}}, defaultEventQueue)
	defer d.events.unsubscribe(sub)
	closed := func() *herdrError {
		return herdrFromVerb(newVerbError(ErrVerbWindowNotFound, "the watched window closed before reaching "+strings.Join(until, ",")), "wait_failed")
	}
	matches := func() (matched, gone bool) {
		w, ok := findWindowState(sess.GetState(), win.ID)
		if !ok {
			return false, true
		}
		return slices.Contains(until, herdrStatus(w.AgentState, sess.finishedUnread(&w))), false
	}
	if ok, gone := matches(); ok {
		return nil
	} else if gone {
		return closed()
	}
	for {
		select {
		case <-deadline:
			return herdrFromVerb(agentStateTimeout(strings.Join(until, ",")), "wait_failed")
		case <-d.ctx.Done():
			return herdrErr("internal_error", "daemon is shutting down")
		case ev := <-sub.ch:
			if ev.Type == EventWindowClosed {
				return closed()
			}
			if ok, gone := matches(); ok {
				return nil
			} else if gone {
				return closed()
			}
		}
	}
}

func (d *Daemon) herdrAgentWait(cs *connState, in *herdrIn) (*herdrResult, *herdrError) {
	return d.herdrAgent(cs, in, func(id string) (*herdrResult, *herdrError) {
		if herr := d.herdrWaitAgent(cs, id, in.Until, in.TimeoutMS); herr != nil {
			return nil, herr
		}
		return d.herdrAgentResult(cs, "agent_info", id)
	})
}

func (d *Daemon) herdrAgentPrompt(cs *connState, in *herdrIn) (*herdrResult, *herdrError) {
	return d.herdrAgent(cs, in, func(id string) (*herdrResult, *herdrError) {
		res, herr := d.herdrAgentResult(cs, "agent_prompted", id)
		if herr != nil {
			return nil, herr
		}
		if res.Agent.AgentStatus == "blocked" {
			return nil, herdrErr("agent_blocked", "agent "+res.Agent.PaneID+" is blocked and needs interactive input. Nothing was typed")
		}
		sess, win, ierr := d.herdrFindPane(id)
		if ierr != nil {
			return nil, ierr.herdr()
		}
		args := herdrWin(sess, win.ID)
		args.Text, args.Submit = in.Text, true
		if _, herr := d.herdrVerb(cs, "send-text", args, "agent_prompt_failed"); herr != nil {
			return nil, herr
		}
		if in.Wait != nil {
			if herr := d.herdrWaitAgent(cs, id, in.Wait.Until, in.Wait.TimeoutMS); herr != nil {
				return nil, herr
			}
		}
		return d.herdrAgentResult(cs, "agent_prompted", id)
	})
}

type herdrWorktreeRecord struct {
	Path             string  `json:"path"`
	Branch           *string `json:"branch,omitempty"`
	IsBare           bool    `json:"is_bare"`
	IsDetached       bool    `json:"is_detached"`
	IsPrunable       bool    `json:"is_prunable"`
	IsLinkedWorktree bool    `json:"is_linked_worktree"`
	OpenWorkspaceID  string  `json:"open_workspace_id,omitempty"`
	Label            string  `json:"label"`
}

func (d *Daemon) herdrRepoDir(cs *connState, in *herdrIn) (string, *herdrError) {
	if in.Cwd != "" {
		return in.Cwd, nil
	}
	if in.WorkspaceID == "" {
		return "", herdrErr("invalid_request", "invalid request: pass cwd or workspace_id")
	}
	sess, ierr := d.herdrFindSession(in.WorkspaceID)
	if ierr != nil {
		return "", ierr.herdr()
	}
	v, herr := d.herdrSessionView(cs, sess)
	if herr != nil {
		return "", herr
	}
	if wt := v.workspaces[0].Worktree; wt != nil {
		return wt.CheckoutPath, nil
	}
	for _, p := range v.panes {
		if p.Cwd != "" {
			return p.Cwd, nil
		}
	}
	return "", herdrErr("not_git_repository", "the workspace has no directory")
}

func (d *Daemon) herdrWorktreeList(cs *connState, in *herdrIn) (*herdrResult, *herdrError) {
	if _, _, verr := d.admitVerb(cs, "list-worktrees", nil); verr != nil {
		return nil, herdrFromVerb(verr, "forbidden")
	}
	dir, herr := d.herdrRepoDir(cs, in)
	if herr != nil {
		return nil, herr
	}
	root, err := worktree.Root(dir)
	if err != nil {
		return nil, herdrErr("not_git_repository", err.Error())
	}
	entries, err := worktree.List(dir)
	if err != nil {
		return nil, herdrErr("worktree_list_failed", err.Error())
	}
	open := d.herdrOpenCheckouts(cs)
	out := make([]herdrWorktreeRecord, 0, len(entries))
	for _, e := range entries {
		out = append(out, herdrWorktreeOf(e, open))
	}
	return &herdrResult{Type: "worktree_list", Worktrees: &out, Source: &herdrWorktreeSource{
		RepoKey: root, RepoName: filepath.Base(root), RepoRoot: root, SourceCheckout: dir, SourceWorkspaceID: in.WorkspaceID,
	}}, nil
}

func herdrWorktreeOf(e worktree.Entry, open map[string]string) herdrWorktreeRecord {
	r := herdrWorktreeRecord{
		Path: e.Path, IsBare: e.Bare, IsDetached: e.Detached, IsPrunable: e.Prunable,
		IsLinkedWorktree: e.Linked, OpenWorkspaceID: open[herdrRealPath(e.Path)], Label: filepath.Base(e.Path),
	}
	if e.Branch != "" {
		r.Branch, r.Label = new(e.Branch), e.Branch
	}
	return r
}

func (d *Daemon) herdrOpenCheckouts(cs *connState) map[string]string {
	open := make(map[string]string)
	for _, w := range d.buildHerdrView(cs).workspaces {
		if w.Worktree == nil || w.Worktree.CheckoutPath == "" {
			continue
		}
		if path := herdrRealPath(w.Worktree.CheckoutPath); open[path] == "" {
			open[path] = w.WorkspaceID
		}
	}
	return open
}

func (d *Daemon) herdrWorktreeCreate(cs *connState, in *herdrIn) (*herdrResult, *herdrError) {
	if in.Branch == "" {
		return nil, herdrErr("invalid_request", "invalid request: tuios needs a branch for a new worktree")
	}
	if in.Path != "" {
		return nil, herdrErr("unsupported", "tuios puts a new worktree in its own worktree directory. Leave path out")
	}
	dir, herr := d.herdrRepoDir(cs, in)
	if herr != nil {
		return nil, herr
	}
	const fail = "worktree_create_failed"
	out, herr := d.herdrVerb(cs, "new-worktree", herdrArgs{Repo: dir, Branch: in.Branch, Base: in.Base}, fail)
	if herr != nil {
		return nil, herr
	}
	wt := &herdrWorktreeRecord{Path: herdrString(out, "path"), Branch: new(in.Branch), IsLinkedWorktree: true, Label: in.Branch}
	return d.herdrLabelled(cs, herdrString(out, "session"), herdrString(out, "window_id"), in.label(), fail, &herdrResult{Type: "worktree_created", Worktree: wt})
}

func herdrRealPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}

func (d *Daemon) herdrWorktreeOpen(cs *connState, in *herdrIn) (*herdrResult, *herdrError) {
	if _, _, verr := d.admitVerb(cs, "list-worktrees", nil); verr != nil {
		return nil, herdrFromVerb(verr, "forbidden")
	}
	dir, herr := d.herdrRepoDir(cs, in)
	if herr != nil {
		return nil, herr
	}
	entries, err := worktree.List(dir)
	if err != nil {
		return nil, herdrErr("not_git_worktree", err.Error())
	}
	var entry *worktree.Entry
	want := herdrRealPath(in.Path)
	for i, e := range entries {
		if (in.Path != "" && herdrRealPath(e.Path) == want) || (in.Path == "" && in.Branch != "" && e.Branch == in.Branch) {
			entry = &entries[i]
		}
	}
	if entry == nil {
		return nil, herdrErr("worktree_not_found", "no worktree of this repository at "+echoName(in.Path+in.Branch))
	}
	open := d.herdrOpenCheckouts(cs)
	rec := herdrWorktreeOf(*entry, open)
	res := &herdrResult{Type: "worktree_opened", Worktree: &rec, AlreadyOpen: new(false)}
	if id := open[herdrRealPath(entry.Path)]; id != "" {
		sess, ierr := d.herdrFindSession(id)
		if ierr != nil {
			return nil, ierr.herdr()
		}
		st := sess.GetState()
		root := st.FocusedWindowID
		if root == "" && len(st.Windows) > 0 {
			root = st.Windows[0].ID
		}
		res.AlreadyOpen = new(true)
		return d.herdrCreated(cs, sess, root, res)
	}
	args := herdrArgs{Cwd: entry.Path}
	if name := herdrWorktreeSessionName(dir, *entry); d.manager.GetSession(name) == nil {
		args.Name = &name
	}
	return d.herdrNewSession(cs, args, in.label(), "worktree_open_failed", res)
}

func herdrWorktreeSessionName(dir string, e worktree.Entry) string {
	repo := filepath.Base(dir)
	if root, err := worktree.Root(dir); err == nil {
		repo = filepath.Base(root)
	}
	if e.Branch == "" {
		return worktree.SessionName(repo, filepath.Base(e.Path))
	}
	return worktree.SessionName(repo, e.Branch)
}

func (d *Daemon) herdrWorktreeRemove(cs *connState, in *herdrIn) (*herdrResult, *herdrError) {
	sess, ierr := d.herdrFindSession(in.WorkspaceID)
	if ierr != nil {
		return nil, ierr.herdr()
	}
	out, herr := d.herdrVerb(cs, "remove-worktree", herdrArgs{Session: sess.Name(), Force: in.Force}, "worktree_remove_failed")
	if herr != nil {
		return nil, herr
	}
	return &herdrResult{Type: "worktree_removed", WorkspaceID: in.WorkspaceID, Path: herdrString(out, "path"), Forced: &in.Force}, nil
}
