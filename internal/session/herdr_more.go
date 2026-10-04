package session

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

// herdr's methods beyond the ones herdr_panes.go answers: resizing a pane,
// reloading the config, and the rest that herdr plugins call. Each is held to
// the grants of the tuios verb that does the same work, as in herdr_panes.go.

// herdrResize is herdr's PaneResizeResult.
type herdrResize struct {
	Changed       bool        `json:"changed"`
	Reason        string      `json:"reason,omitempty"`
	PaneID        string      `json:"pane_id"`
	FocusedPaneID string      `json:"focused_pane_id"`
	Layout        herdrLayout `json:"layout"`
}

// herdrResizeDefault and herdrResizeMax are herdr's default and largest
// share of the tab a resize moves a border by.
const (
	herdrResizeDefault = 0.05
	herdrResizeMax     = 0.5
)

// herdrPaneResize moves one border of a pane, as herdr's pane.resize does:
// right and down move a border right or down, left and up move it left or
// up. The border is the one on that side of the pane, or the other one when
// the pane is at the edge of the tab there. The layout lives in the client,
// so the move is routed to the attached client, as a swap is. It needs what
// set-layout needs.
func (d *Daemon) herdrPaneResize(cs *connState, in *herdrIn) (*herdrResult, *herdrError) {
	if herr := herdrDirection(in.Direction); herr != nil {
		return nil, herr
	}
	site, herr := d.herdrSiteOf(cs, in.PaneID)
	if herr != nil {
		return nil, herr
	}
	if herr := d.herdrAdmit(cs, "set-layout", site.sess); herr != nil {
		return nil, herr
	}
	amount := herdrResizeDefault
	if in.Amount != nil && !math.IsNaN(*in.Amount) && !math.IsInf(*in.Amount, 0) {
		amount = min(math.Abs(*in.Amount), herdrResizeMax)
	}
	res := &herdrResize{PaneID: site.pane.PaneID}
	if amount > 0 {
		data, herr := d.herdrRouteClientData(site.sess, "resize_window", "pane_resize_failed",
			site.win.ID, in.Direction, strconv.FormatFloat(amount, 'f', -1, 64))
		if herr != nil {
			return nil, herr
		}
		res.Changed, _ = data["changed"].(bool)
	}
	if !res.Changed {
		res.Reason = "unchanged"
	}
	if after, herr := d.herdrSiteOf(cs, site.pane.PaneID); herr == nil {
		site = after
	}
	res.FocusedPaneID, res.Layout = site.layout.FocusedPaneID, site.layout
	return &herdrResult{Type: "pane_resize", Resize: res}, nil
}

// herdrRouteClientData is herdrRouteClient for a command whose answer
// carries data.
func (d *Daemon) herdrRouteClientData(sess *Session, command, fail string, args ...string) (map[string]any, *herdrError) {
	tui := d.findTUIClient(sess.ID)
	if tui == nil {
		// routeTape words the no_client error.
		return nil, herdrFromVerb(d.routeTape(sess, command, "", args), fail)
	}
	res, err := d.routeToTUISync(tui, uuid.New().String(), &RemoteCommandPayload{
		CommandType: command, TapeArgs: args,
	}, routedVerbTimeout)
	if err != nil {
		return nil, herdrErr(fail, err.Error())
	}
	if !res.Success {
		msg := res.Message
		if msg == "" {
			msg = "the attached client refused the request"
		}
		return nil, herdrErr(fail, msg)
	}
	return res.Data, nil
}

// herdrReloadConfig answers server.reload_config. herdr reads its own
// config.toml again. tuios does not read that file, and it reads its own
// config.toml when the file changes, so there is nothing to reload. Tools
// call it after they write a setting into herdr's file (terminal-browser
// writes kitty_graphics), and go on when it succeeds.
func (d *Daemon) herdrReloadConfig(_ *connState, _ *herdrIn) (*herdrResult, *herdrError) {
	return &herdrResult{
		Type:        "config_reload",
		Status:      "applied",
		Diagnostics: &[]string{"tuios does not read herdr's config.toml, so nothing was reloaded. tuios reads its own config.toml when the file changes"},
	}, nil
}

