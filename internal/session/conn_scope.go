package session

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
)

type connScope struct {
	own       bool
	readOnly  bool
	sessionID string
	window    string
	via       string
}

// Scope values restrict-connection accepts.
const (
	ScopeOwn = "own"
	ScopeAll = "all"
)

var scopeNames = []string{ScopeOwn, ScopeAll}

type scopeKind int

const (
	scopeUnset scopeKind = iota
	scopeDeny
	scopeOpen
	scopeGlobal
	scopeRead
	scopeSelf
	scopeMail
	scopeWrite
	scopeLaunch
	scopeBufferRead
	scopeBufferWrite
	scopeTask
)

func readsBuffers(verb string) bool {
	kind, _ := verbScope(verb)
	return kind == scopeBufferRead || verb == "paste-buffer"
}

func (d *Daemon) verbRestrictConnection(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Scope     string `json:"scope"`
		ReadOnly  bool   `json:"read_only"`
		PaneID    string `json:"pane_id"`
		PaneToken string `json:"pane_token"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Scope == "" {
		p.Scope = ScopeOwn
	}
	if !slices.Contains(scopeNames, p.Scope) {
		return nil, invalidParam("scope", "scope must be own or all", scopeNames...)
	}

	prev := cs.scope.Load()
	next := &connScope{own: p.Scope == ScopeOwn, readOnly: p.ReadOnly}
	if prev != nil {
		if prev.own && !next.own {
			return nil, scopeWidenError("scope", "this connection is already restricted to its own session, and a restriction cannot be lifted")
		}
		if prev.readOnly && !next.readOnly {
			return nil, scopeWidenError("read_only", "this connection is already read-only, and a restriction cannot be lifted")
		}
		if p.PaneID != "" && p.PaneID != prev.window {
			return nil, scopeWidenError("pane_id", "this connection's pane was settled by its first restrict-connection call")
		}
		next.sessionID, next.window, next.via = prev.sessionID, prev.window, prev.via
	} else {
		window, via, verr := d.placeCaller(cs, p.PaneID, p.PaneToken)
		if verr != nil {
			return nil, verr
		}
		next.window, next.via = window, via
		if sess := d.sessionHoldingWindow(window); sess != nil {
			next.sessionID = sess.ID
		} else {
			next.window, next.via = "", ""
		}
	}
	cs.scope.Store(next)
	cs.mu.Lock()
	cs.presenceNonce, cs.presenceSession = "", ""
	cs.mu.Unlock()
	LogBasic("Client %s restricted: scope=%s read_only=%v pane=%q via=%q", cs.clientID, scopeName(next), next.readOnly, shortWindowID(next.window), next.via)

	res := map[string]any{
		"type":      "connection_restricted",
		"scope":     scopeName(next),
		"read_only": next.readOnly,
		"window":    next.window,
		"session":   d.sessionNameByID(next.sessionID),
		"via":       next.via,
	}
	if next.own {
		res["sessions"] = d.scopeSessionNames(d.sessionNameByID(next.sessionID))
	}
	return res, nil
}

func scopeName(s *connScope) string {
	if s.own {
		return ScopeOwn
	}
	return ScopeAll
}

func scopeWidenError(param, msg string) *verbError {
	return hintedVerbError(ErrVerbForbidden, msg, &VerbHint{
		Param:  param,
		Detail: "Open a new connection for a different restriction. Nothing was changed.",
	})
}

func (d *Daemon) placeCaller(cs *connState, paneID, token string) (window, via string, verr *verbError) {
	if !cs.viaLink && !cs.paneOnly {
		if _, win := d.peerPane(cs); win != "" {
			if paneID != "" && paneID != win {
				return "", "", hintedVerbError(ErrVerbForbidden, "pane_id names another pane than the one this process runs in", &VerbHint{
					Param:  "pane_id",
					Detail: "The daemon reads the caller's pane from the kernel, and that answer wins. Omit pane_id, or pass the caller's own.",
				})
			}
			return win, "pid", nil
		}
	}
	if paneID == "" {
		return "", "", nil
	}
	if cs.viaLink || !d.manager.VerifyPaneToken(paneID, token) {
		return "", "", hintedVerbError(ErrVerbForbidden, "pane_token does not prove pane_id", &VerbHint{
			Param:  "pane_token",
			Detail: "Pass the TUIOS_PANE_TOKEN of the pane named by TUIOS_PANE_ID, from the same pane's environment. A token is good for one pane of one daemon start.",
		})
	}
	return paneID, "token", nil
}

func (d *Daemon) sessionOfWindow(id string) string {
	if sess := d.sessionHoldingWindow(id); sess != nil {
		return sess.Name()
	}
	return ""
}

func (d *Daemon) sessionHoldingWindow(id string) *Session {
	if id == "" {
		return nil
	}
	for _, sess := range d.manager.AllSessions() {
		if sess.holdsWindowID(id) {
			return sess
		}
	}
	return nil
}

func (d *Daemon) sessionNameByID(id string) string {
	if id == "" {
		return ""
	}
	if sess := d.manager.GetSessionByID(id); sess != nil {
		return sess.Name()
	}
	return ""
}

func (d *Daemon) callerSession(cs *connState) *Session {
	if cs == nil {
		return nil
	}
	if sc := cs.scope.Load(); sc != nil {
		if sc.sessionID == "" {
			return nil
		}
		return d.manager.GetSessionByID(sc.sessionID)
	}
	if w := cs.paneBound.Load(); w != nil {
		return d.sessionHoldingWindow(*w)
	}
	if cs.viaLink || cs.paneOnly || cs.peerPID <= 0 {
		return nil
	}
	if _, win := d.peerPane(cs); win != "" {
		return d.sessionHoldingWindow(win)
	}
	return nil
}

func (d *Daemon) sessionInScope(own, target string) bool {
	if own == "" || target == "" {
		return false
	}
	if o, _ := d.manager.ResolveSession(own); o != nil {
		own = o.Name()
	}
	t, _ := d.manager.ResolveSession(target)
	if t != nil {
		target = t.Name()
	}
	if target == own {
		return true
	}
	if t == nil {
		return false
	}
	tw := t.Worktree()
	if tw == nil {
		return false
	}
	o := d.manager.GetSession(own)
	if tw.LaunchedFromID != "" && o != nil && o.ID == tw.LaunchedFromID {
		return true
	}
	if tw.Group == "" || !tw.Managed {
		return false
	}
	if o == nil {
		return false
	}
	ow := o.Worktree()
	return ow != nil && ow.Managed && sameFan(ow, tw)
}

func (d *Daemon) scopeSessionNames(own string) []string {
	out := []string{}
	for _, sess := range d.manager.AllSessions() {
		if d.sessionInScope(own, sess.Name()) {
			out = append(out, sess.Name())
		}
	}
	slices.Sort(out)
	return out
}

func (d *Daemon) eventInScope(cs *connState, ev streamEvent) bool {
	if ev.Type == EventGap {
		return true
	}
	if ev.Transfer != nil || ev.File != nil {
		if cs.scope.Load() != nil {
			return false
		}
		if pa := cs.paneView.Load(); pa != nil && !pa.grants.Has(GrantAdmin) {
			owner := ""
			if ev.Transfer != nil {
				owner = ev.Transfer.Pane
			} else {
				owner = ev.File.Pane
			}
			return pa.grants.Has(GrantFiles) && owner != "" && owner == pa.window
		}
		if cs.viaLink && d.checkLinkCaps(cs, "transfer", []string{capRelay, config.LinkAllowFiles}) != nil {
			return false
		}
		return true
	}
	var owners []string
	if sc := cs.scope.Load(); sc != nil && sc.own {
		owners = append(owners, d.sessionNameByID(sc.sessionID))
	}
	if pa := cs.paneView.Load(); pa != nil && !pa.grants.Has(GrantAdmin) && pa.plugin == "" {
		own := pa.session
		if pa.sessionID != "" {
			own = d.sessionNameByID(pa.sessionID)
		}
		owners = append(owners, own)
	}
	if len(owners) == 0 {
		return true
	}
	if ev.Host != "" {
		return false
	}
	session := ev.Session
	if session == "" && ev.Attention != nil {
		session = ev.Attention.Session
	}
	for _, own := range owners {
		if !d.sessionInScope(own, session) {
			return false
		}
	}
	return true
}

func scopeForbidden(verb, why string) *verbError {
	return hintedVerbError(ErrVerbForbidden, verb+" is refused on this connection: "+why, &VerbHint{
		Verb:   "restrict-connection",
		Detail: "This connection was restricted with restrict-connection, and a restriction lasts as long as the connection. tuios mcp restricts its connections as its flags say: --write allows typing into panes, and --scope all reaches every session.",
	})
}

func (d *Daemon) checkScope(cs *connState, verb string, params json.RawMessage) (json.RawMessage, *verbError) {
	sc := cs.scope.Load()
	if sc == nil {
		return params, nil
	}
	kind, ok := verbScope(verb)
	if !ok || kind == scopeDeny {
		return nil, scopeForbidden(verb, "a restricted connection may not call it")
	}
	if kind == scopeOpen {
		return params, nil
	}
	if sc.readOnly && (kind == scopeWrite || kind == scopeLaunch) {
		return nil, scopeForbidden(verb, "the connection is read-only, and "+verb+" types into a pane or starts one")
	}
	if sc.readOnly && kind == scopeTask {
		return nil, scopeForbidden(verb, "the connection is read-only, and "+verb+" changes the task cards")
	}
	if sc.readOnly && kind == scopeBufferWrite {
		return nil, scopeForbidden(verb, "the connection is read-only, and "+verb+" changes the paste buffers")
	}
	if !sc.own {
		return params, nil
	}
	if kind == scopeGlobal {
		return nil, scopeForbidden(verb, "it reads every session, and the connection is restricted to its own")
	}
	if kind == scopeBufferWrite || readsBuffers(verb) {
		return nil, scopeForbidden(verb, "every session shares the paste buffers, and the connection is restricted to its own")
	}
	own := d.sessionNameByID(sc.sessionID)
	if own == "" {
		return nil, scopeForbidden(verb, "the connection is restricted to its own session, and the caller runs in no pane of this daemon")
	}
	reach := func(target string) string {
		if d.sessionInScope(own, target) {
			return ""
		}
		return "session " + echoName(target) + " is not the caller's own session or in its fan group"
	}
	deny := func(why string) *verbError { return scopeForbidden(verb, why) }
	return d.holdToPane(verb, kind, params, own, sc.window, "the connection is restricted to its own", reach, deny)
}

func (d *Daemon) holdToPane(verb string, kind scopeKind, params json.RawMessage, own, window, bound string, reach func(target string) string, deny func(why string) *verbError) (json.RawMessage, *verbError) {
	var m map[string]json.RawMessage
	if len(strings.TrimSpace(string(params))) > 0 {
		if err := jsonx.Unmarshal(params, &m); err != nil {
			return nil, invalidParam("params", "params must be an object")
		}
	}
	if m == nil {
		m = map[string]json.RawMessage{}
	}
	declares := func(name string) bool {
		entry, ok := verbRegistry()[verb]
		return ok && slices.ContainsFunc(entry.params, func(p verbParam) bool { return p.Name == name })
	}
	str := func(name string) string {
		var s string
		if raw, ok := m[name]; ok {
			_ = jsonx.Unmarshal(raw, &s)
		}
		return s
	}
	flag := func(name string) bool {
		var b bool
		if raw, ok := m[name]; ok {
			_ = jsonx.Unmarshal(raw, &b)
		}
		return b
	}
	set := func(name, value string) {
		raw, _ := jsonx.Marshal(value)
		m[name] = raw
	}

	session := str("session")
	switch {
	case session != "":
		if why := reach(session); why != "" {
			return nil, deny(why)
		}
	case verb == "subscribe":
	case declares("session"):
		session = own
		set("session", session)
		if why := reach(session); why != "" {
			return nil, deny(why)
		}
	}

	for _, wide := range []string{"all_sessions", "any_session", "hosts"} {
		if flag(wide) {
			return nil, deny(wide + " reaches every session, and " + bound)
		}
	}
	if str("select") != "" && verb != "list-agents" {
		return nil, deny("select reaches every session, and " + bound)
	}
	if h := str("host"); h != "" && h != "local" {
		return nil, deny("host " + echoName(h) + " is another machine, and " + bound + " session")
	}

	mine := func(name string) *verbError {
		switch v := str(name); v {
		case "":
			set(name, window)
		case window:
		default:
			return deny(name + " must be the caller's own window " + shortWindowID(window) + ", or omitted")
		}
		return nil
	}
	switch kind {
	case scopeSelf:
		if session != own {
			return nil, deny("it writes a pane's own record, and the caller's pane is in session " + own)
		}
		if verr := mine("window"); verr != nil {
			return nil, verr
		}
	case scopeRead:
		if verb == "agent-message-receipts" {
			if verr := mine("window"); verr != nil {
				return nil, verr
			}
		}
		if verb == "read-agent-messages" && str("to") != "" && str("to") != window {
			return nil, deny("to must be the caller's own window " + shortWindowID(window) + ", or omitted to read the session's ring without marking anything read")
		}
	case scopeMail, scopeWrite:
		if declares("from") {
			if session == own {
				if verr := mine("from"); verr != nil {
					return nil, verr
				}
			} else if f := str("from"); f != "" && f != window {
				return nil, deny("from must be the caller's own window " + shortWindowID(window) + ", or omitted")
			}
		}
	}

	out, err := jsonx.Marshal(m)
	if err != nil {
		return nil, newVerbError(ErrVerbInternal, "could not encode params")
	}
	return out, nil
}

func (d *Daemon) forgetLauncher(s *Session) {
	for _, o := range d.manager.AllSessions() {
		wt := o.Worktree()
		if wt == nil || wt.LaunchedFromID != s.ID {
			continue
		}
		moved := *wt
		moved.LaunchedFrom, moved.LaunchedFromID = "", ""
		_ = o.SetWorktree(&moved)
	}
}

func verbScope(verb string) (kind scopeKind, ok bool) {
	e, ok := verbRegistry()[verb]
	if e.scope == scopeUnset {
		return scopeDeny, ok
	}
	return e.scope, ok
}
