package session

import (
	"runtime"
	"testing"
)

// The session that started a fan reaches its attempts by the session's ID.
// Once it ends, a session that takes its name reaches none of them.
// Security boundary test.
func TestFanLeadIsHeldByID(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the agents here are POSIX programs")
	}
	d, sp, repo := worktreeFixture(t)
	admin := dialVerb(t, sp)
	lead := makeSessionWithWindow(t, d, "lead")
	firstWindow := func(name string) string { return d.manager.GetSession(name).GetState().Windows[0].ID }
	cur := ""
	d.setApprovalPeer(func(*connState) (bool, string) { return true, cur })
	as := func(window string) *verbConn {
		cur = window
		return dialVerb(t, sp)
	}
	setStrict(d, "read", "fan")

	r := callP(as(firstWindow(lead.Name())), t, "fan", map[string]any{"repo": repo, "name": "fan/sub", "count": 1, "agent": "sleep 60", "prompt": "x", "ready_timeout": 300})
	res, ok := r["result"].(map[string]any)
	if !ok {
		t.Fatalf("the lead could not fan: %v", r["error"])
	}
	attempt := res["sessions"].([]any)[0].(map[string]any)["session"].(string)
	if wt := d.manager.GetSession(attempt).Worktree(); wt.LaunchedFromID != lead.ID {
		t.Fatalf("the attempt names its lead by %q, want the ID %q", wt.LaunchedFromID, lead.ID)
	}
	if r := callP(as(firstWindow(lead.Name())), t, "compare-fan", map[string]any{"session": attempt, "changes": false}); r["error"] != nil {
		t.Fatalf("the lead cannot read its attempt: %v", r["error"])
	}

	// After a restart without the lead, a new session of the lead's name
	// reaches nothing: the record names the lead's ID, which it has not.
	state := d.manager.GetSession(attempt).ResurrectionState()
	d2, sp2 := startTestDaemon(t)
	if _, err := d2.restoreSession(state); err != nil {
		t.Fatalf("restore %s: %v", attempt, err)
	}
	other := makeSessionWithWindow(t, d2, "lead")
	d2.setApprovalPeer(func(*connState) (bool, string) { return true, other.GetState().Windows[0].ID })
	setStrict(d2, "read", "fan")
	wantForbidden(t, "compare-fan from a new session of the lead's name after a restart", callP(dialVerb(t, sp2), t, "compare-fan", map[string]any{"session": attempt, "changes": false}))

	// The lead ends, and another session takes its name.
	d.setApprovalPeer(nil)
	if r := callP(admin, t, "kill-session", map[string]any{"session": "lead"}); r["error"] != nil {
		t.Fatalf("kill-session: %v", r["error"])
	}
	if wt := d.manager.GetSession(attempt).Worktree(); wt.LaunchedFrom != "" || wt.LaunchedFromID != "" {
		t.Fatalf("the attempt still names its ended lead: %q %q", wt.LaunchedFrom, wt.LaunchedFromID)
	}
	squat := makeSessionWithWindow(t, d, "lead")
	d.setApprovalPeer(func(*connState) (bool, string) { return true, cur })
	wantForbidden(t, "compare-fan from a session that took the lead's name", callP(as(firstWindow(squat.Name())), t, "compare-fan", map[string]any{"session": attempt, "changes": false}))
}

// A record from before launched_from_id names its lead by name alone. After
// a restart where the lead did not come back, a session that takes the name
// must reach none of the attempts.
func TestReview978LegacyRecord(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip()
	}
	d, sp, repo := worktreeFixture(t)
	admin := dialVerb(t, sp)
	// A record an older daemon wrote: the lead by name only.
	mine := fakeFan(t, d, admin, repo, "fan/sub", 1, "repo-fan-x")
	state := d.manager.GetSession(mine[0]).ResurrectionState()
	if state.Worktree.LaunchedFromID != "" {
		t.Fatal("not a legacy record")
	}
	// Before any restart, the name alone reaches nothing either.
	early := makeSessionWithWindow(t, d, "repo-fan-x")
	d.setApprovalPeer(func(*connState) (bool, string) { return true, early.GetState().Windows[0].ID })
	setStrict(d, "read", "fan")
	if r := callP(dialVerb(t, sp), t, "compare-fan", map[string]any{"session": mine[0], "changes": false}); r["error"] == nil {
		t.Errorf("a session named like the lead of an older record does compare-fan in %s", mine[0])
	}
	d2, sp2 := startTestDaemon(t)
	if _, err := d2.restoreSession(state); err != nil {
		t.Fatal(err)
	}
	if lf := d2.manager.GetSession(mine[0]).Worktree().LaunchedFrom; lf != "" {
		t.Errorf("a restored record from before the ID still names its lead %q", lf)
	}
	squat := makeSessionWithWindow(t, d2, "repo-fan-x")
	d2.setApprovalPeer(func(*connState) (bool, string) { return true, squat.GetState().Windows[0].ID })
	setStrict(d2, "read", "fan")
	c := dialVerb(t, sp2)
	for verb, p := range map[string]map[string]any{
		"compare-fan": {"session": mine[0], "changes": false},
		"send-text":   {"session": mine[0], "window": d2.manager.GetSession(mine[0]).GetState().Windows[0].ID, "text": "x"},
	} {
		r := callP(c, t, verb, p)
		if r["error"] == nil {
			t.Errorf("a new session named like a dead lead from an older record does %s in %s", verb, mine[0])
		} else {
			t.Logf("%s -> %v", verb, r["error"])
		}
	}
}
