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
