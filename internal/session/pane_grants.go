package session

import (
	"encoding/json"
	"log"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/procinfo"
	"github.com/Gaurav-Gosain/tuios/internal/tape"
)

// Pane grants: what a process in a pane may do through tuios.
//
// Every connection on the daemon socket used to be able to call every verb.
// restrict-connection (conn_scope.go) lets a caller give authority up on its
// own connection, which bounds tuios mcp but not a process that opens a
// connection of its own. Pane grants bound the pane: every JSON verb and
// every binary message from a connection placed in a pane of this daemon is
// checked against what that pane holds, before its handler runs.
//
// The grants:
//
//   - read: read the pane's own session and the sessions of its fan group
//     (sessionInScope): listings, captures, agent state, waits, the event
//     stream, mail and stash reads.
//   - write: type into the panes of its own session and leave mail and
//     stashed files there. A pane types only into panes that hold nothing it
//     does not, since what it types runs with the target's grants, and not
//     into a pane waiting on a prompt unless it holds respond
//     (holdTypingTarget).
//   - fan: what write does, in the sessions of its fan group and the ones it
//     launched, and start agents with fan and start-agent.
//   - respond: answer an on-screen prompt with respond, without the person's
//     attach nonce, on a pane in its reach, and type into a pane waiting on a
//     prompt. Nothing gives it by default, and admin does not imply it.
//   - admin: everything else a pane could do before grants existed, which is
//     every verb on every session, the listings across sessions, and the
//     binary protocol (attach, input). It implies read, write and fan.
//
// Every pane may also report its own state, meta and conversation id, ask
// the person as itself, hold its own approval prompt, and call the verbs that
// touch no session (hello, list-verbs, pane-grants), whatever it holds.
//
// Where a pane's grants come from:
//
//   - The grants it was started with: start-agent, fan and new-window take a
//     grants parameter, and set-pane-grants changes them later.
//   - Otherwise the default of [agents.permissions]: admin under mode open,
//     which is the default mode and exactly what every pane could do before,
//     and the grants list under mode strict.
//
// A pane can never hand out more than it holds. A pane without admin that
// starts an agent without naming grants gives the new pane its own; one that
// names grants must hold them all. Only the person, from outside every pane,
// can give respond to a pane that does not hold it.
//
// How a connection is placed in a pane: the kernel's record of the peer's pid
// first (peerPane, the test resolve-pane uses), then the TUIOS_PANE_ID in that
// process's environment for a pane still being created, and on a platform
// with no peer pid the TUIOS_PANE_ID and TUIOS_PANE_TOKEN the connection
// presents with pane-grants, which the tuios CLI does on its own there. A
// connection placed in no pane is held to nothing new: the person's CLI, the
// attached client, and the hooks and dock components the person configured
// keep full rights. A connection over a link is held to that link's policy
// instead (link_policy.go), and its typing to the prompt rule
// (holdLinkTyping), and a report from a pane this machine runs for
// another machine is held there. A process in such a pane that calls this
// daemon directly holds this machine's default and reaches no session here.
//
// This scopes accidents and prompt-injected agents, not a determined local
// attacker. A process that leaves its pane on purpose (a double fork with a
// cleaned environment, a service manager) is not placed in it by any of these
// tests, and is then treated as the person, as human_origin.go explains for
// the same checks.

// Grants is a set of pane grants.
type Grants uint8

// The grants, one bit each.
const (
	GrantRead Grants = 1 << iota
	GrantWrite
	GrantFan
	GrantRespond
	GrantAdmin
)

// grantNone is the name a caller uses for the empty set.
const grantNone = "none"

var grantByName = map[string]Grants{
	config.PaneGrantRead:    GrantRead,
	config.PaneGrantWrite:   GrantWrite,
	config.PaneGrantFan:     GrantFan,
	config.PaneGrantRespond: GrantRespond,
	config.PaneGrantAdmin:   GrantAdmin,
}

// grantAccepted is what a grants parameter accepts.
var grantAccepted = append(append([]string{}, config.PaneGrantNames...), grantNone)

// grantsFromNames reads canonical names. Unknown names are ignored: callers
// validate first.
func grantsFromNames(names []string) Grants {
	var g Grants
	for _, n := range names {
		g |= grantByName[n]
	}
	return g
}

