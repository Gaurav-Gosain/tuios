package session

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestVerbScopesNameEveryVerb holds the scope table to the registry. A verb the
// table does not name is refused on every restricted connection, which is the
// safe default and also a silent one: this makes adding a verb a decision about
// what a restricted caller may do with it.
func TestVerbScopesNameEveryVerb(t *testing.T) {
	for name := range verbRegistry {
		if _, ok := verbScopes[name]; !ok {
			t.Errorf("verb %q has no entry in verbScopes", name)
		}
	}
	for name := range verbScopes {
		if _, ok := verbRegistry[name]; !ok {
			t.Errorf("verbScopes names %q, which is not a verb", name)
		}
	}
}

// scopeFixture is a daemon with two sessions, a with two windows and b with
// one, and the ids of those windows.
func scopeFixture(t *testing.T) (d *Daemon, sp, a1, a2, b1 string) {
	t.Helper()
	d, sp = startTestDaemon(t)
	sa := makeSessionWithWindow(t, d, "a")
	w2, err := sa.AddDaemonWindow("Second", nil)
	if err != nil {
		t.Fatal(err)
	}
	sb := makeSessionWithWindow(t, d, "b")
	return d, sp, sa.GetState().Windows[0].ID, w2.ID, sb.GetState().Windows[0].ID
}

func restrict(t *testing.T, c *verbConn, params map[string]any) map[string]any {
	t.Helper()
	return result(t, c.call(t, `{"id":1,"verb":"restrict-connection","params":`+jsonParams(params)+`}`))
}

func callP(c *verbConn, t *testing.T, verb string, params map[string]any) map[string]any {
	t.Helper()
	return c.call(t, `{"id":1,"verb":"`+verb+`","params":`+jsonParams(params)+`}`)
}

func wantForbidden(t *testing.T, what string, resp map[string]any) {
	t.Helper()
	if code := errCode(t, resp); code != ErrVerbForbidden {
		t.Errorf("%s answered %s, want forbidden", what, code)
	}
}

