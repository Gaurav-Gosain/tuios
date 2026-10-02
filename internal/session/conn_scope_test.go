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
		if _, ok := verbRegistry[name]; !ok && !verbLeftOut(name) {
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

func TestRestrictConnectionNeverWidens(t *testing.T) {
	d, sp, a1, _, b1 := scopeFixture(t)
	d.setApprovalPeer(func(*connState) (bool, string) { return true, a1 })
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
	d.setApprovalPeer(func(*connState) (bool, string) { return false, "" })
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
	d.setApprovalPeer(func(*connState) (bool, string) { return true, a1 })
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

	d.setApprovalPeer(func(*connState) (bool, string) { return true, a1 })
	c := dialVerb(t, sp)
	res := restrict(t, c, nil)
	if got := res["sessions"].([]any); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("a reaches %v, want [a b]", got)
	}
	result(t, callP(c, t, "send-text", map[string]any{"session": "b", "window": b1, "text": "x"}))
	wantForbidden(t, "a writing into c", callP(c, t, "list-windows", map[string]any{"session": "c"}))

	cw := d.manager.GetSession("c").GetState().Windows[0].ID
	d.setApprovalPeer(func(*connState) (bool, string) { return true, cw })
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
