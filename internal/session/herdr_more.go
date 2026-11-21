package session

import (
	"fmt"
	"math"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

type herdrResize struct {
	Changed       bool        `json:"changed"`
	Reason        string      `json:"reason,omitempty"`
	PaneID        string      `json:"pane_id"`
	FocusedPaneID string      `json:"focused_pane_id"`
	Layout        herdrLayout `json:"layout"`
}

const (
	herdrResizeDefault = 0.05
	herdrResizeMax     = 0.5
)

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
		changed, verr := d.resizePane(site.sess, site.win, in.Direction, amount)
		if verr != nil {
			return nil, herdrFromVerb(verr, "pane_resize_failed")
		}
		res.Changed = changed
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

func (d *Daemon) herdrRouteClientData(sess *Session, command, fail string, args ...string) (map[string]any, *herdrError) {
	data, verr := d.routeData(sess, command, args)
	if verr != nil {
		return nil, herdrFromVerb(verr, fail)
	}
	return data, nil
}

func (d *Daemon) herdrReloadConfig(_ *connState, _ *herdrIn) (*herdrResult, *herdrError) {
	return &herdrResult{
		Type:        "config_reload",
		Status:      "applied",
		Diagnostics: &[]string{"tuios does not read herdr's config.toml, so nothing was reloaded. tuios reads its own config.toml when the file changes"},
	}, nil
}

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
	args.Name = new(in.Name)
	if _, herr := d.herdrVerb(cs, "set-window", args, "agent_rename_failed"); herr != nil {
		return nil, herr
	}
	return d.herdrAgentResult(cs, "agent_info", paneID)
}

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

func (d *Daemon) herdrDismissReleaseNotes(_ *connState, _ *herdrIn) (*herdrResult, *herdrError) {
	return nil, herdrErr("stale_release_notes", "the release notes are no longer current")
}

func (d *Daemon) herdrDismissAnnouncement(_ *connState, _ *herdrIn) (*herdrResult, *herdrError) {
	return nil, herdrErr("stale_announcement", "the product announcement is no longer current")
}

type herdrMoveDest struct {
	Type         string  `json:"type"`
	TabID        string  `json:"tab_id"`
	TargetPaneID string  `json:"target_pane_id"`
	Split        string  `json:"split"`
	WorkspaceID  string  `json:"workspace_id"`
	Label        *string `json:"label"`
	TabLabel     *string `json:"tab_label"`
}

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
	to := site.sess
	ws := 0
	switch dest.Type {
	case "new_workspace":
		return nil, herdrErr("unsupported", "tuios cannot make a new workspace for a pane. Make the workspace first, then move the pane to a new tab there")
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
		to, ws = sess, n
	case "new_tab":
		if dest.WorkspaceID != "" {
			sess, ierr := d.herdrFindSession(dest.WorkspaceID)
			if ierr != nil {
				return nil, ierr.herdr()
			}
			to = sess
		}
		toState := to.GetState()
		for _, n := range herdrWorkspaceOrder(toState) {
			if !herdrListedWorkspace(toState, n) {
				ws = n
				break
			}
		}
		if ws == 0 {
			return nil, herdrErr("pane_move_failed", fmt.Sprintf("every one of the %d tabs of this workspace is in use. Close a tab first", toState.workspaceBound()))
		}
	}
	across := to != site.sess
	if across {
		if why := movableRefusal(site.win); why != "" {
			return nil, herdrErr("unsupported", "the pane cannot move to another workspace: "+why)
		}
	}
	if herr := d.herdrAdmit(cs, "move-window", site.sess); herr != nil {
		return nil, herr
	}
	if across {
		if herr := d.herdrAdmit(cs, "move-window", to); herr != nil {
			return nil, herr
		}
	}
	switch {
	case !across && ws == site.win.Workspace:
		res.Reason = "same_tab"
	case herdrZoomedIn(st, site.win.Workspace) != "":
		res.Reason = "zoomed_tab"
	}
	paneID := site.pane.PaneID
	if res.Reason == "" {
		const fail = "pane_move_failed"
		label := ""
		if dest.Type == "new_tab" && dest.Label != nil {
			label = strings.TrimSpace(*dest.Label)
		}
		if label != "" {
			if _, herr := d.herdrVerb(cs, "set-workspace-name", herdrArgs{Session: to.Name(), Workspace: ws, Name: &label}, fail); herr != nil {
				return nil, herr
			}
		}
		args := herdrWin(site.sess, site.win.ID)
		args.Workspace = ws
		if across {
			args.ToSession = to.Name()
		} else if dest.TargetPaneID != "" {
			if _, tw, ierr := d.herdrFindPane(dest.TargetPaneID); ierr == nil && tw.Workspace == ws {
				args.Workspace, args.Beside, args.Side = 0, tw.ID, dest.Split
			}
		}
		if _, herr := d.herdrVerb(cs, "move-window", args, fail); herr != nil {
			return nil, herr
		}
		res.Changed = true
		if across {
			paneID = herdrPaneID(to.ID, site.win.ID)
		}
		if in.Focus {
			if _, herr := d.herdrFocusPane(cs, paneID); herr != nil {
				return nil, herr
			}
		}
	}
	v, herr := d.herdrSessionView(cs, to)
	if herr != nil {
		return nil, herr
	}
	p := v.pane(paneID)
	if p == nil {
		return nil, herdrErr("pane_move_failed", "source pane could not be moved")
	}
	res.Pane = *p
	for i := range v.layouts {
		if v.layouts[i].TabID == p.TabID {
			res.TargetLayout = v.layouts[i]
			res.FocusedPaneID = v.layouts[i].FocusedPaneID
		}
	}
	if res.Changed {
		sv := v
		if across {
			if sv, herr = d.herdrSessionView(cs, site.sess); herr != nil {
				return nil, herr
			}
		}
		for i := range sv.layouts {
			if sv.layouts[i].TabID == res.PreviousTabID {
				res.SourceLayout = &sv.layouts[i]
			}
		}
		if dest.Type == "new_tab" {
			res.CreatedTab = v.tab(p.TabID)
		}
		if sv.tab(res.PreviousTabID) == nil {
			res.ClosedTabID = res.PreviousTabID
		}
	}
	if v.focusedPane != "" {
		res.FocusedPaneID = v.focusedPane
	}
	return &herdrResult{Type: "pane_move", MoveResult: res}, nil
}

const herdrClientTitleMax = 256

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
			return &herdrResult{Type: "client_window_title", Changed: new(false), Reason: "no_foreground_client"}, nil
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

const (
	herdrMetaKeysPerCall   = 16
	herdrMetaKeysPerTarget = 32
	herdrMetaKeyLen        = 32
	herdrMetaValueLen      = 80
	herdrMetaSourceLen     = 80
	herdrMetaSeqSources    = 32
	herdrMetaMaxTTLMS      = 86_400_000
)

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

func (m *herdrWsMeta) forget(session string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, session)
}

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

type herdrManifestInfo struct {
	Agent                        string `json:"agent"`
	Source                       string `json:"source"`
	SourceKind                   string `json:"source_kind"`
	LocalOverrideShadowingRemote bool   `json:"local_override_shadowing_remote"`
}

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