func TestRestrictConnectionOwnScopeReachesOnlyTheCallersSession(t *testing.T) {
	d, sp, a1, a2, b1 := scopeFixture(t)
	d.approvalPeer = func(*connState) (bool, string) { return true, a1 }

	c := dialVerb(t, sp)
	res := restrict(t, c, map[string]any{"scope": "own"})
	if res["session"] != "a" || res["window"] != a1 || res["via"] != "pid" || res["scope"] != "own" {
		t.Fatalf("restrict = %v, want session a, window a1 by pid", res)
	}
	if got := res["sessions"].([]any); len(got) != 1 || got[0] != "a" {
		t.Errorf("sessions = %v, want [a]", got)
	}

	// An omitted session is the caller's own, not the most recently active
	// one: b was made last and is the most recently active.
	listed := result(t, callP(c, t, "list-agents", nil))
	if listed["session"] != "a" {
		t.Errorf("list-agents with no session listed %v, want a", listed["session"])
	}
	wantForbidden(t, "list-windows of b", callP(c, t, "list-windows", map[string]any{"session": "b"}))
	wantForbidden(t, "capture-pane of b", callP(c, t, "capture-pane", map[string]any{"session": "b", "window": b1}))
	wantForbidden(t, "list-sessions", callP(c, t, "list-sessions", nil))
	wantForbidden(t, "list-agents all_sessions", callP(c, t, "list-agents", map[string]any{"all_sessions": true}))
	wantForbidden(t, "wait-for any_session", callP(c, t, "wait-for", map[string]any{"condition": "agent-state", "any_session": true, "until": "idle", "timeout": 10}))
	wantForbidden(t, "new-window", callP(c, t, "new-window", map[string]any{"session": "a"}))
	wantForbidden(t, "kill-session", callP(c, t, "kill-session", map[string]any{"session": "b"}))
	wantForbidden(t, "send-text into b", callP(c, t, "send-text", map[string]any{"session": "b", "window": b1, "text": "x"}))

	// Writing into another pane of its own session is allowed: the
	// connection is not read-only.
	result(t, callP(c, t, "send-text", map[string]any{"window": a2, "text": "echo scoped\r"}))

	// A self report with no window lands on the caller's own pane, and one
	// naming another pane is refused.
	result(t, callP(c, t, "set-agent-state", map[string]any{"state": "working"}))
	if st := d.manager.GetSession("a").GetState(); st.Windows[0].AgentState != AgentStateWorking {
		t.Errorf("the caller's pane is %v after its own report, want working", st.Windows[0].AgentState)
	}
	wantForbidden(t, "set-agent-state on another pane", callP(c, t, "set-agent-state", map[string]any{"window": a2, "state": "idle"}))
	wantForbidden(t, "set-agent-state in b", callP(c, t, "set-agent-state", map[string]any{"session": "b", "window": b1, "state": "idle"}))

	// Mail goes out as the caller's own pane.
	sent := result(t, callP(c, t, "send-agent-message", map[string]any{"to": a2, "text": "hi"}))
	if sent["from"] != a1 {
		t.Errorf("mail from = %v, want the caller's pane %s", sent["from"], a1)
	}
	wantForbidden(t, "mail from another pane", callP(c, t, "send-agent-message", map[string]any{"to": a1, "from": a2, "text": "spoof"}))
	wantForbidden(t, "reading another pane's inbox", callP(c, t, "read-agent-messages", map[string]any{"to": a2}))
	// A session on another machine is never in reach, even one named like
	// the caller's own.
	wantForbidden(t, "mail to another machine", callP(c, t, "send-agent-message", map[string]any{"host": "build", "session": "a", "to": a2, "text": "out"}))
	// The link and held-mail verbs are not for a restricted caller.
	wantForbidden(t, "close-pane", callP(c, t, "close-pane", map[string]any{"pane": "f2c1"}))
	wantForbidden(t, "release-agent-message", callP(c, t, "release-agent-message", map[string]any{"session": "a", "id": 1}))

	// An unrestricted connection is untouched.
	plain := dialVerb(t, sp)
	result(t, callP(plain, t, "list-windows", map[string]any{"session": "b"}))
	result(t, callP(plain, t, "list-sessions", nil))
}

func TestRestrictConnectionReadOnlyRefusesTyping(t *testing.T) {
	d, sp, a1, a2, _ := scopeFixture(t)
	d.approvalPeer = func(*connState) (bool, string) { return true, a1 }
	c := dialVerb(t, sp)
	restrict(t, c, map[string]any{"scope": "own", "read_only": true})

	wantForbidden(t, "send-text", callP(c, t, "send-text", map[string]any{"window": a2, "text": "x"}))
	wantForbidden(t, "send-keys", callP(c, t, "send-keys", map[string]any{"window": a2, "keys": "Enter"}))
	wantForbidden(t, "ask-agent", callP(c, t, "ask-agent", map[string]any{"window": a2, "text": "x"}))
	wantForbidden(t, "respond", callP(c, t, "respond", map[string]any{"window": a2, "action": "approve"}))
	wantForbidden(t, "fan", callP(c, t, "fan", map[string]any{"count": 1, "agent": "claude", "prompt": "x", "repo": "/"}))
	// Reporting its own state, and reading, still work.
	result(t, callP(c, t, "set-agent-state", map[string]any{"state": "idle"}))
	result(t, callP(c, t, "capture-pane", map[string]any{"window": a2}))

	// read_only by itself, over every session.
	all := dialVerb(t, sp)
	restrict(t, all, map[string]any{"scope": "all", "read_only": true})
	result(t, callP(all, t, "list-windows", map[string]any{"session": "b"}))
	result(t, callP(all, t, "list-sessions", nil))
	wantForbidden(t, "send-text under read_only", callP(all, t, "send-text", map[string]any{"session": "a", "window": a2, "text": "x"}))
	wantForbidden(t, "close-window under read_only", callP(all, t, "close-window", map[string]any{"session": "a", "window": a2}))
}