// herdrAgentRename names the agent a target finds, as herdr's agent.rename
// does: the pane takes the name, which agent.get and the other agent methods
// then find it by. No name clears it. It needs what set-window needs.
func (d *Daemon) herdrAgentRename(cs *connState, in *herdrIn) (*herdrResult, *herdrError) {
	id, herr := d.herdrAgentTarget(cs, in.Target)
	if herr != nil {
		return nil, herr
	}
	if in.Name != "" && !herdrAgentNameOK(in.Name) {
		return nil, herdrErr("invalid_agent_name", "agent name must start with a lowercase letter and contain only lowercase letters, digits, '-' or '_' (1-32 characters)")
	}
	if _, herr := d.herdrAgentResult(cs, "agent_info", id); herr != nil {
		return nil, herdrErr("agent_not_found", "agent target does not currently host an agent")
	}
	sess, win, ierr := d.herdrFindPane(id)
	if ierr != nil {
		return nil, ierr.herdr()
	}
	paneID := herdrPaneID(sess.ID, win.ID)
	if in.Name != "" {
		for _, p := range d.buildHerdrView(cs).panes {
			if p.Agent != "" && p.Label == in.Name && p.PaneID != paneID {
				return nil, herdrErr("agent_name_taken", "agent name "+in.Name+" is already used; candidates: pane_id="+p.PaneID)
			}
		}
	}
	args := herdrWin(sess, win.ID)
	args.Name = ptr(in.Name)
	if _, herr := d.herdrVerb(cs, "set-window", args, "agent_rename_failed"); herr != nil {
		return nil, herr
	}
	return d.herdrAgentResult(cs, "agent_info", paneID)
}

// herdrAgentExplain says why tuios sees an agent in a pane, or does not:
// what explain-agent-detect reports, under herdr's explain field. herdr's
// explain is its own detector's report, a free JSON value, so a tool that
// reads it shows it and does not take it apart.
func (d *Daemon) herdrAgentExplain(cs *connState, in *herdrIn) (*herdrResult, *herdrError) {
	id, herr := d.herdrAgentTarget(cs, in.Target)
	if herr != nil {
		return nil, herr
	}
	sess, win, ierr := d.herdrFindPane(id)
	if ierr != nil {
		return nil, ierr.herdr()
	}
	out, herr := d.herdrVerb(cs, "explain-agent-detect", herdrWin(sess, win.ID), "agent_explain_failed")
	if herr != nil {
		return nil, herr
	}
	return &herdrResult{Type: "agent_explain", Explain: out}, nil
}

// herdrDismissStale answers release_notes.dismiss and
// product_announcement.dismiss. herdr shows its own release notes and
// announcements, and answers a dismiss with a stale error when none is
// current. tuios shows none of herdr's, so none is ever current.
func (d *Daemon) herdrDismissReleaseNotes(_ *connState, _ *herdrIn) (*herdrResult, *herdrError) {
	return nil, herdrErr("stale_release_notes", "the release notes are no longer current")
}

func (d *Daemon) herdrDismissAnnouncement(_ *connState, _ *herdrIn) (*herdrResult, *herdrError) {
	return nil, herdrErr("stale_announcement", "the product announcement is no longer current")
}

// herdrMoveDest is herdr's PaneMoveDestination, tagged by type.
type herdrMoveDest struct {
	Type         string  `json:"type"`
	TabID        string  `json:"tab_id"`
	TargetPaneID string  `json:"target_pane_id"`
	Split        string  `json:"split"`
	WorkspaceID  string  `json:"workspace_id"`
	Label        *string `json:"label"`
	TabLabel     *string `json:"tab_label"`
}

// herdrMove is herdr's PaneMoveResult.
type herdrMove struct {
	Changed             bool          `json:"changed"`
	Reason              string        `json:"reason,omitempty"`
	PreviousPaneID      string        `json:"previous_pane_id"`
	PreviousWorkspaceID string        `json:"previous_workspace_id"`
	PreviousTabID       string        `json:"previous_tab_id"`
	Pane                herdrPaneInfo `json:"pane"`
	SourceLayout        *herdrLayout  `json:"source_layout,omitempty"`
	TargetLayout        herdrLayout   `json:"target_layout"`
	CreatedTab          *herdrTab     `json:"created_tab,omitempty"`
	ClosedTabID         string        `json:"closed_tab_id,omitempty"`
	FocusedPaneID       string        `json:"focused_pane_id"`
}