// parseGrantsParam reads a grants parameter: names from PaneGrantNames, or
// none alone for no grants at all.
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

// expand adds what admin implies.
func (g Grants) expand() Grants {
	if g&GrantAdmin != 0 {
		g |= GrantRead | GrantWrite | GrantFan
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
	return out
}

// String is Names joined with commas, or none.
func (g Grants) String() string {
	if g == 0 {
		return grantNone
	}
	return strings.Join(g.Names(), ",")
}

// panePolicy is [agents.permissions] as the daemon uses it.
type panePolicy struct {
	strict bool
	// grants is the default under strict.
	grants Grants
}

// paneGrantEntry is one local pane the table knows.
type paneGrantEntry struct {
	ptyID   string
	session string
	// grants is what the pane was given, nil when it was given nothing and
	// holds the default.
	grants *Grants
}

// paneGrantTable holds the grants of every pane this daemon runs locally,
// keyed by window id. A pane is entered before its process starts and leaves
// when the process exits, so a process is never placed in a pane the table
// does not know. The manager owns one and stamps it into every session.
type paneGrantTable struct {
	mu       sync.RWMutex
	panes    map[string]paneGrantEntry
	explicit int
	policy   atomic.Pointer[panePolicy]
	// restartNeeded is set when a config reload widened the default and
	// only what it narrowed was applied (reloadPanePermissions).
	restartNeeded atomic.Bool
}

func newPaneGrantTable() *paneGrantTable {
	return &paneGrantTable{panes: make(map[string]paneGrantEntry)}
}

// setPolicy replaces [agents.permissions].
func (t *paneGrantTable) setPolicy(r config.ResolvedPermissions) {
	if t == nil {
		return
	}
	t.policy.Store(&panePolicy{strict: r.Strict, grants: grantsFromNames(r.Grants)})
}

// strict reports whether mode is strict.
func (t *paneGrantTable) strict() bool {
	if t == nil {
		return false
	}
	p := t.policy.Load()
	return p != nil && p.strict
}

// defaults is what a pane given nothing holds: admin under open, the grants
// list under strict.
func (t *paneGrantTable) defaults() Grants {
	if t == nil {
		return GrantAdmin
	}
	if p := t.policy.Load(); p != nil && p.strict {
		return p.grants
	}
	return GrantAdmin
}

// strictDefaults is what a pane given nothing holds under strict: the grants
// list of [agents.permissions], whatever the mode.
func (t *paneGrantTable) strictDefaults() Grants {
	if t != nil {
		if p := t.policy.Load(); p != nil {
			return p.grants
		}
	}
	return grantsFromNames(config.DefaultStrictGrants)
}

// mayMatter reports whether any pane holds less than admin, which is when a
// call has to be placed at all. Under open with no pane given grants of its
// own every pane holds admin, and the check costs nothing.
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

// add enters a pane before its process starts. A window that already holds
// grants of its own and gets a new process with none named keeps them, so a
// client asking for a new process in a window cannot reset what the window
// was given.
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

// remove drops a pane whose process with ptyID exited. A pane that has had
// another process entered since is left alone.
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

// lookup returns the entry for a pane.
func (t *paneGrantTable) lookup(windowID string) (paneGrantEntry, bool) {
	if t == nil {
		return paneGrantEntry{}, false
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	e, ok := t.panes[windowID]
	return e, ok
}

// effective is what a pane holds now, and whether it was given that rather
// than holding the default.
func (t *paneGrantTable) effective(windowID string) (Grants, bool) {
	if e, ok := t.lookup(windowID); ok && e.grants != nil {
		return *e.grants, true
	}
	return t.defaults(), false
}

// set gives a known pane grants, or the default for nil. It reports false
// for a pane the table does not know.
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

// grantNamesPtr is the WindowState form of g: nil for the default, and none
// for no grants, because JSON's omitempty and gob both drop an empty list and
// it would come back as the default.
func grantNamesPtr(g *Grants) []string {
	if g == nil {
		return nil
	}
	if *g == 0 {
		return []string{grantNone}
	}
	return g.Names()
}

// recordedGrants is what the session's record says window was given, nil when
// the window holds the default or is not in the record.
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

// policyDefaults is what a pane given nothing holds under r.
func policyDefaults(r config.ResolvedPermissions) Grants {
	if !r.Strict {
		return GrantAdmin
	}
	return grantsFromNames(r.Grants)
}

// reloadPanePermissions applies [agents.permissions] read again from a
// changed config file. Only what narrows the default applies now.
//
// The file is the user's, and a process in a pane runs as the user, so it can
// write the file. If a reload could widen the default, a pane held to strict
// could set mode = "open" and hold admin at its next call. So a reload that
// widens the default, in whole or in part, applies only what it takes away,
// and the rest waits for the daemon to restart, when the file is read as the
// person left it. A confirmation from an attached client is not asked for:
// a pane that holds admin can send keys to that client, so a confirmation
// there proves nothing a restart does not.
func (d *Daemon) reloadPanePermissions(r config.ResolvedPermissions) {
	t := d.manager.grants
	cur, want := t.defaults(), policyDefaults(r)
	if cur.Covers(want) {
		// Nothing widens: the new default holds nothing the one in force
		// does not.
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

// paneAuth is the pane a caller runs in and what it holds.
type paneAuth struct {
	window  string
	session string
	// sessionID is the session's ID, empty when it was found only through
	// the grant table. A stream reads the session's current name through it.
	sessionID string
	// via is how the pane was found: pid from the kernel, env from the
	// process's TUIOS_PANE_ID, token from a presented TUIOS_PANE_TOKEN.
	via      string
	grants   Grants
	explicit bool
	// hosted marks a process in a pane this machine runs for another
	// machine. It has no session here.
	hosted bool
	// linkTable marks a typing caller over a link that the hub did not vouch
	// for as the person (linkTypingAuthority). It names the [hosts] table
	// whose allow list holds the caller, for the refusal to point at.
	linkTable string
}

// addressesHostedPane reports whether a call is one forwardHostedCall sends on
// to the machine that owns a hosted pane: a report verb whose address names a
// pane this machine runs for another machine.
func (d *Daemon) addressesHostedPane(verb string, params json.RawMessage) bool {
	field, ok := hostedCallVerbs[verb]
	if !ok || len(params) == 0 {
		return false
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(params, &fields) != nil {
		return false
	}
	var addr string
	if raw, ok := fields[field]; !ok || json.Unmarshal(raw, &addr) != nil {
		return false
	}
	return d.hostedPaneByAddress(addr) != nil
}

// hostedPaneOfPeer names the hosted pane the caller on cs runs in, by its
// process ancestry, or "" for none. It is asked only for a caller the pane
// tests place inside this daemon's panes but in none of its sessions' panes.
func (d *Daemon) hostedPaneOfPeer(cs *connState) string {
	if d.hostedPeer != nil {
		return d.hostedPeer(cs)
	}
	if d.approvalPeer.Load() != nil || cs.peerPID <= 0 || !d.connFromPane(cs) {
		return ""
	}
	pids := make(map[int]string)
	d.hostedPanesMu.Lock()
	for id, hp := range d.hostedPanes {
		if hp.cmd != nil && hp.cmd.Process != nil {
			pids[hp.cmd.Process.Pid] = id
		}
	}
	d.hostedPanesMu.Unlock()
	if len(pids) == 0 {
		return ""
	}
	for cur, depth := cs.peerPID, 0; depth < paneOriginMaxDepth && cur > 1; depth++ {
		if id, ok := pids[cur]; ok {
			return id
		}
		ppid, _, ok := readProcLineage(cur)
		if !ok {
			break
		}
		cur = ppid
	}
	return ""
}

// paneWriteReach reports why the pane may not type into session target, or ""
// when it may: its own session with write, a session of its fan group with
// fan, and any session with admin.
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

// paneAuthority places the caller on cs in a pane of this daemon and says
// what that pane holds. It returns nil for a caller held to nothing new: one
// placed in no pane, one over a link, and a call from a pane this machine
// runs for another machine. See the file comment.
func (d *Daemon) paneAuthority(cs *connState) *paneAuth {
	if cs == nil || cs.viaLink || cs.paneOnly {
		return nil
	}
	if cs.paneBound.Load() == nil && d.approvalPeer.Load() == nil && !d.connFromPane(cs) {
		// Placed outside every pane when it connected (pinPeer). A caller
		// on each keystroke, such as the person's client, costs no read.
		return nil
	}
	if cs.paneBound.Load() == nil && cs.panePlaced.Load() == nil && d.peerChanged(cs) {
		// The process that connected is gone, and its pid is free or names
		// another process. Nothing read about the pid now is about the
		// caller. A process that connects, hands the socket to a child and
		// exits looks like this. It is held to the strict default in no
		// session, which reaches only the verbs every pane may call.
		return &paneAuth{window: unplacedWindow, via: "pid", grants: d.manager.grants.strictDefaults()}
	}
	window, via := d.placePaneWindow(cs)
	if window == "" {
		// A process in a pane this machine runs for another machine
		// belongs to no session here. Its reports go to the owner through
		// the report channel; a call it makes here directly holds this
		// machine's default and reaches no session. Under open that is
		// admin, as before.
		if id := d.hostedPaneOfPeer(cs); id != "" {
			return &paneAuth{window: "hosted:" + id, via: "pid", grants: d.manager.grants.defaults(), hosted: true}
		}
		if d.connFromPane(cs) && cs.fromPaneWhy != paneOriginEnv {
			// The kernel counts the caller as inside this daemon's panes (a
			// child of the daemon, a pane's terminal, or a process that
			// cannot be read) and no pane is found for it. A process the
			// daemon starts outside every pane shell looks like this: a
			// hook, git, or ssh and whatever ssh runs, such as a
			// ProxyCommand. It holds what a pane given nothing holds, in no
			// session: under strict the grants list, which reaches only the
			// verbs every pane may call, and under open admin, which every
			// pane on the default holds already.
			//
			// A caller placed only by its environment passes, as before: the
			// client's hooks and dock components carry a window id or the
			// socket, and a process can set its environment to anything, so
			// the environment is no boundary to hold it at.
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
		// A pane whose window is not in its session's state yet: the
		// process started before the window was recorded.
		if e, ok := table.lookup(window); ok {
			session = e.session
		}
	}
	return &paneAuth{window: window, session: session, sessionID: sessionID, via: via, grants: g, explicit: explicit}
}

// unplacedWindow names the pane of a caller whose process cannot be read.
const unplacedWindow = "unknown"

// placePaneWindow finds the pane the caller on cs runs in, "" for none. A
// connection that presented a pane's token is in that pane. Otherwise the
// kernel's answer is asked for and kept once it names a pane, because a
// process does not move between panes.
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
		// A process in a pane whose window is not recorded yet names it in
		// its environment. Only a pane the grant table holds counts, which
		// is every local pane from before its process started.
		if id, ok := readProcEnvVar(cs.peerPID, "TUIOS_PANE_ID"); ok && id != "" {
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

// grantKind is what a verb needs from a pane: its restricted-connection class
// (verbScopes), except for the verbs a pane calls about itself that a
// restricted connection is not given.
func grantKind(verb string) scopeKind {
	switch verb {
	case "request-approval":
		// A harness hook holds its own pane's prompt. The handler refuses any
		// other pane already; here it is a report about the caller's pane.
		return scopeSelf
	case "resolve-pane", "pane-grants", "set-pane-grants":
		// resolve-pane is how a hook finds its own pane. pane-grants reads
		// the caller's own grants, and set-pane-grants checks its caller in
		// its handler.
		return scopeOpen
	}
	kind, ok := verbScopes[verb]
	if !ok {
		return scopeDeny
	}
	return kind
}

// grantForbidden is the refusal for a call a pane's grants do not cover.
func grantForbidden(verb string, pa *paneAuth, why string) *verbError {
	if pa.linkTable != "" {
		return hintedVerbError(ErrVerbForbidden, verb+" is refused for this link: "+why, &VerbHint{
			Detail: "Nothing was done. The call came over a link from a pane on the other machine, or from a machine that does not say. " +
				"Pane grants do not travel over a link, so the link policy holds the call. " +
				"To let such calls answer prompts, add \"respond\" to allow in " + pa.linkTable + " in the config on this machine.",
		})
	}
	source := "the default of [agents.permissions]"
	if pa.explicit {
		source = "the grants it was given"
	}
	return hintedVerbError(ErrVerbForbidden, verb+" is refused for this pane: "+why, &VerbHint{
		Verb:    "pane-grants",
		Command: "tuios pane-grants",
		Detail: "Pane " + shortWindowID(pa.window) + " holds " + pa.grants.String() + ", " + source + ". Nothing was done. " +
			"The person can give this pane more with tuios set-pane-grants -w " + shortWindowID(pa.window) + " --grants <names>, " +
			"or every pane started with none with mode and grants under [agents.permissions] in config.toml.",
	})
}

// checkGrants holds a call from a pane to what the pane holds. It returns the
// params the handler should see, with the caller's own session and window
// filled in where the call left them out, or the refusal. A caller held to
// nothing new (paneAuthority is nil) and a pane holding admin pass unchanged.
func (d *Daemon) checkGrants(cs *connState, verb string, params json.RawMessage) (json.RawMessage, *verbError) {
	if cs != nil && cs.viaLink && typingVerbs[verb] {
		return d.holdLinkTyping(cs, verb, params)
	}
	if cs == nil || cs.viaLink || cs.paneOnly {
		return params, nil
	}
	// Under open with no pane given grants every pane holds admin, and only a
	// typing verb has anything to check: whether it answers another pane's
	// prompt (holdTypingTarget).
	if cs.paneBound.Load() == nil && !d.manager.grants.mayMatter() && !typingVerbs[verb] {
		return params, nil
	}
	pa := d.paneAuthority(cs)
	if pa == nil {
		cs.paneView.Store(nil)
		return params, nil
	}
	cs.paneView.Store(pa)
	deny := func(why string) *verbError {
		LogBasic("Pane %s (%s) refused %s: %s", shortWindowID(pa.window), pa.grants.String(), verb, why)
		return grantForbidden(verb, pa, why)
	}
	if pa.grants.Has(GrantAdmin) {
		if typingVerbs[verb] {
			return d.holdTypingTarget(verb, pa, params, deny)
		}
		return params, nil
	}
	kind := grantKind(verb)
	if pa.hosted {
		// A report as the hosted pane is sent on to the machine that owns
		// its window, which holds it to that pane (forwardHostedCall, which
		// also checks the caller is in the pane). Nothing else reaches any
		// session here.
		if kind == scopeOpen || d.addressesHostedPane(verb, params) {
			return params, nil
		}
		return nil, deny("the pane runs here for another machine and has no session on this one; its reports go to that machine")
	}
	var reach func(target string) string
	switch kind {
	case scopeOpen:
		return params, nil
	case scopeDeny, scopeGlobal:
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
			if d.sessionInScope(pa.session, target) {
				return ""
			}
			return "session " + echoName(target) + " is not the pane's own session or in its fan group, and reading it needs the admin grant"
		}
	case scopeLaunch:
		if !pa.grants.Has(GrantFan) {
			return nil, deny(verb + " starts agents, which needs the fan grant")
		}
		reach = func(target string) string {
			if d.sessionInScope(pa.session, target) {
				return ""
			}
			return "session " + echoName(target) + " is not the pane's own session or in its fan group, and starting agents there needs the admin grant"
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
	return d.holdTypingTarget(verb, pa, out, deny)
}

// holdLinkTyping holds a typing call over a link to the rule a pane here is
// held to (typingRefusal). The link policy already said the peer may write.
// It did not say whether the caller may answer a prompt: keys typed into a
// pane on needs_input answer it, which on this machine needs respond.
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
	return d.holdTypingTarget(verb, pa, params, deny)
}

// linkTypingAuthority is what a typing caller over a link holds here, or nil
// for the person. A stream the hub vouched for (linkHuman) is the person on
// the hub, typing from outside every pane there, and is held to nothing new.
//
// Any other link stream comes from a pane on the hub, from the hub daemon
// itself, or from a hub too old to vouch. The caller's own pane grants do not
// travel over the link, so the check fails closed: the caller holds admin,
// which the link policy's write capability already reaches here, and holds
// respond only when the link policy allows respond. Such a caller cannot
// answer another pane's prompt by typing unless the person on this machine
// let the link answer prompts.
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

// typingVerbs are the verbs that type into one pane: what they type runs with
// whatever the process in that pane may do, so the target pane is checked as
// well as its session (holdTypingTarget). respond is not here: it answers a
// prompt the target shows, and the respond grant is the person's consent to
// that. fan and start-agent type only into the panes they start, which never
// hold more than their caller (launchGrants).
//
// queue-prompt and send-review type later, through the delivery queue. They
// are held here at the call the same way, and the queue checks the caller's
// grants against the target again when it types.
var typingVerbs = map[string]bool{
	"send-text":    true,
	"send-keys":    true,
	"ask-agent":    true,
	"run":          true,
	"queue-prompt": true,
	"send-review":  true,
}

// holdTypingTarget holds a typing call from a pane without admin to the pane
// it types into. The session check lets a pane type into its own session, but
// a sibling pane there can hold more than the caller: a shell on the open
// default holds admin, and text typed into it runs with admin. So the target
// must hold nothing the caller does not (typingRefusal).
//
// The call's window is pinned to the id it resolves to now, so a focus change
// between this check and the handler cannot send the text somewhere else. A
// target that does not resolve is left for the handler to report.
func (d *Daemon) holdTypingTarget(verb string, pa *paneAuth, params json.RawMessage, deny func(string) *verbError) (json.RawMessage, *verbError) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(params, &m); err != nil || m == nil {
		return params, nil
	}
	str := func(name string) string {
		var s string
		if raw, ok := m[name]; ok {
			_ = json.Unmarshal(raw, &s)
		}
		return s
	}
	sess := d.findTargetSession(str("session"))
	if sess == nil {
		return params, nil
	}
	window := str("window")
	if verb == "ask-agent" && (window == "" || window == AgentInboxHuman) {
		// ask-agent needs a window, and human is an inbox, not a pane. The
		// handler answers both without typing.
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
		_ = json.Unmarshal(raw, &allowBlocked)
	}
	// ask-agent refuses a pane on needs_input itself, with agent_blocked, and
	// checks again right before it types. Only allow_blocked takes it past.
	blockedChecked := verb == "ask-agent" && !allowBlocked
	if pa.grants.Has(GrantAdmin) && (verb == "queue-prompt" || verb == "send-review") {
		// The queue types only into a pane at rest (agentReady), never into
		// a prompt, so queuing for a pane on a prompt answers nothing.
		blockedChecked = true
	}
	if why := d.typingRefusal(pa, target, blockedChecked); why != "" {
		return nil, deny(why)
	}
	if pa.grants.Has(GrantAdmin) {
		// A pane holding admin is only checked, not pinned: with no window,
		// its send-keys still goes through the attached client.
		return params, nil
	}
	raw, _ := json.Marshal(target.ID)
	m["window"] = raw
	out, err := json.Marshal(m)
	if err != nil {
		return nil, newVerbError(ErrVerbInternal, "could not encode params")
	}
	return out, nil
}

// typingRefusal says why the pane pa may not type into target, or "" when it
// may. A pane may always type into itself: it could write to its own terminal
// anyway. Into any other pane it may type only when that pane holds nothing
// pa does not, and, unless blockedChecked says the verb checks this itself,
// only when that pane is not waiting on a prompt or pa holds respond, since
// keys typed into a prompt answer it.
//
// The prompt rule holds a pane with admin too. Admin is what every pane holds
// under the default open mode, and admin does not imply respond: without the
// rule one agent could answer another agent's permission prompt by typing
// "1" and Enter into it. The person answers it, or gives respond.
func (d *Daemon) typingRefusal(pa *paneAuth, target WindowState, blockedChecked bool) string {
	if target.ID == pa.window {
		return ""
	}
	held, _ := d.manager.grants.effective(target.ID)
	if !pa.grants.Has(GrantAdmin) && !pa.grants.Covers(held) {
		return "window " + shortWindowID(target.ID) + " holds " + held.String() + ", more than this pane holds, and what is typed there runs with that. " +
			"A pane types only into panes that hold nothing it does not"
	}
	if !pa.grants.Has(GrantRespond) && d.agentsOff.Load() {
		// With the agent features off nothing knows which pane waits on a
		// prompt, so the rule fails closed: every other pane may be on one.
		// blockedChecked does not help, since the check it stands for reads
		// the same missing state. See agents_switch.go.
		return "the agent features are off, so tuios cannot see whether window " + shortWindowID(target.ID) +
			" is waiting on a prompt. Typing into another pane needs the respond grant"
	}
	if !blockedChecked && target.AgentState == AgentStateNeedsInput && !pa.grants.Has(GrantRespond) {
		return "window " + shortWindowID(target.ID) + " is waiting on a prompt, and what is typed now would answer it, which needs the respond grant"
	}
	return ""
}

// recheckTyping repeats the target check right before a typing verb writes,
// for the caller whose checked call this is: the target may have come to a
// prompt, or been given more, since checkGrants ran. It returns nil for a
// caller held to nothing new. A window of "" is the focused one.
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
			// checkGrants pinned the window of a pane without admin.
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

// paneTypesRaw reports whether a send-keys from the caller on cs must go to
// the target's terminal as bytes rather than through the attached client. The
// client reads keys as the person's, so the prefix key there drives the window
// manager: it opens, closes and focuses panes, which only admin may do. It
// also moves focus to another pane and opens the Inbox, so a sequence such as
// PREFIX, next window, 1, Enter answers a prompt that no check before the
// keys could see. So every pane without respond types to a terminal, admin
// included, and only the pane the keys go to is checked.
func paneTypesRaw(cs *connState) bool {
	if cs == nil {
		return false
	}
	pa := cs.paneView.Load()
	return pa != nil && !pa.grants.Has(GrantRespond)
}

// refuseTypingInto checks input the caller on cs writes into the pane with
// ptyID through the client protocol (MsgInput): a pane may not answer another
// pane's prompt that way either. It returns "" when the write may go ahead.
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

// refuseMultifocusInto checks a SetMultifocus from the caller on cs. The
// windows it names get every key the person types in the focused pane, so a
// pane may name only the windows it may type into itself, as with send-keys.
// It returns "" when the command may run.
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

// typingTapeCommands are the tape commands that press keys or type, in the
// focused pane or the window manager, and the ones that do so by other means:
// Source runs another tape file, and LoadLayout types a cd line into panes.
var typingTapeCommands = map[tape.CommandType]bool{
	tape.CommandTypeType: true, tape.CommandTypeEnter: true, tape.CommandTypeSpace: true,
	tape.CommandTypeBackspace: true, tape.CommandTypeDelete: true, tape.CommandTypeTab: true,
	tape.CommandTypeEscape: true, tape.CommandTypeUp: true, tape.CommandTypeDown: true,
	tape.CommandTypeLeft: true, tape.CommandTypeRight: true, tape.CommandTypeHome: true,
	tape.CommandTypeEnd: true, tape.CommandTypeKeyCombo: true, tape.CommandTypeSource: true,
	tape.CommandTypeLoadLayout: true,
}

// refuseTapeTyping checks a run-command from the caller on cs. The attached
// client runs it as the person, in whatever pane is focused when each command
// runs, so a pane without respond may not press keys or type through it: a
// focus change earlier in the script, or in another call, would aim them at a
// pane waiting on a prompt. It returns "" when the command may run.
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

// checkGrantMessage holds a binary protocol message from a pane to its
// grants. The binary protocol is the attached client's: it reads and types
// into every pane of a session, creates and closes windows and kills
// sessions, so only a pane holding admin may use it past the hello.
func (d *Daemon) checkGrantMessage(cs *connState, t MessageType) *verbError {
	if t == MsgHello || cs == nil || cs.viaLink || cs.paneOnly {
		return nil
	}
	if cs.paneBound.Load() == nil && !d.manager.grants.mayMatter() {
		return nil
	}
	pa := d.paneAuthority(cs)
	if pa == nil || pa.grants.Has(GrantAdmin) {
		return nil
	}
	LogBasic("Pane %s (%s) refused binary message %d", shortWindowID(pa.window), pa.grants.String(), t)
	if t == MsgExecuteCommand {
		// tuios run-command sends this, and so did tuios get-window before
		// the get-window verb. Name what the caller ran, not attach.
		return grantForbidden("run-command", pa, "run-command runs a window-manager command through the client protocol, which needs the admin grant. "+
			"To read one window, use tuios get-window or list-windows, which need read")
	}
	return grantForbidden("attach", pa, "the client protocol (attach, input, windows) needs the admin grant")
}

// paneMayRespond reports whether the caller is a pane holding respond that may
// answer a prompt in session target. It is the second way past respond's
// check for the person, after an attach nonce.
func (d *Daemon) paneMayRespond(cs *connState, target string) bool {
	pa := d.paneAuthority(cs)
	if pa == nil || !pa.grants.Has(GrantRespond) {
		return false
	}
	return d.paneWriteReach(pa, target) == ""
}

// launchGrants decides what a pane that cs starts holds. requested is the
// call's grants parameter, nil when it named none. The result is nil for a
// pane that holds the default.
//
// The person, from outside every pane, may give anything. A pane may give
// only what it holds, and a pane without admin that names nothing gives the
// new pane its own grants, so starting an agent never widens anything. A
// machine over a link, and a pane run for another machine, may not give
// respond, which answering for the person needs.
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
			return nil, nil
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

// grantsParam is the grants parameter of the verbs that start a pane.
var grantsParam = verbParam{
	Name:        "grants",
	Type:        "[]string",
	Description: "What the new pane may do through tuios: read, write, fan, respond, admin, or none. Omit for the default of [agents.permissions], or, from a pane without admin, the calling pane's own grants. A pane may give only what it holds, and admin does not include respond. A window on another machine holds what that machine gives it.",
	Accepted:    grantAccepted,
}

// permissionMode names the permission mode.
func (d *Daemon) permissionMode() string {
	if d.manager.grants.strict() {
		return config.PaneModeStrict
	}
	return config.PaneModeOpen
}

// verbPaneGrants reports what the caller may do. With pane_id and pane_token
// it also places a connection the kernel cannot place.
func (d *Daemon) verbPaneGrants(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		PaneID    string `json:"pane_id"`
		PaneToken string `json:"pane_token"`
		PeerPID   *int   `json:"peer_pid"`
		PeerStart uint64 `json:"peer_start"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.PeerPID != nil {
		// Another process asked about: the holder of a tmux shim pane asks
		// for the process on its socket, which does not go through the
		// daemon. It is placed exactly as a connection from it would be.
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
	out["window"] = pa.window
	out["session"] = pa.session
	out["via"] = pa.via
	out["grants"] = pa.grants.Names()
	out["explicit"] = pa.explicit
	return out, nil
}

// peerGrants answers pane-grants for the process with pid. start is its start
// time as the asker read it when the process connected to it, 0 for none: a
// pid whose process has another start time now is answered as a process that
// cannot be read, since it is not the one that connected.
//
// The answer names the pane and its grants only to a caller outside every
// pane, a pane holding admin, or the pane the process runs in itself, which is
// the tmux shim's holder asking about a process in its own pane. Any other
// pane is told only whether the process runs in a pane and holds admin, which
// is all the holder's check needs.
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

// verbSetPaneGrants gives a pane grants, or with reset the default.
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

	// From a pane, a call that names no session means the pane's own, not
	// the most recently active one.
	caller := d.paneAuthority(cs)
	if p.Session == "" && caller != nil && caller.session != "" {
		p.Session = caller.session
	}
	sess, verr := d.resolveVerbSession(p.Session)
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

// envValue is TUIOS_PANE_GRANTS for a pane: what it holds when it starts,
// for a script to read. The daemon's answer to pane-grants is the one that
// counts, because grants can change after the process started.
func (t *paneGrantTable) envValue(windowID string) string {
	g, _ := t.effective(windowID)
	return g.String()
}

// savedGrants reads the grants a saved window was given, nil for none. A name
// this build does not know is dropped, which only ever narrows.
func savedGrants(names []string) *Grants {
	if names == nil {
		return nil
	}
	valid, _ := config.CanonicalPaneGrants(names)
	g := grantsFromNames(valid)
	return &g
}

// renameSession moves the panes of the session named old to newName, so the
// table still names the session a pane is in after a rename.
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