func TestRestrictConnectionNeverWidens(t *testing.T) {
	d, sp, a1, _, b1 := scopeFixture(t)
	d.approvalPeer = func(*connState) (bool, string) { return true, a1 }
	c := dialVerb(t, sp)
	restrict(t, c, map[string]any{"scope": "own", "read_only": true})
	wantForbidden(t, "lifting read_only", callP(c, t, "restrict-connection", map[string]any{"scope": "own"}))
	wantForbidden(t, "widening to all", callP(c, t, "restrict-connection", map[string]any{"scope": "all", "read_only": true}))
	wantForbidden(t, "moving to another pane", callP(c, t, "restrict-connection", map[string]any{"read_only": true, "pane_id": b1, "pane_token": d.manager.PaneToken(b1)}))
	// Repeating the restriction is fine.
	restrict(t, c, map[string]any{"scope": "own", "read_only": true})
	wantForbidden(t, "send-text after a refused widening", callP(c, t, "send-text", map[string]any{"text": "x"}))
}

func TestRestrictConnectionPlacesTheCallerByTokenOnlyWhenTheKernelCannot(t *testing.T) {
	d, sp, a1, _, b1 := scopeFixture(t)

	// The kernel places the caller in no pane: a valid token places it.
	d.approvalPeer = func(*connState) (bool, string) { return false, "" }
	c := dialVerb(t, sp)
	res := restrict(t, c, map[string]any{"pane_id": b1, "pane_token": d.manager.PaneToken(b1)})
	if res["session"] != "b" || res["via"] != "token" {
		t.Fatalf("restrict by token = %v, want session b via token", res)
	}
	result(t, callP(c, t, "list-windows", nil))

	// A wrong token is refused, and so is another pane's token.
	bad := dialVerb(t, sp)
	wantForbidden(t, "a wrong token", callP(bad, t, "restrict-connection", map[string]any{"pane_id": b1, "pane_token": "0123"}))
	wantForbidden(t, "another pane's token", callP(bad, t, "restrict-connection", map[string]any{"pane_id": b1, "pane_token": d.manager.PaneToken(a1)}))

	// No claim at all: the caller reaches no session.
	none := dialVerb(t, sp)
	res = restrict(t, none, nil)
	if res["window"] != "" || res["session"] != "" {
		t.Fatalf("restrict with no pane = %v, want no window", res)
	}
	wantForbidden(t, "list-windows from no pane", callP(none, t, "list-windows", nil))
	result(t, callP(none, t, "list-verbs", map[string]any{"verb": "hello"}))

	// The kernel's answer wins over a token for another pane.
	d.approvalPeer = func(*connState) (bool, string) { return true, a1 }
	k := dialVerb(t, sp)
	wantForbidden(t, "a token for another pane than the kernel's", callP(k, t, "restrict-connection", map[string]any{"pane_id": b1, "pane_token": d.manager.PaneToken(b1)}))
}