// herdrPaneMove moves a pane to another tab of its workspace, or to a new
// tab there, as move-window does, and needs what move-window needs. A tuios
// window stays in its session, so a tab of another workspace and a new
// workspace fail with unsupported. tuios places the pane in the tab's layout
// itself: target_pane_id, split and ratio are not followed. The pane keeps
// its id.
func (d *Daemon) herdrPaneMove(cs *connState, in *herdrIn) (*herdrResult, *herdrError) {
	dest := in.Destination
	switch dest.Type {
	case "tab", "new_tab", "new_workspace":
	default:
		return nil, herdrErr("invalid_request", "invalid request: unknown variant `"+echoName(dest.Type)+"`, expected one of `tab`, `new_tab`, `new_workspace`")
	}
	site, herr := d.herdrSiteOf(cs, in.PaneID)
	if herr != nil {
		return nil, herr
	}
	res := &herdrMove{
		PreviousPaneID: site.pane.PaneID, PreviousWorkspaceID: site.pane.WorkspaceID, PreviousTabID: site.pane.TabID,
	}
	st := site.sess.GetState()
	ws := 0
	switch dest.Type {
	case "new_workspace":
		return nil, herdrErr("unsupported", "tuios keeps a pane in its own workspace, so it cannot move one to a new workspace. Move it to a tab of its workspace")
	case "tab":
		if dest.TabID == "" || dest.Split == "" {
			return nil, herdrErr("invalid_request", "invalid request: missing field `"+map[bool]string{true: "tab_id", false: "split"}[dest.TabID == ""]+"`")
		}
		if dest.Split != "right" && dest.Split != "down" {
			return nil, herdrErr("invalid_request", "invalid request: unknown variant `"+echoName(dest.Split)+"`, expected `right` or `down`")
		}
		sess, n, ierr := d.herdrFindTab(dest.TabID)
		if ierr != nil {
			return nil, ierr.herdr()
		}
		if sess != site.sess {
			return nil, herdrErr("unsupported", "tuios keeps a pane in its own workspace, so it cannot move one to a tab of another workspace")
		}
		ws = n
	case "new_tab":
		if dest.WorkspaceID != "" {
			sess, ierr := d.herdrFindSession(dest.WorkspaceID)
			if ierr != nil {
				return nil, ierr.herdr()
			}
			if sess != site.sess {
				return nil, herdrErr("unsupported", "tuios keeps a pane in its own workspace, so it cannot move one to a new tab of another workspace")
			}
		}
		for _, n := range herdrWorkspaceOrder(st) {
			if !herdrListedWorkspace(st, n) {
				ws = n
				break
			}
		}
		if ws == 0 {
			return nil, herdrErr("pane_move_failed", fmt.Sprintf("every one of the %d tabs of this workspace is in use. Close a tab first", st.workspaceBound()))
		}
	}
	if herr := d.herdrAdmit(cs, "move-window", site.sess); herr != nil {
		return nil, herr
	}
	switch {
	case ws == site.win.Workspace:
		res.Reason = "same_tab"
	case herdrZoomedIn(st, site.win.Workspace) != "":
		res.Reason = "zoomed_tab"
	}
	if res.Reason == "" {
		const fail = "pane_move_failed"
		label := ""
		if dest.Type == "new_tab" && dest.Label != nil {
			label = strings.TrimSpace(*dest.Label)
		}
		if label != "" {
			if _, herr := d.herdrVerb(cs, "set-workspace-name", herdrArgs{Session: site.sess.Name(), Workspace: ws, Name: &label}, fail); herr != nil {
				return nil, herr
			}
		}
		args := herdrWin(site.sess, site.win.ID)
		args.Workspace = ws
		if _, herr := d.herdrVerb(cs, "move-window", args, fail); herr != nil {
			return nil, herr
		}
		res.Changed = true
		if in.Focus {
			if _, herr := d.herdrFocusPane(cs, site.pane.PaneID); herr != nil {
				return nil, herr
			}
		}
	}
	v, herr := d.herdrSessionView(cs, site.sess)
	if herr != nil {
		return nil, herr
	}
	p := v.pane(site.pane.PaneID)
	if p == nil {
		return nil, herdrErr("pane_move_failed", "source pane could not be moved")
	}
	res.Pane = *p
	for i := range v.layouts {
		switch v.layouts[i].TabID {
		case p.TabID:
			res.TargetLayout = v.layouts[i]
			res.FocusedPaneID = v.layouts[i].FocusedPaneID
		case res.PreviousTabID:
			if res.Changed {
				res.SourceLayout = &v.layouts[i]
			}
		}
	}
	if res.Changed {
		if dest.Type == "new_tab" {
			res.CreatedTab = v.tab(p.TabID)
		}
		if v.tab(res.PreviousTabID) == nil {
			res.ClosedTabID = res.PreviousTabID
		}
	}
	if v.focusedPane != "" {
		res.FocusedPaneID = v.focusedPane
	}
	return &herdrResult{Type: "pane_move", MoveResult: res}, nil
}

// herdrClientTitleMax bounds a client title, in runes.
const herdrClientTitleMax = 256

