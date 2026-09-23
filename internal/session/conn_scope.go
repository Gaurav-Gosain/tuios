package session

import (
	"encoding/json"
	"slices"
	"strings"
)

// Restricted connections: the minimal form of scoped callers.
//
// Every connection on the daemon socket can call every verb. That is right for
// the person's own CLI and wrong for an agent that a file it read or a page it
// fetched has talked into acting on other panes. restrict-connection lets a
// client give up authority on its own connection, and the daemon then holds
// the connection to what is left for as long as it is open. A second call can
// narrow further and never widen.
//
// Two restrictions exist:
//
//   - scope own. The connection reaches only its own session: the session of
//     the pane the caller runs in, the sessions that share its fan group (the
//     siblings a fan started together), and the sessions a fan run from it
//     started. Every other session is invisible to it: reads fail with
//     forbidden, and its event stream carries nothing from them. The pane is
//     found by the kernel's record of the caller's pid first, and only when
//     that places the caller in no pane by TUIOS_PANE_ID plus the
//     TUIOS_PANE_TOKEN only that pane was started with (pane_token.go). A
//     caller placed in no pane reaches no session at all: the restriction
//     fails closed.
//   - read_only. The connection may read, report its own pane's state and
//     meta, and leave mail, and may not type into any pane: send-text,
//     send-keys, ask-agent, respond and fan are refused.
//
// Verbs outside the table below are refused on any restricted connection. The
// table has to name every verb, which a test checks, so a new verb is refused
// here until someone decides what a restricted caller may do with it.
//
// tuios mcp restricts every connection it opens before its first call, so an
// agent that drives tuios through MCP holds exactly what the server was
// started with. This does not stop a process in a pane from opening its own
// unrestricted connection with the tuios CLI; it bounds the MCP surface, which
// is the one an agent reaches without writing a shell command. docs/protocol.md
// has the whole contract.

// connScope is what a connection was restricted to.
type connScope struct {
	// own restricts the connection to its own session and fan group.
	own bool
	// readOnly refuses every verb that types into a pane or starts one.
	readOnly bool
	// session and window are the caller's pane, empty when the caller runs
	// in no pane of this daemon.
	session string
	window  string
	// via says how the pane was found: "pid" from the kernel, "token" from
	// TUIOS_PANE_TOKEN, or "" when it was not.
	via string
}

// Scope values restrict-connection accepts.
const (
	ScopeOwn = "own"
	ScopeAll = "all"
)

var scopeNames = []string{ScopeOwn, ScopeAll}

// scopeKind is what a verb does, for a restricted connection.
type scopeKind int

const (
	// scopeDeny: refused on any restricted connection.
	scopeDeny scopeKind = iota
	// scopeOpen touches no session: always allowed.
	scopeOpen
	// scopeGlobal reads across sessions. Allowed under read_only alone,
	// refused under scope own.
	scopeGlobal
	// scopeRead reads one session.
	scopeRead
	// scopeSelf writes the caller's own pane's record: its state, meta or
	// conversation id. Under scope own the window must be the caller's.
	scopeSelf
	// scopeMail leaves something in a session's store as the caller: mail or
	// a stashed file. Nothing is typed.
	scopeMail
	// scopeWrite types into a pane. Refused under read_only.
	scopeWrite
	// scopeLaunch starts new sessions. Refused under read_only, and under
	// scope own for a caller in no pane, whose launches it could not reach.
	scopeLaunch
)