func TestRestrictConnectionReachesTheFanGroup(t *testing.T) {
	d, sp, a1, _, b1 := scopeFixture(t)
	for _, name := range []string{"c", "e"} {
		makeSessionWithWindow(t, d, name)
	}
	// b was launched from a; c and e are siblings of one fan; e is in no
	// group of a's.
	mustSetWorktree(t, d, "b", &WorktreeInfo{LaunchedFrom: "a"})
	sibling := func(name string) {
		info := &WorktreeInfo{Group: "fan/x", Managed: true}
		info.RepoRoot = "/src/repo"
		mustSetWorktree(t, d, name, info)
	}
	sibling("c")
	sibling("e")

	d.approvalPeer = func(*connState) (bool, string) { return true, a1 }
	c := dialVerb(t, sp)
	res := restrict(t, c, nil)
	if got := res["sessions"].([]any); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("a reaches %v, want [a b]", got)
	}
	result(t, callP(c, t, "send-text", map[string]any{"session": "b", "window": b1, "text": "x"}))
	wantForbidden(t, "a writing into c", callP(c, t, "list-windows", map[string]any{"session": "c"}))

	cw := d.manager.GetSession("c").GetState().Windows[0].ID
	d.approvalPeer = func(*connState) (bool, string) { return true, cw }
	sc := dialVerb(t, sp)
	res = restrict(t, sc, nil)
	if got := res["sessions"].([]any); len(got) != 2 || got[0] != "c" || got[1] != "e" {
		t.Errorf("c reaches %v, want its sibling [c e]", got)
	}
	wantForbidden(t, "c reading its launcher's session", callP(sc, t, "list-windows", map[string]any{"session": "a"}))

	// A sibling in another repository is not in the group.
	other := &WorktreeInfo{Group: "fan/x", Managed: true}
	other.RepoRoot = "/src/other"
	mustSetWorktree(t, d, "e", other)
	wantForbidden(t, "a same-named group in another repository", callP(sc, t, "list-windows", map[string]any{"session": "e"}))
}

func mustSetWorktree(t *testing.T, d *Daemon, session string, info *WorktreeInfo) {
	t.Helper()
	if err := d.manager.GetSession(session).SetWorktree(info); err != nil {
		t.Fatal(err)
	}
}

func TestRestrictedSubscribeCarriesOnlyTheSessionsItReaches(t *testing.T) {
	d, sp, a1, _, b1 := scopeFixture(t)
	d.approvalPeer = func(*connState) (bool, string) { return true, a1 }
	c := dialVerb(t, sp)
	restrict(t, c, map[string]any{"read_only": true})
	wantForbidden(t, "subscribe to b", callP(c, t, "subscribe", map[string]any{"session": "b"}))
	wantForbidden(t, "subscribe with hosts", callP(c, t, "subscribe", map[string]any{"hosts": true}))
	ack := result(t, callP(c, t, "subscribe", map[string]any{"types": []string{EventAgentState}}))
	if ack["type"] != EventSubscribed {
		t.Fatalf("subscribe ack = %v", ack)
	}

	plain := dialVerb(t, sp)
	setAgentState(t, plain, "b", b1, "working", "", "")
	setAgentState(t, plain, "a", a1, "working", "", "")

	ev := readEvent(t, c)
	if ev["session"] != "a" || ev["window"] != a1 {
		t.Fatalf("the first event on a restricted stream is %v, want a's; b's must not be written", ev)
	}
}

// TestRestrictedResumeFromAnEvictedSeqGivesTheBaseline: a scope own subscriber
// whose session was quiet while another session pushed its seq out of the
// ring gets a gap marker and nothing else, and an ack whose seq is past every
// event it missed and counts no replayed events. tuios mcp resumes from that
// seq; if the ack gave anything less, the caller could never move past the gap.
func TestRestrictedResumeFromAnEvictedSeqGivesTheBaseline(t *testing.T) {
	d, sp, a1, _, _ := scopeFixture(t)
	d.approvalPeer = func(*connState) (bool, string) { return true, a1 }
	d.events.mu.Lock()
	start := d.events.seq
	d.events.mu.Unlock()
	for range defaultEventRing + 10 {
		d.events.publish(streamEvent{Type: EventAgentState, Session: "b"})
	}
	c := dialVerb(t, sp)
	restrict(t, c, map[string]any{"read_only": true})
	ack := result(t, callP(c, t, "subscribe", map[string]any{
		"types":     []string{EventAgentState},
		"after_seq": start,
		"boot_id":   d.events.bootIdentity(),
	}))
	seq, _ := ack["seq"].(float64)
	if uint64(seq) < start+defaultEventRing+10 {
		t.Errorf("ack seq = %v, want at least %d", ack["seq"], start+defaultEventRing+10)
	}
	if ack["replayed"] != float64(0) {
		t.Errorf("ack replayed = %v, want 0: every retained event is another session's", ack["replayed"])
	}
	if ev := readEvent(t, c); ev["type"] != EventGap || ev["reason"] != GapEvicted {
		t.Fatalf("first event = %v, want a gap with reason evicted", ev)
	}
	// Nothing else is written: the next line is a live event of a.
	plain := dialVerb(t, sp)
	setAgentState(t, plain, "a", a1, "working", "", "")
	if ev := readEvent(t, c); ev["session"] != "a" || ev["seq"].(float64) <= seq {
		t.Fatalf("event after the gap = %v, want a live event of a above seq %v", ev, seq)
	}
}

