package session

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/worktree"
)

// herdr's socket API, answered by tuios.
//
// herdr (github.com/herdrdev/herdr) serves a JSON API on its socket that
// tools drive it with: Collie's herdr adapter, herdr plugins, bar widgets and
// editor bridges. tuios answers the part of that API that maps onto tuios on
// its herdr socket (HerdrSocketPath), so those tools work against tuios.
// herdr_compat.go carries the pane report methods and the transport;
// this file carries everything else.
//
// The API tracked is herdr's at herdrTargetVersion: the request and result
// types of src/api/schema, the handlers of src/app/api, and the stream of
// src/api/server.rs. docs/AGENT_STATE.md#herdr-compatibility lists what is
// answered and how, and how the version is tracked.
//
// # Mapping
//
//	herdr workspace   a tuios session
//	herdr tab         a tuios workspace (one of a session's numbered slots)
//	herdr pane        a tuios window
//
// herdr_ids.go says how the ids are made. A tuios workspace is a fixed slot,
// and herdr lists only the tabs that exist, so a workspace is a tab when it
// holds a window, has a name, or is the one showing.
//
// # Authority
//
// Every method that reads or changes tuios runs the tuios verb that does the
// same thing, through admitVerb, the path the verb socket uses. So a herdr
// client inside a pane is held to that pane's grants exactly as the verb
// would be: reads need read, typing needs write and passes typingRefusal
// (a pane without respond cannot type into a pane that waits on a prompt),
// and creating, closing, renaming and focusing need admin. A caller outside
// every pane is the person, as on the verb socket. The adapters translate
// shapes and ids, and hold no rule of their own.
//
// # Size
//
// Requests decode into one struct (herdrIn), results encode from one
// (herdrResult), and verb calls are built from one (herdrArgs), each with
// omitempty fields. A map literal per call site costs several times the code
// of a struct literal, and the binary has a size budget.

// herdrTargetVersion is the herdr release whose API this file follows, and
// herdrTargetProtocol the protocol number that release reports.
const (
	herdrTargetVersion  = "0.9.3"
	herdrTargetProtocol = 22
)

// herdrReadDefaultLines and herdrReadMaxLines bound a read, as herdr does.
const (
	herdrReadDefaultLines = 80
	herdrReadMaxLines     = 1000
)

// herdrError is a failed method: herdr's error code and message.
type herdrError struct{ code, msg string }

func herdrErr(code, msg string) *herdrError { return &herdrError{code: code, msg: msg} }

func (e *herdrIDError) herdr() *herdrError { return herdrErr(e.code, e.msg) }

// herdrIn is the params of every method, each field under herdr's name.
// Like herdr's serde types, a field a method does not take is ignored.
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

// label is the label, "" when there is none.
func (in *herdrIn) label() string {
	if in.Label == nil {
		return ""
	}
	return *in.Label
}

// herdrResult is the result of every method: herdr's ResponseResult, whose
// type says which fields it carries.
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

// herdrAck is the result of a method that only acknowledges.
var herdrAck = &herdrResult{Type: "ok"}

// herdrReadRecord is herdr's PaneReadResult.
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

// herdrWorktreeSource is herdr's WorktreeSourceInfo.
type herdrWorktreeSource struct {
	RepoKey           string `json:"repo_key"`
	RepoName          string `json:"repo_name"`
	RepoRoot          string `json:"repo_root"`
	SourceCheckout    string `json:"source_checkout_path"`
	SourceWorkspaceID string `json:"source_workspace_id,omitempty"`
}

// herdrArgs is the params of every verb call an adapter makes, each field
// under the verb's name. Name and Focus are pointers because the empty name
// and focus false are values the verbs read.
type herdrArgs struct {
	Session   string  `json:"session,omitempty"`
	Window    string  `json:"window,omitempty"`
	Name      *string `json:"name,omitempty"`
	Workspace int     `json:"workspace,omitempty"`
	Focus     *bool   `json:"focus,omitempty"`
	Cwd       string  `json:"cwd,omitempty"`
	Text      string  `json:"text,omitempty"`
	Keys      string  `json:"keys,omitempty"`
	Literal   bool    `json:"literal,omitempty"`
	Paste     bool    `json:"paste,omitempty"`
	Submit    bool    `json:"submit,omitempty"`
	Source    string  `json:"source,omitempty"`
	Styled    bool    `json:"styled,omitempty"`
	Lines     int     `json:"lines,omitempty"`
	Direction string  `json:"direction,omitempty"`
	Order     []int   `json:"order,omitempty"`
	Condition string  `json:"condition,omitempty"`
	Pattern   string  `json:"pattern,omitempty"`
	Until     string  `json:"until,omitempty"`
	Timeout   *int64  `json:"timeout,omitempty"`
	Repo      string  `json:"repo,omitempty"`
	Branch    string  `json:"branch,omitempty"`
	Base      string  `json:"base,omitempty"`
	Force     bool    `json:"force,omitempty"`
	Command   string  `json:"command,omitempty"`
}