// verbScopes classifies every verb. A verb missing here is treated as
// scopeDeny, and TestVerbScopesNameEveryVerb fails until it is added.
var verbScopes = map[string]scopeKind{
	"hello":               scopeOpen,
	"list-verbs":          scopeOpen,
	"unsubscribe":         scopeOpen,
	"restrict-connection": scopeOpen,

	"list-sessions":      scopeGlobal,
	"list-attention":     scopeGlobal,
	"list-worktrees":     scopeGlobal,
	"list-hosts":         scopeGlobal,
	"list-host-sessions": scopeGlobal,
	"list-host-agents":   scopeGlobal,
	"list-themes":        scopeGlobal,
	"list-glyphs":        scopeGlobal,
	"list-hooks":         scopeGlobal,

	"session-info":         scopeRead,
	"list-windows":         scopeRead,
	"list-workspaces":      scopeRead,
	"capture-pane":         scopeRead,
	"get-agent-state":      scopeRead,
	"list-agents":          scopeRead,
	"wait-for":             scopeRead,
	"subscribe":            scopeRead,
	"peek-prompt":          scopeRead,
	"read-agent-messages":  scopeRead,
	"explain-agent-screen": scopeRead,
	"list-options":         scopeRead,
	"get-option":           scopeRead,
	"stash-list":           scopeRead,
	"stash-get":            scopeRead,

	"set-agent-state":   scopeSelf,
	"set-agent-meta":    scopeSelf,
	"set-agent-session": scopeSelf,

	"send-agent-message": scopeMail,
	"stash-put":          scopeMail,

	"send-text": scopeWrite,
	"send-keys": scopeWrite,
	"ask-agent": scopeWrite,
	"respond":   scopeWrite,

	"fan": scopeLaunch,

	"list-dock-components": scopeDeny,
	"refresh-dock":         scopeDeny,
	"new-session":          scopeDeny,
	"new-worktree":         scopeDeny,
	"remove-worktree":      scopeDeny,
	"open-host-connection": scopeDeny,
	"open-pane":            scopeDeny,
	"resize-pane":          scopeDeny,
	"pane-cwd":             scopeDeny,
	"pane-agent":           scopeDeny,
	"pane-calls":           scopeDeny,
	"read-dir":             scopeDeny,
	"new-window":           scopeDeny,
	"popup":                scopeDeny,
	"split-window":         scopeDeny,
	"focus-window":         scopeDeny,
	"move-window":          scopeDeny,
	"set-window":           scopeDeny,
	"select-workspace":     scopeDeny,
	"set-layout":           scopeDeny,
	"run-command":          scopeDeny,
	"close-window":         scopeDeny,
	"screenshot":           scopeDeny,
	"resize":               scopeDeny,
	"kill-session":         scopeDeny,
	"set-option":           scopeDeny,
	"set-session-name":     scopeDeny,
	"set-session-accent":   scopeDeny,
	"set-workspace-name":   scopeDeny,
	"set-workspace-order":  scopeDeny,
	"resume-agent":         scopeDeny,
	"resolve-pane":         scopeDeny,
	"explain-agent-detect": scopeDeny,
	"dismiss-attention":    scopeDeny,
	"request-approval":     scopeDeny,
	"reply-approval":       scopeDeny,

	// From the host policy and dropped-link work: the link handshake,
	// ending a hosted pane, and passing on held mail are for a link
	// connection or the person's client, never a restricted caller.
	"link-peer":             scopeDeny,
	"close-pane":            scopeDeny,
	"release-agent-message": scopeDeny,
}

