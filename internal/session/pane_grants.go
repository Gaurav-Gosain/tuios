package session

import (
	"encoding/json"
	"log"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/Gaurav-Gosain/tuios/internal/shquote"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/harness"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/procinfo"
	"github.com/Gaurav-Gosain/tuios/internal/tape"
)

// Grants is a set of pane grants.
type Grants uint8

// The grants, one bit each.
const (
	GrantRead Grants = 1 << iota
	GrantWrite
	GrantFan
	GrantRespond
	GrantAdmin
	// GrantFiles copies files: transfer-start with both ends in the pane's
	// own folder or a host's home, and the transfer verbs on the copies the
	// pane started. See transferGrantCheck in transfer_grants.go.
	GrantFiles
	// GrantTask writes the task cards of the pane's own session. See
	// task.go.
	GrantTask
	// GrantUI shows a plugin's cells, panels and messages. Only a native
	// plugin holds it, so it is not in config.PaneGrantNames. See
	// plugin_grants.go.
	GrantUI
)

const grantNameUI = "ui"

const grantNone = "none"

var grantByName = map[string]Grants{
	config.PaneGrantRead:    GrantRead,
	config.PaneGrantWrite:   GrantWrite,
	config.PaneGrantFan:     GrantFan,
	config.PaneGrantRespond: GrantRespond,
	config.PaneGrantFiles:   GrantFiles,
	config.PaneGrantTask:    GrantTask,
	config.PaneGrantAdmin:   GrantAdmin,
	grantNameUI:             GrantUI,
}

var grantAccepted = append(append([]string{}, config.PaneGrantNames...), grantNone)

func grantsFromNames(names []string) Grants {
	var g Grants
	for _, n := range names {
		g |= grantByName[n]
	}
	return g
}

func parseGrantsParam(names []string) (Grants, *verbError) {
	if len(names) == 1 && strings.EqualFold(strings.TrimSpace(names[0]), grantNone) {
		return 0, nil
	}
	if len(names) == 0 {
		return 0, invalidParam("grants", "grants is empty: name the grants, or none for no grants at all", grantAccepted...)
	}
	valid, unknown := config.CanonicalPaneGrants(names)
	if len(unknown) > 0 {
		return 0, invalidParam("grants", "unknown grant "+echoName(unknown[0]), grantAccepted...)
	}
	return grantsFromNames(valid), nil
}

func (g Grants) expand() Grants {
	if g&GrantAdmin != 0 {
		g |= GrantRead | GrantWrite | GrantFan | GrantFiles | GrantTask | GrantUI
	}
	return g
}

// Has reports whether g holds every grant in x, counting what admin implies.
func (g Grants) Has(x Grants) bool { return g.expand()&x == x }

// Covers reports whether g holds everything o holds, so a holder of g may
// give o to another pane.
func (g Grants) Covers(o Grants) bool { return o.expand()&^g.expand() == 0 }

// Names lists the grants in g as they were given, in documented order. The
// empty set is an empty list.
func (g Grants) Names() []string {
	out := []string{}
	for _, n := range config.PaneGrantNames {
		if g&grantByName[n] != 0 {
			out = append(out, n)
		}
	}
	if g&GrantUI != 0 {
		out = append(out, grantNameUI)
	}
	return out
}

// String is Names joined with commas, or none.
func (g Grants) String() string {
	if g == 0 {
		return grantNone
	}
	return strings.Join(g.Names(), ",")
}

type panePolicy struct {
	strict bool
	grants Grants
}

type paneGrantEntry struct {
	ptyID   string
	session string
	grants  *Grants
}

type paneGrantTable struct {
	mu            sync.RWMutex
	panes         map[string]paneGrantEntry
	explicit      int
	policy        atomic.Pointer[panePolicy]
	restartNeeded atomic.Bool
}

func newPaneGrantTable() *paneGrantTable {
	return &paneGrantTable{panes: make(map[string]paneGrantEntry)}
}

func (t *paneGrantTable) setPolicy(r config.ResolvedPermissions) {
	if t == nil {
		return
	}
	t.policy.Store(&panePolicy{strict: r.Strict, grants: grantsFromNames(r.Grants)})
}

func (t *paneGrantTable) strict() bool {
	if t == nil {
		return false
	}
	p := t.policy.Load()
	return p != nil && p.strict
}

func (t *paneGrantTable) defaults() Grants {
	if t == nil {
		return GrantAdmin
	}
	if p := t.policy.Load(); p != nil && p.strict {
		return p.grants
	}
	return GrantAdmin
}

func (t *paneGrantTable) strictDefaults() Grants {
	if t != nil {
		if p := t.policy.Load(); p != nil {
			return p.grants
		}
	}
	return grantsFromNames(config.DefaultStrictGrants)
}