// readEvent reads one event line from a subscribed connection.
func readEvent(t *testing.T, c *verbConn) map[string]any {
	t.Helper()
	_ = c.conn.SetReadDeadline(time.Now().Add(5 * time.Second * testDeadlineScale))
	line, err := c.r.ReadBytes('\n')
	if err != nil {
		t.Fatalf("read event: %v", err)
	}
	var ev map[string]any
	if err := json.Unmarshal(line, &ev); err != nil {
		t.Fatalf("decode event %q: %v", line, err)
	}
	return ev
}

func TestFanRecordsTheSessionItWasLaunchedFrom(t *testing.T) {
	d, sp, repo := worktreeFixture(t)
	fakeClaudeOnPath(t)
	home := makeSessionWithWindow(t, d, "home")
	homeWin := home.GetState().Windows[0].ID
	d.approvalPeer = func(*connState) (bool, string) { return true, homeWin }

	c := dialVerb(t, sp)
	res := result(t, c.call(t, `{"id":1,"verb":"fan","params":{"count":1,"agent":"claude","prompt":"Say hi.","repo":"`+repo+`","base":"main","ready_timeout":200}}`))
	rows := res["sessions"].([]any)
	name := rows[0].(map[string]any)["session"].(string)
	info := d.manager.GetSession(name).Worktree()
	if info == nil || info.LaunchedFrom != "home" {
		t.Fatalf("the fan session's record = %+v, want launched_from home", info)
	}
	listed := result(t, c.call(t, `{"id":2,"verb":"list-worktrees","params":{"group":"`+res["group"].(string)+`"}}`))
	if row := listed["worktrees"].([]any)[0].(map[string]any); row["launched_from"] != "home" {
		t.Errorf("list-worktrees row = %v, want launched_from home", row)
	}

	// A connection restricted to home reaches it.
	r := dialVerb(t, sp)
	got := restrict(t, r, map[string]any{"read_only": true})["sessions"].([]any)
	if len(got) != 2 || !slices.Contains(got, any(name)) {
		t.Errorf("home reaches %v, want home and %s", got, name)
	}
}

func TestPaneTokenIsExportedAndNamesOneWindow(t *testing.T) {
	m := NewManager()
	sess, err := m.CreateSession("tok", &SessionConfig{}, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sess.Stop)
	env := sess.buildEnv("win-1", false)
	idx := slices.IndexFunc(env, func(s string) bool { return strings.HasPrefix(s, "TUIOS_PANE_TOKEN=") })
	if idx < 0 {
		t.Fatalf("no TUIOS_PANE_TOKEN in the pane environment: %v", env)
	}
	tok := strings.TrimPrefix(env[idx], "TUIOS_PANE_TOKEN=")
	if !m.VerifyPaneToken("win-1", tok) {
		t.Error("the exported token does not verify for its own window")
	}
	if m.VerifyPaneToken("win-2", tok) {
		t.Error("the token verifies for another window")
	}
	if NewManager().VerifyPaneToken("win-1", tok) {
		t.Error("the token verifies under another daemon start's key")
	}
	if m.VerifyPaneToken("win-1", "") {
		t.Error("an empty token verifies")
	}
}