// herdrClientTitle sets or clears the title of the terminal a tuios client
// runs in, as herdr's client.window_title.set and .clear do for herdr's
// foreground client. The client is the one that shows the caller's session,
// else the one that shows the session herdr calls the active workspace.
// With none, nothing changes and the reason is no_foreground_client, as in
// herdr. The title is what the person sees on their own window, so a pane
// needs what run-command needs to set it. Control characters are taken out.
func herdrClientTitle(clear bool) func(*Daemon, *connState, *herdrIn) (*herdrResult, *herdrError) {
	return func(d *Daemon, cs *connState, in *herdrIn) (*herdrResult, *herdrError) {
		var sess *Session
		if fromPane, window := d.peerPane(cs); fromPane {
			sess = d.sessionHoldingWindow(window)
		}
		if sess == nil || d.findTUIClient(sess.ID) == nil {
			sess = d.herdrFocusedSession(d.herdrSessions(cs))
		}
		if sess == nil || d.findTUIClient(sess.ID) == nil {
			return &herdrResult{Type: "client_window_title", Changed: ptr(false), Reason: "no_foreground_client"}, nil
		}
		if herr := d.herdrAdmit(cs, "run-command", sess); herr != nil {
			return nil, herr
		}
		var args []string
		reason := "cleared"
		if !clear {
			title := []rune(strings.Map(func(r rune) rune {
				if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
					return -1
				}
				return r
			}, in.Title))
			args, reason = []string{string(title[:min(len(title), herdrClientTitleMax)])}, "set"
		}
		data, herr := d.herdrRouteClientData(sess, "set_client_title", "client_window_title_failed", args...)
		if herr != nil {
			return nil, herr
		}
		changed, _ := data["changed"].(bool)
		return &herdrResult{Type: "client_window_title", Changed: &changed, Reason: reason}, nil
	}
}

// herdr's limits on metadata tokens (src/app/api_helpers.rs).
const (
	herdrMetaKeysPerCall   = 16
	herdrMetaKeysPerTarget = 32
	herdrMetaKeyLen        = 32
	herdrMetaValueLen      = 80
	herdrMetaSourceLen     = 80
	herdrMetaSeqSources    = 32
	herdrMetaMaxTTLMS      = 86_400_000
)

// herdrWsMeta is the tokens tools report on each session through
// workspace.report_metadata, kept in the daemon's memory as herdr keeps
// them: a token is keyed by its name alone, a later report replaces it
// whoever sends it, null clears it, and a ttl drops it when it runs out.
// workspace.list, workspace.get and the snapshot show them under tokens.
type herdrWsMeta struct {
	mu       sync.Mutex
	sessions map[string]*herdrWsTokens
}

type herdrWsTokens struct {
	values map[string]herdrWsToken
	seqs   map[string]uint64
}

type herdrWsToken struct {
	value   string
	expires time.Time
}

// tokens is the live tokens of a session, nil for none.
func (m *herdrWsMeta) tokens(session string, now time.Time) map[string]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.sessions[session]
	if t == nil {
		return nil
	}
	var out map[string]string
	for k, tok := range t.values {
		if !tok.expires.IsZero() && !now.Before(tok.expires) {
			delete(t.values, k)
			continue
		}
		if out == nil {
			out = map[string]string{}
		}
		out[k] = tok.value
	}
	return out
}

// forget drops a session's tokens, when the session closes.
func (m *herdrWsMeta) forget(session string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, session)
}