func (t *paneGrantTable) mayMatter() bool {
	if t == nil {
		return false
	}
	if t.strict() {
		return true
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.explicit > 0
}

func (t *paneGrantTable) add(windowID, ptyID, session string, g *Grants) {
	if t == nil || windowID == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if old, ok := t.panes[windowID]; ok && old.grants != nil {
		if g == nil {
			g = old.grants
		}
		t.explicit--
	}
	if g != nil {
		c := *g
		g = &c
		t.explicit++
	}
	t.panes[windowID] = paneGrantEntry{ptyID: ptyID, session: session, grants: g}
}

func (t *paneGrantTable) remove(windowID, ptyID string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if old, ok := t.panes[windowID]; ok && old.ptyID == ptyID {
		if old.grants != nil {
			t.explicit--
		}
		delete(t.panes, windowID)
	}
}

func (t *paneGrantTable) lookup(windowID string) (paneGrantEntry, bool) {
	if t == nil {
		return paneGrantEntry{}, false
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	e, ok := t.panes[windowID]
	return e, ok
}

func (t *paneGrantTable) effective(windowID string) (Grants, bool) {
	if e, ok := t.lookup(windowID); ok && e.grants != nil {
		return *e.grants, true
	}
	return t.defaults(), false
}

func (t *paneGrantTable) set(windowID string, g *Grants) bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.panes[windowID]
	if !ok {
		return false
	}
	if e.grants != nil {
		t.explicit--
	}
	if g != nil {
		c := *g
		g = &c
		t.explicit++
	}
	e.grants = g
	t.panes[windowID] = e
	return true
}

func grantNamesPtr(g *Grants) []string {
	if g == nil {
		return nil
	}
	if *g == 0 {
		return []string{grantNone}
	}
	return g.Names()
}

func (s *Session) recordedGrants(windowID string) *Grants {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	if s.state == nil {
		return nil
	}
	for i := range s.state.Windows {
		if s.state.Windows[i].ID == windowID {
			return savedGrants(s.state.Windows[i].Grants)
		}
	}
	return nil
}

// SetPanePermissions replaces [agents.permissions] for every pane.
func (m *Manager) SetPanePermissions(r config.ResolvedPermissions) {
	m.grants.setPolicy(r)
	m.grants.restartNeeded.Store(false)
}

func policyDefaults(r config.ResolvedPermissions) Grants {
	if !r.Strict {
		return GrantAdmin
	}
	return grantsFromNames(r.Grants)
}

func (d *Daemon) reloadPanePermissions(r config.ResolvedPermissions) {
	t := d.manager.grants
	cur, want := t.defaults(), policyDefaults(r)
	if cur.Covers(want) {
		t.setPolicy(r)
		t.restartNeeded.Store(false)
		d.recordAppliedGrants(t.inForce())
		return
	}
	kept := cur.expand() & want.expand()
	t.setPolicy(config.ResolvedPermissions{Strict: true, Grants: kept.Names()})
	t.restartNeeded.Store(true)
	d.recordAppliedGrants(t.inForce())
	log.Printf("The new [agents.permissions] gives panes more than before. Panes on the default now hold %s. The rest applies after tuios config apply from outside tuios, or a daemon restart.", kept.String())
}

// PanePermissionsFromConfig reads [agents.permissions].
func PanePermissionsFromConfig(c config.PermissionsConfig) config.ResolvedPermissions {
	return c.Resolve()
}

type paneAuth struct {
	window    string
	session   string
	sessionID string
	via       string
	grants    Grants
	explicit  bool
	hosted    bool
	linkTable string
	plugin    string
}

func (d *Daemon) addressesHostedPane(verb string, params json.RawMessage) bool {
	field, ok := hostedCallVerbs[verb]
	if !ok || len(params) == 0 {
		return false
	}
	var fields map[string]json.RawMessage
	if jsonx.Unmarshal(params, &fields) != nil {
		return false
	}
	var addr string
	if raw, ok := fields[field]; !ok || jsonx.Unmarshal(raw, &addr) != nil {
		return false
	}
	return d.hostedPaneByAddress(addr) != nil
}

func (d *Daemon) hostedPaneOfPeer(cs *connState) string {
	if d.hostedPeer != nil {
		return d.hostedPeer(cs)
	}
	if d.approvalPeer.Load() != nil || cs.peerPID <= 0 || !d.connFromPane(cs) {
		return ""
	}
	d.hostedPanesMu.Lock()
	none := len(d.hostedPanes) == 0
	d.hostedPanesMu.Unlock()
	if none {
		return ""
	}
	start := uint64(0)
	if cs.peerStartOK {
		start = cs.peerStart
	}
	if w := d.spawnRootOf(cs.peerPID, start, true); w.found && w.root.kind == spawnHosted {
		return w.root.id
	}
	return ""
}

func (d *Daemon) hookOfPeer(cs *connState) bool {
	if cs == nil || cs.viaLink || cs.peerPID <= 0 || !cs.peerStartOK || d.approvalPeer.Load() != nil {
		return false
	}
	if !d.connFromPane(cs) || cs.fromPaneWhy == paneOriginEnv || d.peerChanged(cs) {
		return false
	}
	w := d.spawnRootOf(cs.peerPID, cs.peerStart, false)
	return w.found && w.root.kind == spawnHook
}

func (d *Daemon) paneWriteReach(pa *paneAuth, target string) string {
	switch {
	case pa.grants.Has(GrantAdmin):
		return ""
	case target == pa.session:
		if pa.grants.Has(GrantWrite) {
			return ""
		}
		return "writing into the pane's own session needs the write grant"
	case d.sessionInScope(pa.session, target):
		if pa.grants.Has(GrantFan) {
			return ""
		}
		return "session " + echoName(target) + " is in the pane's fan group, and writing there needs the fan grant"
	}
	return "session " + echoName(target) + " is not the pane's own session or in its fan group, and reaching it needs the admin grant"
}

func (d *Daemon) paneAuthority(cs *connState) *paneAuth {
	if cs == nil || cs.viaLink || cs.paneOnly {
		return nil
	}
	if pa := d.pluginAuthority(cs); pa != nil {
		return pa
	}
	if cs.paneBound.Load() == nil && d.approvalPeer.Load() == nil && !d.connFromPane(cs) {
		return nil
	}
	if cs.paneBound.Load() == nil && cs.panePlaced.Load() == nil && d.peerChanged(cs) {
		return &paneAuth{window: unplacedWindow, via: "pid", grants: d.manager.grants.strictDefaults()}
	}
	window, via := d.placePaneWindow(cs)
	if window == "" {
		if id := d.hostedPaneOfPeer(cs); id != "" {
			return &paneAuth{window: "hosted:" + id, via: "pid", grants: d.manager.grants.defaults(), hosted: true}
		}
		if d.connFromPane(cs) && cs.fromPaneWhy != paneOriginEnv {
			return &paneAuth{window: unplacedWindow, via: "pid", grants: d.manager.grants.defaults()}
		}
		return nil
	}
	table := d.manager.grants
	g, explicit := table.effective(window)
	session, sessionID := "", ""
	if sess := d.sessionHoldingWindow(window); sess != nil {
		session, sessionID = sess.Name(), sess.ID
	}
	if session == "" {
		if e, ok := table.lookup(window); ok {
			session = e.session
		}
	}
	return &paneAuth{window: window, session: session, sessionID: sessionID, via: via, grants: g, explicit: explicit}
}

const unplacedWindow = "unknown"

func (d *Daemon) placePaneWindow(cs *connState) (window, via string) {
	if w := cs.paneBound.Load(); w != nil {
		return *w, "token"
	}
	if w := cs.panePlaced.Load(); w != nil {
		return *w, "pid"
	}
	fromPane, win := d.peerPane(cs)
	via = "pid"
	if win == "" && fromPane && d.approvalPeer.Load() == nil && cs.peerPID > 0 {
		if id, ok := procinfo.EnvVar(cs.peerPID, "TUIOS_PANE_ID"); ok && id != "" {
			if _, known := d.manager.grants.lookup(id); known {
				return id, "env"
			}
		}
	}
	if win != "" {
		cs.panePlaced.Store(&win)
	}
	return win, via
}

func grantKind(verb string) scopeKind {
	switch verb {
	case "request-approval":
		return scopeSelf
	case "resolve-pane", "pane-grants", "set-pane-grants":
		return scopeOpen
	}
	kind, ok := verbScope(verb)
	if !ok {
		return scopeDeny
	}
	return kind
}

func grantForbidden(verb string, pa *paneAuth, why string) *verbError {
	if pa.plugin != "" {
		return hintedVerbError(ErrVerbForbidden, verb+" is refused for plugin "+pa.plugin+": "+why, &VerbHint{
			Detail: "The plugin holds " + pa.grants.String() + ". Nothing was done. A plugin asks for grants in [permissions] of tuios-plugin.toml. " +
				"It holds them only after the person allows the plugin on this machine, in the approval dialog or with tuios plugins approve.",
		})
	}
	if pa.linkTable != "" {
		return hintedVerbError(ErrVerbForbidden, verb+" is refused for this link: "+why, &VerbHint{
			Detail: "Nothing was done. The call came over a link from a pane on the other machine, or from a machine that does not say. " +
				"Pane grants do not travel over a link, so the link policy holds the call. " +
				"To let such calls answer prompts, add \"respond\" to allow in " + pa.linkTable + " in the config on this machine.",
		})
	}
	target := "-w " + shortWindowID(pa.window)
	if pa.session != "" {
		target = "-s " + shquote.Word(pa.session) + " " + target
	}
	source := "the default of [agents.permissions]"
	if pa.explicit {
		source = "the grants it was given"
	}
	return hintedVerbError(ErrVerbForbidden, verb+" is refused for this pane: "+why, &VerbHint{
		Verb:    "pane-grants",
		Command: "tuios pane-grants",
		Detail: "Pane " + shortWindowID(pa.window) + " holds " + pa.grants.String() + ", " + source + ". Nothing was done. " +
			"What a pane may do is the person's decision. Do not try another verb or another process to do the same thing. " +
			"Tell the person what you needed and why. " +
			"The person can give this pane more with tuios set-pane-grants " + target + " --grants <names>, " +
			"or every pane started with none with mode and grants under [agents.permissions] in config.toml.",
	})
}

func (d *Daemon) checkGrants(cs *connState, verb string, params json.RawMessage) (json.RawMessage, *verbError) {
	if cs != nil && cs.viaLink && typingVerbs[verb] {
		return d.holdLinkTyping(cs, verb, params)
	}
	if cs != nil && cs.viaLink && grantKind(verb) == scopeSelf {
		if pa := d.linkTypingAuthority(cs); pa != nil && !pa.grants.Has(GrantRespond) {
			LogBasic("Link %s refused %s: it reports for a pane on this machine", cs.clientID, verb)
			return nil, grantForbidden(verb, pa, "it reports for a pane on this machine, and only that pane, the person, or a caller allowed to answer prompts reports for it")
		}
		return params, nil
	}
	if cs == nil || cs.viaLink || cs.paneOnly {
		return params, nil
	}
	if cs.paneBound.Load() == nil && cs.pluginBound.Load() == nil && !d.manager.grants.mayMatter() && !d.nativePluginsRun() && !typingVerbs[verb] && grantKind(verb) != scopeSelf {
		return params, nil
	}
	pa := d.paneAuthority(cs)
	if pa == nil {
		cs.paneView.Store(nil)
		return params, nil
	}
	cs.paneView.Store(pa)
	deny := func(why string) *verbError {
		LogBasic("Pane %s%s (%s) refused %s: %s", shortWindowID(pa.window), pa.plugin, pa.grants.String(), verb, why)
		return grantForbidden(verb, pa, why)
	}
	if pa.plugin != "" {
		return d.checkPluginGrants(cs, verb, pa, params, deny)
	}
	if pa.grants.Has(GrantAdmin) {
		if typingVerbs[verb] {
			return d.holdTypingTarget(cs, verb, pa, params, deny)
		}
		if grantKind(verb) == scopeSelf {
			return d.holdOwnRecord(verb, pa, params, deny)
		}
		return params, nil
	}
	kind := grantKind(verb)
	if pa.hosted {
		if kind == scopeOpen || d.addressesHostedPane(verb, params) {
			return params, nil
		}
		return nil, deny("the pane runs here for another machine and has no session on this one; its reports go to that machine")
	}
	if readsBuffers(verb) && !pa.grants.Has(GrantRead) {
		return nil, deny(verb + " reads the paste buffers, which needs the read grant")
	}
	var reach func(target string) string
	switch kind {
	case scopeOpen:
		return params, nil
	case scopeBufferRead:
		return params, nil
	case scopeBufferWrite:
		if !pa.grants.Has(GrantWrite) {
			return nil, deny(verb + " changes the paste buffers, which needs the write grant")
		}
		return params, nil
	case scopeDeny, scopeGlobal:
		if transferGrantVerbs[verb] && pa.grants.Has(GrantFiles) {
			return params, nil
		}
		if transferGrantVerbs[verb] {
			return nil, deny(verb + " copies files, which needs the files grant")
		}
		return nil, deny(verb + " needs the admin grant")
	case scopeSelf:
		reach = func(target string) string {
			if target == pa.session {
				return ""
			}
			return "it writes the pane's own record, and the pane is in session " + pa.session
		}
	case scopeRead:
		if !pa.grants.Has(GrantRead) {
			return nil, deny(verb + " needs the read grant")
		}
		reach = func(target string) string {
			if d.sessionInScope(pa.session, target) || judgeReadVerbs[verb] && d.judgeReaches(pa, target) {
				return ""
			}
			return "session " + echoName(target) + " is not the pane's own session or in its fan group, and reading it needs the admin grant"
		}
	case scopeLaunch:
		verdict := verb == "set-fan-verdict"
		if !pa.grants.Has(GrantFan) && !verdict {
			return nil, deny(verb + " starts agents, which needs the fan grant")
		}
		reach = func(target string) string {
			if verdict && d.judgeReaches(pa, target) {
				return ""
			}
			if pa.grants.Has(GrantFan) && d.sessionInScope(pa.session, target) {
				return ""
			}
			return "session " + echoName(target) + " is not the pane's own session or in its fan group, and starting agents there needs the admin grant"
		}
	case scopeTask:
		if !pa.grants.Has(GrantTask) {
			return nil, deny(verb + " writes the task cards, which needs the task grant")
		}
		reach = func(target string) string {
			if target == pa.session {
				return ""
			}
			return "session " + echoName(target) + " is not the pane's own session, and its task cards need the admin grant"
		}
	case scopeMail, scopeWrite:
		if verb == "respond" {
			if !pa.grants.Has(GrantRespond) {
				return nil, deny("answering another pane's prompt needs the respond grant")
			}
			reach = func(target string) string {
				if target == pa.session || (pa.grants.Has(GrantFan) && d.sessionInScope(pa.session, target)) {
					return ""
				}
				return "session " + echoName(target) + " holds no pane this pane may answer for"
			}
			break
		}
		if !pa.grants.Has(GrantWrite) && !pa.grants.Has(GrantFan) {
			return nil, deny(verb + " needs the write grant")
		}
		reach = func(target string) string { return d.paneWriteReach(pa, target) }
	default:
		return nil, deny(verb + " needs the admin grant")
	}
	if pa.session == "" {
		return nil, deny("the pane's session could not be found")
	}
	out, verr := d.holdToPane(verb, kind, params, pa.session, pa.window, "without the admin grant a pane reaches only its own", reach, deny)
	if verr != nil || !typingVerbs[verb] {
		return out, verr
	}
	return d.holdTypingTarget(cs, verb, pa, out, deny)
}

func (d *Daemon) holdLinkTyping(cs *connState, verb string, params json.RawMessage) (json.RawMessage, *verbError) {
	pa := d.linkTypingAuthority(cs)
	if pa == nil {
		cs.paneView.Store(nil)
		return params, nil
	}
	cs.paneView.Store(pa)
	deny := func(why string) *verbError {
		LogBasic("Link %s (%s) refused %s: %s", cs.clientID, pa.window, verb, why)
		return grantForbidden(verb, pa, why)
	}
	return d.holdTypingTarget(cs, verb, pa, params, deny)
}

func (d *Daemon) linkTypingAuthority(cs *connState) *paneAuth {
	if cs == nil || !cs.viaLink || cs.linkHuman {
		return nil
	}
	policy := d.linkPolicy(cs)
	g := GrantAdmin
	if policy.Allows(config.LinkAllowRespond) {
		g |= GrantRespond
	}
	peer, table := policy.Peer, `[hosts."*"]`
	if peer == "" {
		peer = "unnamed"
	} else {
		table = "[hosts." + peer + "]"
	}
	return &paneAuth{window: "link:" + peer, via: "link", grants: g, linkTable: table}
}

var typingVerbs = map[string]bool{
	"send-text":    true,
	"send-keys":    true,
	"ask-agent":    true,
	"run":          true,
	"queue-prompt": true,
	"send-review":  true,
	"paste-buffer": true,
}

func (d *Daemon) holdTypingTarget(cs *connState, verb string, pa *paneAuth, params json.RawMessage, deny func(string) *verbError) (json.RawMessage, *verbError) {
	var m map[string]json.RawMessage
	if err := jsonx.Unmarshal(params, &m); err != nil || m == nil {
		return params, nil
	}
	str := func(name string) string {
		var s string
		if raw, ok := m[name]; ok {
			_ = jsonx.Unmarshal(raw, &s)
		}
		return s
	}
	window := str("window")
	if window == "" && (verb == "send-text" || verb == "send-keys" || verb == "run") && pa.inPane() {
		return nil, typingNeedsWindow(verb)
	}
	sess := d.findTargetSession(cs, str("session"))
	if sess == nil {
		return params, nil
	}
	if verb == "ask-agent" && (window == "" || window == AgentInboxHuman) {
		return params, nil
	}
	state := sess.GetState()
	if window == "" {
		id, err := focusedWindowID(state)
		if err != nil {
			return params, nil
		}
		window = id
	}
	idx, err := findWindowStateIndex(state.Windows, window)
	if err != nil {
		return params, nil
	}
	target := state.Windows[idx]
	var allowBlocked bool
	if raw, ok := m["allow_blocked"]; ok {
		_ = jsonx.Unmarshal(raw, &allowBlocked)
	}
	blockedChecked := verb == "ask-agent" && !allowBlocked
	if pa.grants.Has(GrantAdmin) && (verb == "queue-prompt" || verb == "send-review") {
		blockedChecked = true
	}
	if why := d.typingRefusal(pa, target, blockedChecked); why != "" {
		return nil, deny(why)
	}
	if verr := typingBlocked(verb, pa, target, sess.Name(), allowBlocked); verr != nil {
		LogBasic("Pane %s refused %s: window %s is on needs_input", shortWindowID(pa.window), verb, shortWindowID(target.ID))
		return nil, verr
	}
	if pa.grants.Has(GrantAdmin) {
		return params, nil
	}
	raw, _ := jsonx.Marshal(target.ID)
	m["window"] = raw
	out, err := jsonx.Marshal(m)
	if err != nil {
		return nil, newVerbError(ErrVerbInternal, "could not encode params")
	}
	return out, nil
}

func (d *Daemon) holdOwnRecord(verb string, pa *paneAuth, params json.RawMessage, deny func(string) *verbError) (json.RawMessage, *verbError) {
	if pa.grants.Has(GrantRespond) || pa.plugin != "" {
		return params, nil
	}
	if pa.window == unplacedWindow {
		LogBasic("A process outside every pane (%s) refused %s: it reports for no pane", pa.grants.String(), verb)
		return nil, hintedVerbError(ErrVerbForbidden, verb+" is refused: this process runs outside every pane, so it reports for no pane", &VerbHint{
			Detail: "Nothing was done. Report from inside the pane, or run the command yourself. A pane with the respond grant can report for another pane.",
		})
	}
	if pa.hosted {
		if d.addressesHostedPane(verb, params) {
			return params, nil
		}
		return nil, deny("it reports for the caller's own pane, and this pane runs here for another machine. Reporting for another pane needs the respond grant")
	}
	var m map[string]json.RawMessage
	if len(strings.TrimSpace(string(params))) > 0 {
		if err := jsonx.Unmarshal(params, &m); err != nil {
			return nil, invalidParam("params", "params must be an object")
		}
	}
	if m == nil {
		m = map[string]json.RawMessage{}
	}
	str := func(name string) string {
		var s string
		if raw, ok := m[name]; ok {
			_ = jsonx.Unmarshal(raw, &s)
		}
		return s
	}
	other := func(what string) *verbError {
		return deny(what + ", and a pane reports only for its own window " + shortWindowID(pa.window) +
			". Reporting for another pane needs the respond grant")
	}
	session, window := str("session"), str("window")
	own := d.sessionHoldingWindow(pa.window)
	if own == nil {
		if window != "" && window != pa.window {
			return nil, other("window " + echoName(window) + " is not this pane")
		}
	} else {
		if session != "" {
			if sess := d.findTargetSession(nil, session); sess == nil || sess.ID != own.ID {
				return nil, other("session " + echoName(session) + " is not the pane's own session")
			}
		}
		session = own.Name()
		if window != "" {
			wins := own.GetState().Windows
			if idx, err := findWindowStateIndex(wins, window); err != nil || wins[idx].ID != pa.window {
				return nil, other("window " + echoName(window) + " is not this pane")
			}
		}
	}
	set := func(name, value string) {
		raw, _ := jsonx.Marshal(value)
		m[name] = raw
	}
	if session != "" {
		set("session", session)
	}
	set("window", pa.window)
	out, err := jsonx.Marshal(m)
	if err != nil {
		return nil, newVerbError(ErrVerbInternal, "could not encode params")
	}
	return out, nil
}

func (d *Daemon) typingRefusal(pa *paneAuth, target WindowState, blockedChecked bool) string {
	if target.ID == pa.window {
		return ""
	}
	if !blockedChecked {
		if why := d.childApprovalRefusal(pa.window, target); why != "" {
			return why
		}
	}
	held, _ := d.manager.grants.effective(target.ID)
	if pa.plugin != "" {
		held = 0
	}
	if !pa.grants.Has(GrantAdmin) && !pa.grants.Covers(held) {
		return "window " + shortWindowID(target.ID) + " holds " + held.String() + ", more than this pane holds, and what is typed there runs with that. " +
			"A pane types only into panes that hold nothing it does not"
	}
	if !pa.grants.Has(GrantAdmin) && pa.plugin == "" && held.Has(GrantFiles) {
		if tf := d.windowFolder(target.ID); tf != "" {
			if pf := d.paneFolder(pa); pf == "" || !pathUnder(tf, pf) {
				return "window " + shortWindowID(target.ID) + " holds files for " + echoName(tf) + ", a folder this pane's files grant does not reach, and what is typed there copies from it. " +
					"A pane types only into panes whose folder is inside its own"
			}
		}
	}
	if !pa.grants.Has(GrantRespond) && d.agentsOff.Load() && !d.offTypingAllowed(pa, target) {
		return "the agent features are off, so tuios cannot see whether window " + shortWindowID(target.ID) +
			" is waiting on a prompt. Typing into a pane this pane did not open, and that is not at its shell prompt, needs the respond grant"
	}
	if target.AgentState != AgentStateNeedsInput && !pa.grants.Has(GrantRespond) {
		if sess := d.sessionHoldingWindow(target.ID); sess != nil && sess.promptStillShown(target) {
			return "window " + shortWindowID(target.ID) + " was waiting on a prompt, and its state was changed from outside the pane. " +
				"The prompt can still be on the screen, and what is typed now could answer it, which needs the respond grant"
		}
	}
	if !blockedChecked && target.AgentState == AgentStateNeedsInput && !pa.grants.Has(GrantRespond) && pa.plugin != "" {
		return "window " + shortWindowID(target.ID) + " is waiting on a prompt, and what is typed now would answer it. A plugin does not answer a prompt that tuios knows of. Only you do that"
	}
	if !blockedChecked && target.AgentState == AgentStateNeedsInput && !pa.grants.Has(GrantRespond) {
		return "window " + shortWindowID(target.ID) + " is waiting on a prompt, and what is typed now would answer it, which needs the respond grant"
	}
	return ""
}

var typingAllowBlockedParam = verbParam{
	Name:        "allow_blocked",
	Type:        "bool",
	Description: "From a pane, type into a pane on needs_input instead of refusing with agent_blocked. What is typed then answers its prompt, which also needs the respond grant. The person, outside every pane, is not held.",
	Default:     "false",
}

var paneTypingVerbs = map[string]bool{
	"send-text":    true,
	"send-keys":    true,
	"paste-buffer": true,
}

func (pa *paneAuth) inPane() bool {
	return pa != nil && pa.plugin == "" && pa.via != "link" && pa.window != "" && pa.window != unplacedWindow
}

func (pa *paneAuth) outsideWithAdmin() bool {
	return pa != nil && pa.plugin == "" && !pa.hosted && pa.window == unplacedWindow && pa.grants.Has(GrantAdmin)
}

func typingNeedsWindow(verb string) *verbError {
	return hintedVerbError(ErrVerbInvalidParams, verb+" from a pane needs a window: without one, the text goes to the pane the person has in focus", &VerbHint{
		Param:   "window",
		Verb:    "list-windows",
		Command: "tuios list-windows",
		Detail:  "Nothing was typed. Name the pane with -w: its name, its id, or its index from list-windows.",
	})
}

func typingBlocked(verb string, pa *paneAuth, target WindowState, session string, allowBlocked bool) *verbError {
	if allowBlocked || !paneTypingVerbs[verb] || !pa.inPane() || target.ID == pa.window || target.AgentState != AgentStateNeedsInput {
		return nil
	}
	what := "a prompt"
	switch agentBlockedBy(target) {
	case harness.PromptKindApproval:
		what = "an approval"
	case harness.PromptKindQuestion:
		what = "a question"
	case harness.PromptKindAuth:
		what = "a login"
	}
	msg := "window " + shortWindowID(target.ID) + " is waiting on " + what + ", and text typed now would answer it"
	if note := printableClaim(target.AgentMessage, agentMsgMaxSubject); note != "" {
		msg += ": " + note
	}
	return hintedVerbError(ErrVerbAgentBlocked, msg, &VerbHint{
		Param:   "allow_blocked",
		Verb:    "peek-prompt",
		Command: "tuios peek-prompt -s " + session + " -w " + shortWindowID(target.ID),
		Detail:  "Nothing was typed. Read the prompt with peek-prompt. Tell the person which pane waits, with tuios ask-human. To answer the prompt yourself, pass --allow-blocked.",
	})
}

func (d *Daemon) recheckBlocked(cs *connState, verb string, sess *Session, windowID string, allowBlocked bool) *verbError {
	if cs == nil || allowBlocked || windowID == "" {
		return nil
	}
	pa := cs.paneView.Load()
	if !pa.inPane() {
		return nil
	}
	target, ok := findWindowState(sess.GetState(), windowID)
	if !ok {
		return nil
	}
	return typingBlocked(verb, pa, target, sess.Name(), false)
}

func (d *Daemon) recheckTyping(cs *connState, verb string, sess *Session, windowID string) *verbError {
	if cs == nil {
		return nil
	}
	pa := cs.paneView.Load()
	if pa == nil {
		return nil
	}
	state := sess.GetState()
	if windowID == "" {
		if !pa.grants.Has(GrantAdmin) {
			return nil
		}
		id, err := focusedWindowID(state)
		if err != nil {
			return nil
		}
		windowID = id
	}
	target, ok := findWindowState(state, windowID)
	if !ok {
		return nil
	}
	if why := d.typingRefusal(pa, target, false); why != "" {
		LogBasic("Pane %s (%s) refused %s: %s", shortWindowID(pa.window), pa.grants.String(), verb, why)
		return grantForbidden(verb, pa, why)
	}
	return nil
}

func paneTypesRaw(cs *connState) bool {
	if cs == nil {
		return false
	}
	pa := cs.paneView.Load()
	return pa != nil && !pa.grants.Has(GrantRespond)
}

func (d *Daemon) refuseTypingInto(cs *connState, sess *Session, ptyID string) string {
	pa := d.paneAuthority(cs)
	if pa == nil {
		return ""
	}
	for _, w := range sess.GetState().Windows {
		if w.PTYID == ptyID {
			if why := d.typingRefusal(pa, w, false); why != "" {
				LogBasic("Pane %s (%s) refused input: %s", shortWindowID(pa.window), pa.grants.String(), why)
				return why
			}
			return ""
		}
	}
	return ""
}

func (d *Daemon) refuseMultifocusInto(cs *connState, sess *Session, command string, windows []string) string {
	if command != string(tape.CommandTypeSetMultifocus) {
		return ""
	}
	pa := d.paneAuthority(cs)
	if pa == nil {
		return ""
	}
	state := sess.GetState()
	for _, w := range windows {
		idx, err := findWindowStateIndex(state.Windows, w)
		if err != nil {
			return "window " + w + " is not in session " + sess.Name()
		}
		if why := d.typingRefusal(pa, state.Windows[idx], false); why != "" {
			return why
		}
	}
	return ""
}

var typingTapeCommands = map[tape.CommandType]bool{
	tape.CommandTypeType: true, tape.CommandTypeEnter: true, tape.CommandTypeSpace: true,
	tape.CommandTypeBackspace: true, tape.CommandTypeDelete: true, tape.CommandTypeTab: true,
	tape.CommandTypeEscape: true, tape.CommandTypeUp: true, tape.CommandTypeDown: true,
	tape.CommandTypeLeft: true, tape.CommandTypeRight: true, tape.CommandTypeHome: true,
	tape.CommandTypeEnd: true, tape.CommandTypeKeyCombo: true, tape.CommandTypeSource: true,
	tape.CommandTypeLoadLayout: true, tape.CommandTypeAction: true, tape.CommandTypePress: true,
	tape.CommandTypeRun: true,
}

func (d *Daemon) refuseTapeTyping(cs *connState, p *ExecuteCommandPayload) string {
	pa := d.paneAuthority(cs)
	if pa == nil || pa.grants.Has(GrantRespond) {
		return ""
	}
	why := "a pane without the respond grant may not press keys or type through run-command, since the client types into whatever pane is focused, and that pane may be waiting on a prompt. Use send-keys or send-text with a window"
	if p.TapeScript != "" {
		for _, c := range tape.NewParser(tape.New(p.TapeScript)).Parse() {
			if typingTapeCommands[c.Type] {
				return why
			}
		}
		return ""
	}
	if ct, ok := tape.ResolveCommandName(p.CommandType); ok && typingTapeCommands[ct] {
		return why
	}
	return ""
}

func (d *Daemon) checkGrantMessage(cs *connState, t MessageType) *verbError {
	if t == MsgHello || t == MsgPing || cs == nil || cs.viaLink || cs.paneOnly {
		return nil
	}
	if cs.paneBound.Load() == nil && cs.pluginBound.Load() == nil && !d.manager.grants.mayMatter() && !d.nativePluginsRun() {
		return nil
	}
	pa := d.paneAuthority(cs)
	if pa == nil || pa.grants.Has(GrantAdmin) {
		return nil
	}
	LogBasic("Pane %s (%s) refused binary message %d", shortWindowID(pa.window), pa.grants.String(), t)
	if t == MsgExecuteCommand {
		return grantForbidden("run-command", pa, "run-command runs a window-manager command through the client protocol, which needs the admin grant. "+
			"To read one window, use tuios get-window or list-windows, which need read")
	}
	return grantForbidden("attach", pa, "the client protocol (attach, input, windows) needs the admin grant")
}

func (d *Daemon) paneMayRespond(cs *connState, target string) bool {
	pa := d.paneAuthority(cs)
	if pa == nil || !pa.grants.Has(GrantRespond) {
		return false
	}
	return d.paneWriteReach(pa, target) == ""
}

func (d *Daemon) launchGrants(cs *connState, requested []string) (*Grants, *verbError) {
	var want *Grants
	if requested != nil {
		g, verr := parseGrantsParam(requested)
		if verr != nil {
			return nil, verr
		}
		want = &g
	}
	if cs != nil && (cs.viaLink || cs.paneOnly) {
		if want != nil && want.Has(GrantRespond) {
			return nil, hintedVerbError(ErrVerbForbidden, "grants: respond cannot be given from another machine", &VerbHint{
				Param:  "grants",
				Detail: "respond answers prompts for the person, so only the person on this machine gives it, with tuios set-pane-grants. Nothing was started.",
			})
		}
		return want, nil
	}
	pa := d.paneAuthority(cs)
	if pa == nil {
		return want, nil
	}
	if want == nil {
		if pa.grants.Has(GrantAdmin) {
			return d.clampDefaultGrants(pa), nil
		}
		g := pa.grants
		return &g, nil
	}
	if !pa.grants.Covers(*want) {
		return nil, hintedVerbError(ErrVerbForbidden, "grants: a pane cannot give more than it holds, and this one holds "+pa.grants.String(), &VerbHint{
			Param:   "grants",
			Verb:    "pane-grants",
			Command: "tuios pane-grants",
			Detail:  "Name grants this pane holds, or omit grants. Nothing was started.",
		})
	}
	return want, nil
}

var grantsParam = verbParam{
	Name:        "grants",
	Type:        "[]string",
	Description: "What the new pane may do through tuios: read, write, fan, respond, admin, or none. Omit for the default of [agents.permissions], or, from a pane without admin, the calling pane's own grants. A pane may give only what it holds, and admin does not include respond. A window on another machine holds what that machine gives it.",
	Accepted:    grantAccepted,
}

func (d *Daemon) permissionMode() string {
	if d.manager.grants.strict() {
		return config.PaneModeStrict
	}
	return config.PaneModeOpen
}

func (d *Daemon) verbPaneGrants(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		PaneID      string `json:"pane_id"`
		PaneToken   string `json:"pane_token"`
		PluginID    string `json:"plugin_id"`
		PluginToken string `json:"plugin_token"`
		PeerPID     *int   `json:"peer_pid"`
		PeerStart   uint64 `json:"peer_start"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.PluginID != "" || p.PluginToken != "" {
		if p.PeerPID != nil || p.PaneID != "" || p.PaneToken != "" {
			return nil, invalidParam("plugin_token", "pass plugin_id and plugin_token alone")
		}
		if verr := d.bindPluginToken(cs, p.PluginID, p.PluginToken); verr != nil {
			return nil, verr
		}
	}
	if p.PeerPID != nil {
		switch {
		case cs != nil && (cs.viaLink || cs.paneOnly):
			return nil, hintedVerbError(ErrVerbForbidden, "peer_pid is answered only on the machine that runs the process", &VerbHint{Param: "peer_pid"})
		case p.PaneID != "" || p.PaneToken != "":
			return nil, invalidParam("peer_pid", "pass peer_pid or pane_id and pane_token, not both")
		case *p.PeerPID <= 0:
			return nil, invalidParam("peer_pid", "peer_pid must be a process id above 0")
		}
		return d.peerGrants(cs, *p.PeerPID, p.PeerStart), nil
	} else if p.PaneID != "" || p.PaneToken != "" {
		if cs.viaLink || cs.paneOnly {
			return nil, hintedVerbError(ErrVerbForbidden, "a pane token is good only on the machine that runs the pane", &VerbHint{Param: "pane_token"})
		}
		window, via, verr := d.placeCaller(cs, p.PaneID, p.PaneToken)
		if verr != nil {
			return nil, verr
		}
		if via == "token" {
			if prev := cs.paneBound.Load(); prev != nil && *prev != window {
				return nil, hintedVerbError(ErrVerbForbidden, "this connection already presented another pane's token", &VerbHint{
					Param:  "pane_id",
					Detail: "A connection is placed in one pane for as long as it is open. Nothing was changed.",
				})
			}
			cs.paneBound.Store(&window)
		}
	}
	out := map[string]any{
		"type":           "pane_grants",
		"mode":           d.permissionMode(),
		"default_grants": d.manager.grants.defaults().Names(),
	}
	if d.configWaiting() {
		out["restart_needed"] = true
		out["restart_note"] = "config.toml gives panes or linked machines more than they have now. That part applies after tuios config apply from outside tuios, or a daemon restart."
	}
	if d.grantsWidenedAtStart.Load() {
		out["widened_at_start"] = true
		out["widened_note"] = grantsWidenedNote
	}
	pa := d.paneAuthority(cs)
	if pa == nil {
		out["pane"] = false
		out["note"] = "The caller runs in no pane of this daemon, so no pane grants apply to it."
		return out, nil
	}
	out["pane"] = true
	if pa.plugin != "" {
		out["pane"], out["plugin"] = false, pa.plugin
	}
	out["window"] = pa.window
	out["session"] = pa.session
	out["via"] = pa.via
	out["grants"] = pa.grants.Names()
	out["explicit"] = pa.explicit
	return out, nil
}

func (d *Daemon) peerGrants(caller *connState, pid int, start uint64) map[string]any {
	probe := &connState{clientID: "peer " + strconv.Itoa(pid), peerPID: pid}
	if start != 0 {
		probe.peerStart, probe.peerStartOK = start, true
	} else {
		probe.peerStart, probe.peerStartOK = procinfo.StartTime(pid)
	}
	out := map[string]any{
		"type":           "pane_grants",
		"peer_pid":       pid,
		"mode":           d.permissionMode(),
		"default_grants": d.manager.grants.defaults().Names(),
	}
	pa := d.paneAuthority(probe)
	if pa == nil {
		out["pane"] = false
		return out
	}
	out["pane"] = true
	asker := d.paneAuthority(caller)
	if asker != nil && !asker.grants.Has(GrantAdmin) && asker.window != pa.window {
		out["admin"] = pa.grants.Has(GrantAdmin)
		return out
	}
	out["window"] = pa.window
	out["session"] = pa.session
	out["via"] = pa.via
	out["grants"] = pa.grants.Names()
	out["explicit"] = pa.explicit
	out["admin"] = pa.grants.Has(GrantAdmin)
	return out
}

func (d *Daemon) verbSetPaneGrants(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string    `json:"session"`
		Window  string    `json:"window"`
		Grants  *[]string `json:"grants"`
		Reset   bool      `json:"reset"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if cs != nil && (cs.viaLink || cs.paneOnly) {
		return nil, hintedVerbError(ErrVerbForbidden, "set-pane-grants is for the machine that runs the pane", &VerbHint{
			Detail: "A pane's grants are set on its own machine, by the person there. Nothing was changed.",
		})
	}
	switch {
	case p.Reset && p.Grants != nil:
		return nil, invalidParam("reset", "pass grants or reset, not both")
	case !p.Reset && p.Grants == nil:
		return nil, invalidParam("grants", "grants is required: the grants to give, or none; or pass reset for the default", grantAccepted...)
	}
	var want *Grants
	if p.Grants != nil {
		g, verr := parseGrantsParam(*p.Grants)
		if verr != nil {
			return nil, verr
		}
		want = &g
	}

	caller := d.paneAuthority(cs)
	if p.Session == "" && caller != nil && caller.session != "" {
		p.Session = caller.session
	}
	sess, verr := d.resolveVerbSession(cs, p.Session)
	if verr != nil {
		return nil, verr
	}
	st := sess.GetState()
	window := p.Window
	if window == "" && caller != nil && caller.session == sess.Name() {
		window = caller.window
	}
	if window == "" {
		return nil, invalidParam("window", "window is required: the pane whose grants to set")
	}
	idx, err := findWindowStateIndex(st.Windows, window)
	if err != nil {
		return nil, mapResolveErr(err, sess)
	}
	w := st.Windows[idx]
	if w.Host != "" {
		return nil, hintedVerbError(ErrVerbInvalidParams, "window "+shortWindowID(w.ID)+" runs on "+w.Host+", and its grants are that machine's", &VerbHint{Param: "window"})
	}
	next := d.manager.grants.defaults()
	if want != nil {
		next = *want
	}
	if caller != nil {
		if w.ID != caller.window && !caller.grants.Has(GrantAdmin) {
			return nil, grantForbidden("set-pane-grants", caller, "a pane without the admin grant may change only its own grants")
		}
		if !caller.grants.Covers(next) {
			return nil, grantForbidden("set-pane-grants", caller, "a pane cannot give more than it holds")
		}
	}
	prev, prevExplicit := d.manager.grants.effective(w.ID)
	if !d.manager.grants.set(w.ID, want) {
		return nil, hintedVerbError(ErrVerbPTYNotFound, "window "+shortWindowID(w.ID)+" has no process of this daemon to hold grants", &VerbHint{Param: "window"})
	}
	_ = sess.mutateState(func(st *SessionState) error {
		for i := range st.Windows {
			if st.Windows[i].ID == w.ID {
				st.Windows[i].Grants = grantNamesPtr(want)
			}
		}
		return nil
	})
	by := "the person"
	if caller != nil {
		by = "pane " + shortWindowID(caller.window)
	}
	LogBasic("Pane %s grants %s -> %s, set by %s", shortWindowID(w.ID), prev.String(), next.String(), by)
	return map[string]any{
		"type":              "pane_grants_set",
		"session":           sess.Name(),
		"window":            w.ID,
		"grants":            next.Names(),
		"explicit":          want != nil,
		"previous":          prev.Names(),
		"previous_explicit": prevExplicit,
	}, nil
}

func (t *paneGrantTable) envValue(windowID string) string {
	g, _ := t.effective(windowID)
	return g.String()
}

func savedGrants(names []string) *Grants {
	if names == nil {
		return nil
	}
	valid, _ := config.CanonicalPaneGrants(names)
	g := grantsFromNames(valid)
	return &g
}

func (t *paneGrantTable) renameSession(old, newName string) {
	if t == nil || old == newName {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for id, e := range t.panes {
		if e.session == old {
			e.session = newName
			t.panes[id] = e
		}
	}
}