// herdrMethod answers one method for cs.
type herdrMethod struct {
	run      func(d *Daemon, cs *connState, in *herdrIn) (*herdrResult, *herdrError)
	required []string
}

// herdrMethods are the methods tuios answers besides the pane reports, with
// the params each must have.
var herdrMethods map[string]herdrMethod

func init() {
	pane := []string{"pane_id"}
	tab := []string{"tab_id"}
	ws := []string{"workspace_id"}
	target := []string{"target"}
	herdrMethods = map[string]herdrMethod{
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

// herdrUnsupported are herdr's other methods. tuios has nothing they map
// onto, and answers each with herdr's error shape and code unsupported. A
// method herdr does not have at all is answered as herdr answers it: code
// invalid_request, "unknown variant".
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
	"popup.close",
	"integration.list", "integration.install", "integration.uninstall",
	"plugin.link", "plugin.list", "plugin.unlink", "plugin.enable", "plugin.disable",
	"plugin.action.list", "plugin.action.invoke", "plugin.log.list",
	"plugin.pane.open", "plugin.pane.focus", "plugin.pane.close",
}

// herdrAPICall answers a method that is not a pane report. handled is false
// for a method herdr does not have.
func (d *Daemon) herdrAPICall(cs *connState, method string, params json.RawMessage) (any, *herdrError, bool) {
	m, ok := herdrMethods[method]
	if !ok {
		if slices.Contains(herdrUnsupported, method) {
			return nil, herdrErr("unsupported", "tuios does not answer "+method+". See the herdr compatibility section of docs/AGENT_STATE.md for what it answers"), true
		}
		return nil, nil, false
	}
	in := &herdrIn{}
	if herr := herdrDecode(params, in, m.required...); herr != nil {
		return nil, herr, true
	}
	// Each send method takes only its own field.
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

// herdrDecode decodes params into p the way herdr's serde types do: unknown
// fields are ignored, and a field in required that is absent or null is
// invalid_request, with serde's words.
func herdrDecode(params json.RawMessage, p any, required ...string) *herdrError {
	if len(params) == 0 || string(params) == "null" {
		params = json.RawMessage("{}")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(params, &fields); err != nil {
		return herdrErr("invalid_request", "invalid request: params must be an object")
	}
	for _, name := range required {
		if raw, ok := fields[name]; !ok || string(raw) == "null" {
			return herdrErr("invalid_request", "invalid request: missing field `"+name+"`")
		}
	}
	if err := json.Unmarshal(params, p); err != nil {
		return herdrErr("invalid_request", "invalid request: "+err.Error())
	}
	return nil
}

// herdrFromVerb turns a verb's error into herdr's shape. fail is the code
// for a failure no herdr code names, such as pane_send_failed.
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

// herdrVerb runs a verb for cs and reads its result as an object.
func (d *Daemon) herdrVerb(cs *connState, verb string, args herdrArgs, fail string) (map[string]any, *herdrError) {
	out, verr := d.callVerb(cs, verb, args)
	if verr != nil {
		return nil, herdrFromVerb(verr, fail)
	}
	m, _ := out.(map[string]any)
	return m, nil
}

// herdrString is one string field of a verb's result.
func herdrString(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

func ptr[T any](v T) *T { return &v }

// herdrVersion is the version a pong and a snapshot report: the herdr
// release whose API this follows, marked as tuios in semver build metadata,
// which a version comparison ignores.
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

// herdrSessionView is the view of one session, for a method about one
// object. The caller must be allowed to read the session, as list-windows
// requires. Its number is its place in the full list.
func (d *Daemon) herdrSessionView(cs *connState, sess *Session) (*herdrView, *herdrError) {
	if _, _, verr := d.admitVerb(cs, "list-windows", herdrSessionParams(sess)); verr != nil {
		return nil, herdrFromVerb(verr, "forbidden")
	}
	sessions := d.herdrOrderedSessions()
	v := &herdrView{}
	d.addHerdrSession(v, sess, sess.GetState(), slices.Index(sessions, sess)+1, d.herdrFocusedSession(sessions) == sess)
	return v, nil
}

// herdrPaneView resolves a pane id and reads its session.
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

// pane is the view's record of a pane, nil for none.
func (v *herdrView) pane(id string) *herdrPaneInfo {
	for i := range v.panes {
		if v.panes[i].PaneID == id {
			return &v.panes[i]
		}
	}
	return nil
}

// tab is the view's record of a tab, nil for none.
func (v *herdrView) tab(id string) *herdrTab {
	for i := range v.tabs {
		if v.tabs[i].TabID == id {
			return &v.tabs[i]
		}
	}
	return nil
}

// herdrPaneResult answers with a pane's record.
func (d *Daemon) herdrPaneResult(cs *connState, kind, id string) (*herdrResult, *herdrError) {
	_, _, p, _, herr := d.herdrPaneView(cs, id)
	if herr != nil {
		return nil, herr
	}
	return &herdrResult{Type: kind, Pane: p}, nil
}

// herdrWin names a window for a verb call.
func herdrWin(sess *Session, windowID string) herdrArgs {
	return herdrArgs{Session: sess.Name(), Window: windowID}
}

// Workspaces.

func (d *Daemon) herdrWorkspaceList(cs *connState, _ *herdrIn) (*herdrResult, *herdrError) {
	return &herdrResult{Type: "workspace_list", Workspaces: &d.buildHerdrView(cs).workspaces}, nil
}

func (d *Daemon) herdrWorkspaceGet(cs *connState, in *herdrIn) (*herdrResult, *herdrError) {
	return d.herdrWorkspaceInfo(cs, in.WorkspaceID)
}

// herdrWorkspaceInfo answers with the record of the workspace id names.
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

// herdrWorkspaceCreate is new-session: a session with one window in cwd, its
// display name the label. herdr's focus moves its client to the new
// workspace. tuios has no verb that moves an attached client to another
// session, so focus is taken and does nothing.
func (d *Daemon) herdrWorkspaceCreate(cs *connState, in *herdrIn) (*herdrResult, *herdrError) {
	if len(in.Env) > 0 {
		return nil, herdrErr("unsupported", "tuios does not set env for a new workspace. Leave env out")
	}
	return d.herdrNewSession(cs, herdrArgs{Cwd: in.Cwd}, in.label(), "workspace_create_failed", &herdrResult{Type: "workspace_created"})
}

// herdrNewSession runs new-session, names the session label when one is
// given, and answers res with the new session's workspace, tab and pane.
func (d *Daemon) herdrNewSession(cs *connState, args herdrArgs, label, fail string, res *herdrResult) (*herdrResult, *herdrError) {
	out, herr := d.herdrVerb(cs, "new-session", args, fail)
	if herr != nil {
		return nil, herr
	}
	return d.herdrLabelled(cs, herdrString(out, "session"), herdrString(out, "window_id"), label, fail, res)
}

// herdrLabelled names a new session label when one is given, and answers res
// with its workspace, tab and the pane of windowID.
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

// herdrCreated fills a create's answer: the workspace, the tab and the root
// pane a new window is in. A tab_created has no workspace.
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

// Tabs.

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

// herdrTabInfo answers with the record of the tab id names.
func (d *Daemon) herdrTabInfo(cs *connState, id string) (*herdrResult, *herdrError) {
	sess, ws, ierr := d.herdrFindTab(id)
	if ierr != nil {
		return nil, ierr.herdr()
	}
	v, herr := d.herdrSessionView(cs, sess)
	if herr != nil {
		return nil, herr
	}
	if t := v.tab(herdrTabID(sess.ID, ws)); t != nil {
		return &herdrResult{Type: "tab_info", Tab: t}, nil
	}
	return nil, herdrNotFound("tab", id).herdr()
}

// herdrTabCreate puts a window on the first workspace of the session that is
// not a tab yet, and names the workspace when a label is given.
func (d *Daemon) herdrTabCreate(cs *connState, in *herdrIn) (*herdrResult, *herdrError) {
	if len(in.Env) > 0 {
		return nil, herdrErr("unsupported", "tuios does not set env for a new tab. Leave env out")
	}
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
	// The name goes on first, so the tab_created event of the new window
	// carries it. A window that then fails to open takes the name back off.
	label := in.label()
	if label != "" {
		if _, herr := d.herdrVerb(cs, "set-workspace-name", herdrArgs{Session: sess.Name(), Workspace: ws, Name: &label}, fail); herr != nil {
			return nil, herr
		}
	}
	out, herr := d.herdrVerb(cs, "new-window", herdrArgs{Session: sess.Name(), Workspace: ws, Focus: &in.Focus, Cwd: in.Cwd}, fail)
	if herr != nil {
		if label != "" {
			_, _ = d.herdrVerb(cs, "set-workspace-name", herdrArgs{Session: sess.Name(), Workspace: ws, Name: ptr("")}, fail)
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

// herdrSessionOrFocused is the session a workspace id names, or with none
// the session herdr calls the active workspace.
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

// herdrTabVerb runs a verb on the workspace of a tab and answers with the
// tab's record.
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

// herdrTabMove is set-workspace-order. insert_index counts places in the
// session's tab list before the tab is taken out, as herdr's does.
func (d *Daemon) herdrTabMove(cs *connState, in *herdrIn) (*herdrResult, *herdrError) {
	if *in.InsertIndex < 0 {
		return nil, herdrErr("invalid_request", "invalid request: insert_index must not be negative")
	}
	sess, ws, ierr := d.herdrFindTab(in.TabID)
	if ierr != nil {
		return nil, ierr.herdr()
	}
	order := herdrWorkspaceOrder(sess.GetState())
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

// herdrTabClose closes every window on the workspace, as herdr closes every
// pane of the tab, and clears the workspace's name so it is not a tab any
// more.
func (d *Daemon) herdrTabClose(cs *connState, in *herdrIn) (*herdrResult, *herdrError) {
	sess, ws, ierr := d.herdrFindTab(in.TabID)
	if ierr != nil {
		return nil, ierr.herdr()
	}
	const fail = "tab_close_failed"
	st := sess.GetState()
	// The name goes first: clearing it is admin, as closing is, and a pane
	// refused here has closed nothing.
	if st.WorkspaceNames[ws] != "" {
		if _, herr := d.herdrVerb(cs, "set-workspace-name", herdrArgs{Session: sess.Name(), Workspace: ws, Name: ptr("")}, fail); herr != nil {
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
	// The workspace showing is always a tab. herdr shows another tab when
	// it closes the one showing, and so does this: the nearest one before
	// it in display order that holds a window, else the nearest after it.
	if st = sess.GetState(); st.CurrentWorkspace == ws {
		if next := herdrNeighbourTab(st, ws); next != 0 {
			if _, herr := d.herdrVerb(cs, "select-workspace", herdrArgs{Session: sess.Name(), Workspace: next}, fail); herr != nil {
				return nil, herr
			}
		}
	}
	return herdrAck, nil
}

// herdrNeighbourTab is the workspace to show when ws closes: the nearest
// one before it in display order that holds a window, else the nearest
// after it, else 0.
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

// Panes.

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

// herdrPaneCurrent is the pane caller_pane_id names, else the caller's own
// pane, else the focused one.
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

// herdrRead is capture-pane, in herdr's result. herdr's sources map onto
// tuios's: visible and detection read the screen, recent and
// recent_unwrapped the screen and scrollback. tuios does not rejoin
// soft-wrapped rows, so recent_unwrapped reads as recent does. A recent read
// is the last lines lines, 80 when lines is left out, and at most 1000.
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
		// One line more than asked says whether there was more to read.
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

// herdrSource is tuios's capture source for herdr's, "" for none.
func herdrSource(s string) string {
	switch s {
	case "visible", "detection":
		return "visible"
	case "recent", "recent_unwrapped":
		return "recent"
	}
	return ""
}

// herdrPaneSend is pane.send_text, pane.send_keys and pane.send_input: the
// text, then the keys.
func (d *Daemon) herdrPaneSend(cs *connState, in *herdrIn) (*herdrResult, *herdrError) {
	return d.herdrSend(cs, in.PaneID, in.Text, in.Keys, "pane_send_failed")
}

// herdrSend types text, then keys, into a pane: send-text with paste for the
// text, and send-keys for the keys. Each call is checked as that verb is, so
// a pane is held to its write grant and to typingRefusal. Every key is read
// before anything is sent, so a key tuios cannot send fails the call whole,
// as in herdr.
//
// herdr 0.9.3 writes the text raw. tuios sends it as a paste: control
// characters removed, and in bracketed paste delimiters when the pane's
// program has that mode on. Collie sends a reply as send_text and then
// send_keys Enter, and a reply of several lines written raw would run line by
// line.
func (d *Daemon) herdrSend(cs *connState, id, text string, keys []string, fail string) (*herdrResult, *herdrError) {
	runs, bad := herdrKeyRuns(keys)
	if bad != "" {
		return nil, herdrErr("invalid_key", "unsupported key "+bad)
	}
	sess, win, ierr := d.herdrFindPane(id)
	if ierr != nil {
		return nil, ierr.herdr()
	}
	// A tool that wraps its text in bracketed paste itself (terminal-browser
	// does for text of several lines) sends the markers as text. A paste
	// removes their escape bytes and would type the rest, so the markers
	// come off, and the paste puts its own on when the program wants them.
	if inner, ok := strings.CutPrefix(text, bracketedPasteStart); ok {
		text = strings.TrimSuffix(inner, bracketedPasteEnd)
	}
	if text != "" {
		args := herdrWin(sess, win.ID)
		args.Text, args.Paste = text, true
		if _, herr := d.herdrVerb(cs, "send-text", args, fail); herr != nil {
			return nil, herr
		}
	}
	for _, r := range runs {
		args := herdrWin(sess, win.ID)
		args.Keys, args.Literal = r.keys, r.literal
		if _, verr := d.callVerb(cs, "send-keys", args); verr != nil {
			if verr.Code == ErrVerbInvalidParams {
				return nil, herdrErr("invalid_key", verr.Message)
			}
			return nil, herdrFromVerb(verr, fail)
		}
	}
	return herdrAck, nil
}

// herdrPaneRename is set-window's name. A null label clears it, as in herdr.
func (d *Daemon) herdrPaneRename(cs *connState, in *herdrIn) (*herdrResult, *herdrError) {
	sess, win, ierr := d.herdrFindPane(in.PaneID)
	if ierr != nil {
		return nil, ierr.herdr()
	}
	args := herdrWin(sess, win.ID)
	args.Name = ptr(strings.TrimSpace(in.label()))
	if _, herr := d.herdrVerb(cs, "set-window", args, "pane_rename_failed"); herr != nil {
		return nil, herr
	}
	return d.herdrPaneResult(cs, "pane_info", in.PaneID)
}

func (d *Daemon) herdrPaneFocus(cs *connState, in *herdrIn) (*herdrResult, *herdrError) {
	return d.herdrFocusPane(cs, in.PaneID)
}

// herdrFocusPane is focus-window, which also shows the window's workspace.
func (d *Daemon) herdrFocusPane(cs *connState, id string) (*herdrResult, *herdrError) {
	sess, win, ierr := d.herdrFindPane(id)
	if ierr != nil {
		return nil, ierr.herdr()
	}
	if _, herr := d.herdrVerb(cs, "focus-window", herdrWin(sess, win.ID), "pane_focus_failed"); herr != nil {
		return nil, herr
	}
	return d.herdrPaneResult(cs, "pane_info", id)
}

// herdrPaneSplit is split-window when a client is attached and tiles the
// session. Without one, tuios has no split to cut, and the new pane is a
// window on the same workspace, which a client tiles when it attaches.
func (d *Daemon) herdrPaneSplit(cs *connState, in *herdrIn) (*herdrResult, *herdrError) {
	dir := "vertical"
	switch in.Direction {
	case "right":
	case "down":
		dir = "horizontal"
	default:
		return nil, herdrErr("invalid_request", "invalid request: unknown variant `"+in.Direction+"`, expected `right` or `down`")
	}
	if len(in.Env) > 0 {
		return nil, herdrErr("unsupported", "tuios does not set env for a new pane. Leave env out")
	}
	target := in.TargetPaneID
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
	const fail = "pane_split_failed"
	newID, split := "", false
	if in.Cwd == "" && d.findTUIClient(sess.ID) != nil {
		args := herdrWin(sess, win.ID)
		args.Direction = dir
		out, verr := d.callVerb(cs, "split-window", args)
		if verr == nil {
			// The split ran. A new window is made only when it did not:
			// a split whose pane is late to reach the state is reported,
			// never answered with a second pane.
			split = true
			if newID = herdrString(out.(map[string]any), "window_id"); newID == "" {
				return nil, herdrErr(fail, "the split ran, and the new pane has not reached the daemon yet. Run pane list to find it")
			}
		} else if verr.Code != ErrVerbNeedsClient && verr.Code != ErrVerbCommandFailed {
			return nil, herdrFromVerb(verr, fail)
		}
	}
	if !split {
		out, herr := d.herdrVerb(cs, "new-window", herdrArgs{Session: sess.Name(), Workspace: max(win.Workspace, 1), Focus: &in.Focus, Cwd: in.Cwd}, fail)
		if herr != nil {
			return nil, herr
		}
		newID = herdrString(out, "window_id")
	}
	return d.herdrPaneResult(cs, "pane_info", herdrPaneID(sess.ID, newID))
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

// herdrPaneWaitForOutput is wait-for window-output: a substring is matched
// as the text itself, a regex as it is.
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
			matched = ptr(line)
		}
	}
	return &herdrResult{Type: "output_matched", PaneID: res.Read.PaneID, Revision: &res.Read.Revision, MatchedLine: matched, Read: res.Read}, nil
}

// Agents.

func (d *Daemon) herdrAgentList(cs *connState, _ *herdrIn) (*herdrResult, *herdrError) {
	return &herdrResult{Type: "agent_list", Agents: &d.buildHerdrView(cs).agents}, nil
}

// herdrAgentTarget is the pane an agent target names: a pane id, a
// terminal id, or the one agent pane with that label or agent name.
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

// herdrAgentResult answers with the agent record of the pane id names,
// which must hold an agent.
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

// herdrAgent runs an agent method on the pane in.Target names.
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

// herdrStatesFor is the tuios agent states a herdr status list waits for.
func herdrStatesFor(statuses []string) (string, *herdrError) {
	var states []string
	for _, s := range statuses {
		switch s {
		case "idle":
			states = append(states, string(AgentStateIdle))
		case "working":
			states = append(states, string(AgentStateWorking))
		case "blocked":
			states = append(states, string(AgentStateNeedsInput))
		case "done":
			states = append(states, string(AgentStateDone), string(AgentStateErrored))
		case "unknown":
			states = append(states, string(AgentStateUnknown))
		default:
			return "", herdrErr("invalid_request", "invalid request: unknown variant `"+s+"`, expected one of `idle`, `working`, `blocked`, `done`, `unknown`")
		}
	}
	return strings.Join(states, ","), nil
}

// herdrWaitAgent is wait-for agent-state on one pane.
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
	_, herr = d.herdrVerb(cs, "wait-for", args, "wait_failed")
	return herr
}

func (d *Daemon) herdrAgentWait(cs *connState, in *herdrIn) (*herdrResult, *herdrError) {
	return d.herdrAgent(cs, in, func(id string) (*herdrResult, *herdrError) {
		if herr := d.herdrWaitAgent(cs, id, in.Until, in.TimeoutMS); herr != nil {
			return nil, herr
		}
		return d.herdrAgentResult(cs, "agent_info", id)
	})
}

// herdrAgentPrompt types a prompt into an agent at rest and presses Enter,
// as herdr does. An agent that works or waits on a prompt is refused with
// agent_not_idle, and nothing is typed.
func (d *Daemon) herdrAgentPrompt(cs *connState, in *herdrIn) (*herdrResult, *herdrError) {
	return d.herdrAgent(cs, in, func(id string) (*herdrResult, *herdrError) {
		res, herr := d.herdrAgentResult(cs, "agent_prompted", id)
		if herr != nil {
			return nil, herr
		}
		if s := res.Agent.AgentStatus; s == "working" || s == "blocked" {
			return nil, herdrErr("agent_not_idle", "agent "+res.Agent.PaneID+" is "+s+". Nothing was typed")
		}
		// send-text submit is the prompt path ask-agent types with: one
		// paste, a wait for it to be taken in, and the harness's submit key.
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

// Worktrees.

// herdrWorktreeRecord is herdr's WorktreeInfo.
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

// herdrRepoDir is the directory a worktree call names: cwd, or the directory
// of the workspace's session.
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

// herdrWorktreeList runs git worktree list in the repository. It reads git,
// not a session, and needs what list-worktrees needs.
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

// herdrWorktreeOf is the record of one checkout.
func herdrWorktreeOf(e worktree.Entry, open map[string]string) herdrWorktreeRecord {
	r := herdrWorktreeRecord{
		Path: e.Path, IsBare: e.Bare, IsDetached: e.Detached, IsPrunable: e.Prunable,
		IsLinkedWorktree: e.Linked, OpenWorkspaceID: open[filepath.Clean(e.Path)], Label: filepath.Base(e.Path),
	}
	if e.Branch != "" {
		r.Branch, r.Label = ptr(e.Branch), e.Branch
	}
	return r
}

// herdrOpenCheckouts maps each checkout a session the caller may read shows
// to its workspace id: the checkout of the session's worktree record, else
// the one its directory is in.
func (d *Daemon) herdrOpenCheckouts(cs *connState) map[string]string {
	open := make(map[string]string)
	for _, w := range d.buildHerdrView(cs).workspaces {
		if w.Worktree == nil || w.Worktree.CheckoutPath == "" {
			continue
		}
		if path := filepath.Clean(w.Worktree.CheckoutPath); open[path] == "" {
			open[path] = w.WorkspaceID
		}
	}
	return open
}

// herdrWorktreeCreate is new-worktree. tuios puts the checkout under its own
// worktree directory, so a path is refused rather than ignored.
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
	wt := &herdrWorktreeRecord{Path: herdrString(out, "path"), Branch: ptr(in.Branch), IsLinkedWorktree: true, Label: in.Branch}
	return d.herdrLabelled(cs, herdrString(out, "session"), herdrString(out, "window_id"), in.label(), fail, &herdrResult{Type: "worktree_created", Worktree: wt})
}

// herdrWorktreeOpen shows a checkout that exists: the session that already
// shows it, or a new session in it.
func (d *Daemon) herdrWorktreeOpen(cs *connState, in *herdrIn) (*herdrResult, *herdrError) {
	// It runs git before any verb does, so the caller is checked first, as
	// for worktree.list: a refused caller learns nothing of the repository.
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
	for i, e := range entries {
		if (in.Path != "" && filepath.Clean(e.Path) == filepath.Clean(in.Path)) || (in.Path == "" && in.Branch != "" && e.Branch == in.Branch) {
			entry = &entries[i]
		}
	}
	if entry == nil {
		return nil, herdrErr("worktree_not_found", "no worktree of this repository at "+echoName(in.Path+in.Branch))
	}
	open := d.herdrOpenCheckouts(cs)
	rec := herdrWorktreeOf(*entry, open)
	res := &herdrResult{Type: "worktree_opened", Worktree: &rec, AlreadyOpen: ptr(false)}
	if id := open[filepath.Clean(entry.Path)]; id != "" {
		sess, ierr := d.herdrFindSession(id)
		if ierr != nil {
			return nil, ierr.herdr()
		}
		st := sess.GetState()
		root := st.FocusedWindowID
		if root == "" && len(st.Windows) > 0 {
			root = st.Windows[0].ID
		}
		res.AlreadyOpen = ptr(true)
		return d.herdrCreated(cs, sess, root, res)
	}
	args := herdrArgs{Cwd: entry.Path}
	if name := herdrWorktreeSessionName(dir, *entry); d.manager.GetSession(name) == nil {
		args.Name = &name
	}
	return d.herdrNewSession(cs, args, in.label(), "worktree_open_failed", res)
}

// herdrWorktreeSessionName is the session name new-worktree would give the
// checkout: <repo>-<branch>, or the directory's name for a detached one.
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

// herdrWorktreeRemove is remove-worktree. herdr removes the worktree of a
// workspace that shows one; tuios removes a worktree session's.
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