// herdrWorkspaceReportMetadata applies one workspace.report_metadata, with
// herdr's checks and words. The tokens are shown to everyone who reads the
// session, so a pane needs what set-session-accent needs to report them.
func (d *Daemon) herdrWorkspaceReportMetadata(cs *connState, in *herdrIn) (*herdrResult, *herdrError) {
	sess, ierr := d.herdrFindSession(in.WorkspaceID)
	if ierr != nil {
		return nil, ierr.herdr()
	}
	source := strings.TrimSpace(in.Source)
	switch {
	case source == "":
		return nil, herdrErr("invalid_metadata_source", "metadata source must not be empty")
	case utf8.RuneCountInString(source) > herdrMetaSourceLen:
		return nil, herdrErr("invalid_metadata_source", "metadata source must be 80 characters or fewer")
	case strings.ContainsFunc(source, func(r rune) bool {
		return !(r < 0x80 && (unicode.IsLetter(r) || unicode.IsDigit(r)) || strings.ContainsRune(":._-", r))
	}):
		return nil, herdrErr("invalid_metadata_source", "metadata source may contain only ASCII letters, digits, colon, dot, underscore, and hyphen")
	}
	var ttl time.Duration
	if in.TTLMS != nil {
		if *in.TTLMS < 1 {
			return nil, herdrErr("invalid_metadata_ttl", "metadata ttl_ms must be at least 1")
		}
		if *in.TTLMS > herdrMetaMaxTTLMS {
			return nil, herdrErr("invalid_metadata_ttl", "metadata ttl_ms must be 86400000 or less")
		}
		ttl = time.Duration(*in.TTLMS) * time.Millisecond
	}
	if len(in.Tokens) == 0 {
		return nil, herdrErr("invalid_metadata_token", "missing token to set or clear")
	}
	if len(in.Tokens) > herdrMetaKeysPerCall {
		return nil, herdrErr("invalid_metadata_token", fmt.Sprintf("a metadata report may update at most %d tokens", herdrMetaKeysPerCall))
	}
	patch := make(map[string]*string, len(in.Tokens))
	for k, v := range in.Tokens {
		if k == "" || len(k) > herdrMetaKeyLen || strings.ContainsFunc(k, func(r rune) bool {
			return !(r < 0x80 && (unicode.IsLetter(r) || unicode.IsDigit(r)) || r == '_' || r == '-')
		}) {
			return nil, herdrErr("invalid_metadata_token", "invalid metadata token key: "+echoName(k))
		}
		if v != nil {
			clean := []rune(strings.Map(func(r rune) rune {
				if unicode.IsControl(r) {
					return -1
				}
				return r
			}, strings.TrimSpace(*v)))
			s := strings.TrimSpace(string(clean[:min(len(clean), herdrMetaValueLen)]))
			if s == "" {
				v = nil
			} else {
				v = &s
			}
		}
		patch[k] = v
	}
	if herr := d.herdrAdmit(cs, "set-session-accent", sess); herr != nil {
		return nil, herr
	}
	m := &d.herdrWorkspaceMeta
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sessions == nil {
		m.sessions = map[string]*herdrWsTokens{}
	}
	t := m.sessions[sess.ID]
	if t == nil {
		t = &herdrWsTokens{values: map[string]herdrWsToken{}, seqs: map[string]uint64{}}
		m.sessions[sess.ID] = t
	}
	if in.Seq != nil {
		last, seen := t.seqs[source]
		if seen && *in.Seq <= last {
			return herdrAck, nil
		}
		if !seen && len(t.seqs) >= herdrMetaSeqSources {
			return nil, herdrErr("metadata_sequence_source_limit", fmt.Sprintf("workspace metadata may track at most %d sequenced sources", herdrMetaSeqSources))
		}
	}
	now := time.Now()
	count := 0
	for k, tok := range t.values {
		if tok.expires.IsZero() || now.Before(tok.expires) {
			if v, ok := patch[k]; !ok || v != nil {
				count++
			}
		}
	}
	for k, v := range patch {
		if _, held := t.values[k]; !held && v != nil {
			count++
		}
	}
	if count > herdrMetaKeysPerTarget {
		return nil, herdrErr("metadata_token_limit", fmt.Sprintf("workspace metadata may contain at most %d tokens", herdrMetaKeysPerTarget))
	}
	if in.Seq != nil {
		t.seqs[source] = *in.Seq
	}
	var expires time.Time
	if ttl > 0 {
		expires = now.Add(ttl)
	}
	for k, v := range patch {
		if v == nil {
			delete(t.values, k)
		} else {
			t.values[k] = herdrWsToken{value: *v, expires: expires}
		}
	}
	return herdrAck, nil
}

// herdrManifestInfo is herdr's AgentManifestInfo.
type herdrManifestInfo struct {
	Agent                        string `json:"agent"`
	Source                       string `json:"source"`
	SourceKind                   string `json:"source_kind"`
	LocalOverrideShadowingRemote bool   `json:"local_override_shadowing_remote"`
}

// herdrAgentManifests answers server.agent_manifests with tuios's harness
// manifests: the agents tuios knows, each under herdr's name for it, from
// the bundled set or from the person's own file. tuios does not fetch
// manifests from the network, so there is no remote check to report.
func (d *Daemon) herdrAgentManifests(_ *connState, _ *herdrIn) (*herdrResult, *herdrError) {
	out := []herdrManifestInfo{}
	if reg := d.agentMatcher.registry; reg != nil {
		for _, id := range reg.IDs() {
			m := reg.Lookup(id)
			if m == nil {
				continue
			}
			info := herdrManifestInfo{Agent: herdrAgentLabel(id), Source: "bundled", SourceKind: "bundled"}
			if src, _ := m.Source(); src != "" && src != "bundled" {
				info.Source, info.SourceKind = src, "local override"
			}
			out = append(out, info)
		}
	}
	return &herdrResult{Type: "agent_manifest_status", Manifests: &out}, nil
}