// verbRestrictConnection narrows what this connection may do from now on.
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
		// The pane was settled by the first call. A later claim may only
		// repeat it.
		if p.PaneID != "" && p.PaneID != prev.window {
			return nil, scopeWidenError("pane_id", "this connection's pane was settled by its first restrict-connection call")
		}
		next.session, next.window, next.via = prev.session, prev.window, prev.via
	} else {
		window, via, verr := d.placeCaller(cs, p.PaneID, p.PaneToken)
		if verr != nil {
			return nil, verr
		}
		next.window, next.via = window, via
		next.session = d.sessionOfWindow(window)
		if next.session == "" {
			next.window, next.via = "", ""
		}
	}
	cs.scope.Store(next)
	LogBasic("Client %s restricted: scope=%s read_only=%v pane=%q via=%q", cs.clientID, scopeName(next), next.readOnly, shortWindowID(next.window), next.via)

	res := map[string]any{
		"type":      "connection_restricted",
		"scope":     scopeName(next),
		"read_only": next.readOnly,
		"window":    next.window,
		"session":   next.session,
		"via":       next.via,
	}
	if next.own {
		res["sessions"] = d.scopeSessionNames(next.session)
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

// placeCaller finds the pane the process on cs runs in. The kernel's answer
// comes first; a pane_id claim that disagrees with it is refused. Only when the
// kernel places the caller in no pane is the claim checked against its token.
// A caller placed nowhere gets an empty window and no error: its restriction
// then reaches no session.
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

// sessionOfWindow names the local session holding a window, "" for none.
func (d *Daemon) sessionOfWindow(id string) string {
	if id == "" {
		return ""
	}
	for _, sess := range d.manager.AllSessions() {
		st := sess.GetState()
		for i := range st.Windows {
			if st.Windows[i].ID == id {
				return sess.Name
			}
		}
	}
	return ""
}

// callerSession is the session of the pane the caller on cs runs in, "" when it
// runs in none or cannot be placed.
func (d *Daemon) callerSession(cs *connState) string {
	if cs == nil {
		return ""
	}
	if sc := cs.scope.Load(); sc != nil {
		return sc.session
	}
	if cs.viaLink || cs.paneOnly || cs.peerPID <= 0 {
		return ""
	}
	if _, win := d.peerPane(cs); win != "" {
		return d.sessionOfWindow(win)
	}
	return ""
}

// sessionInScope reports whether a connection whose pane is in session own may
// reach session target: target is own, or it was launched from own, or it
// shares own's fan group in the same repository.
func (d *Daemon) sessionInScope(own, target string) bool {
	if own == "" || target == "" {
		return false
	}
	if target == own {
		return true
	}
	t := d.manager.GetSession(target)
	if t == nil {
		return false
	}
	tw := t.Worktree()
	if tw == nil {
		return false
	}
	if tw.LaunchedFrom == own {
		return true
	}
	if tw.Group == "" || !tw.Managed {
		return false
	}
	o := d.manager.GetSession(own)
	if o == nil {
		return false
	}
	ow := o.Worktree()
	return ow != nil && ow.Managed && ow.Group == tw.Group && ow.RepoRoot == tw.RepoRoot
}

// scopeSessionNames lists the sessions own reaches, sorted.
func (d *Daemon) scopeSessionNames(own string) []string {
	out := []string{}
	for _, sess := range d.manager.AllSessions() {
		if d.sessionInScope(own, sess.Name) {
			out = append(out, sess.Name)
		}
	}
	slices.Sort(out)
	return out
}

// eventInScope reports whether an event may be written to cs. Gap markers
// always may. Under scope own an event reaches the stream only when its
// session is one the connection reaches; an event with no session, such as
// host-changed, does not.
func (d *Daemon) eventInScope(cs *connState, ev streamEvent) bool {
	sc := cs.scope.Load()
	if sc == nil || !sc.own || ev.Type == EventGap {
		return true
	}
	if ev.Host != "" {
		return false
	}
	session := ev.Session
	if session == "" && ev.Attention != nil {
		session = ev.Attention.Session
	}
	return d.sessionInScope(sc.session, session)
}

// scopeForbidden is the refusal for a verb or target outside a restriction.
func scopeForbidden(verb, why string) *verbError {
	return hintedVerbError(ErrVerbForbidden, verb+" is refused on this connection: "+why, &VerbHint{
		Verb:   "restrict-connection",
		Detail: "This connection was restricted with restrict-connection, and a restriction lasts as long as the connection. tuios mcp restricts its connections as its flags say: --write allows typing into panes, and --scope all reaches every session.",
	})
}

// checkScope holds a call on a restricted connection to its restriction. It
// returns the params the handler should see, which under scope own have the
// caller's session, window and sender filled in where the call left them out,
// or the refusal. A connection that was never restricted passes unchanged.
func (d *Daemon) checkScope(cs *connState, verb string, params json.RawMessage) (json.RawMessage, *verbError) {
	sc := cs.scope.Load()
	if sc == nil {
		return params, nil
	}
	kind, ok := verbScopes[verb]
	if !ok || kind == scopeDeny {
		return nil, scopeForbidden(verb, "a restricted connection may not call it")
	}
	if kind == scopeOpen {
		return params, nil
	}
	if sc.readOnly && (kind == scopeWrite || kind == scopeLaunch) {
		return nil, scopeForbidden(verb, "the connection is read-only, and "+verb+" types into a pane or starts one")
	}
	if !sc.own {
		return params, nil
	}
	if kind == scopeGlobal {
		return nil, scopeForbidden(verb, "it reads every session, and the connection is restricted to its own")
	}
	if sc.session == "" {
		return nil, scopeForbidden(verb, "the connection is restricted to its own session, and the caller runs in no pane of this daemon")
	}

	var m map[string]json.RawMessage
	if len(strings.TrimSpace(string(params))) > 0 {
		if err := json.Unmarshal(params, &m); err != nil {
			// Not an object: the handler reports it as invalid_params. The
			// session cannot be checked, so the call is refused here instead.
			return nil, invalidParam("params", "params must be an object")
		}
	}
	if m == nil {
		m = map[string]json.RawMessage{}
	}
	declares := func(name string) bool {
		entry, ok := verbRegistry[verb]
		return ok && slices.ContainsFunc(entry.params, func(p verbParam) bool { return p.Name == name })
	}
	str := func(name string) string {
		var s string
		if raw, ok := m[name]; ok {
			_ = json.Unmarshal(raw, &s)
		}
		return s
	}
	flag := func(name string) bool {
		var b bool
		if raw, ok := m[name]; ok {
			_ = json.Unmarshal(raw, &b)
		}
		return b
	}
	set := func(name, value string) {
		raw, _ := json.Marshal(value)
		m[name] = raw
	}

	// The session. subscribe with none streams every session the connection
	// reaches, and is filtered event by event (eventInScope). Every other
	// verb gets the caller's own session when it names none, rather than the
	// most recently active one, which could be anybody's.
	session := str("session")
	switch {
	case session != "":
		if !d.sessionInScope(sc.session, session) {
			return nil, scopeForbidden(verb, "session "+echoName(session)+" is not the caller's own session or in its fan group")
		}
	case verb == "subscribe":
	case declares("session"):
		session = sc.session
		set("session", session)
	}

	for _, wide := range []string{"all_sessions", "any_session", "hosts"} {
		if flag(wide) {
			return nil, scopeForbidden(verb, wide+" reaches every session, and the connection is restricted to its own")
		}
	}
	// A session on another machine is never in reach: send-agent-message's
	// host sends there over this machine's link.
	if h := str("host"); h != "" && h != "local" {
		return nil, scopeForbidden(verb, "host "+echoName(h)+" is another machine, and the connection is restricted to its own session")
	}

	own := func(name string) *verbError {
		switch v := str(name); v {
		case "":
			set(name, sc.window)
		case sc.window:
		default:
			return scopeForbidden(verb, name+" must be the caller's own window "+shortWindowID(sc.window)+", or omitted")
		}
		return nil
	}
	switch kind {
	case scopeSelf:
		if session != sc.session {
			return nil, scopeForbidden(verb, "it writes a pane's own record, and the caller's pane is in session "+sc.session)
		}
		if verr := own("window"); verr != nil {
			return nil, verr
		}
	case scopeRead:
		if verb == "read-agent-messages" && str("to") != "" && str("to") != sc.window {
			return nil, scopeForbidden(verb, "to must be the caller's own window "+shortWindowID(sc.window)+", or omitted to read the session's ring without marking anything read")
		}
	case scopeMail, scopeWrite:
		// from names a window of the target session. It is filled in only
		// when that is the caller's own session, where the caller's window
		// resolves; elsewhere a from the caller passes must still be its own.
		if declares("from") {
			if session == sc.session {
				if verr := own("from"); verr != nil {
					return nil, verr
				}
			} else if f := str("from"); f != "" && f != sc.window {
				return nil, scopeForbidden(verb, "from must be the caller's own window "+shortWindowID(sc.window)+", or omitted")
			}
		}
	}

	out, err := json.Marshal(m)
	if err != nil {
		return nil, newVerbError(ErrVerbInternal, "could not encode params")
	}
	return out, nil
}
