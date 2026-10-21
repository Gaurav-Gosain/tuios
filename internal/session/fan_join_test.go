package session

import (
	"runtime"
	"testing"
)

// A fan's sessions reach each other, so a pane must not join a fan it is
// not part of by naming the fan's branch stem. Security boundary test.
func TestFanRefusesJoiningAnotherGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the agents here are POSIX programs")
	}
	d, sp, repo := worktreeFixture(t)
	admin := dialVerb(t, sp)
	lead := makeSessionWithWindow(t, d, "lead")
	other := makeSessionWithWindow(t, d, "other")
	mine := fakeFan(t, d, admin, repo, "fan/retry", 2, "lead")
	firstWindow := func(name string) string { return d.manager.GetSession(name).GetState().Windows[0].ID }
	fanCall := func(c *verbConn, name string) map[string]any {
		return callP(c, t, "fan", map[string]any{"repo": repo, "name": name, "count": 1, "agent": "sleep 60", "prompt": "x", "ready_timeout": 300})
	}

	cur := ""
	d.setApprovalPeer(func(*connState) (bool, string) { return true, cur })
	as := func(window string) *verbConn {
		cur = window
		return dialVerb(t, sp)
	}
	setStrict(d, "read", "fan")

	// A pane of another session is refused the fan's stem, and makes no
	// session in it.
	before := len(d.fanSiblings(d.manager.GetSession(mine[0]).Worktree()))
	wantForbidden(t, "fan into another pane's group", fanCall(as(firstWindow(other.Name())), "fan/retry"))
	if after := len(d.fanSiblings(d.manager.GetSession(mine[0]).Worktree())); after != before {
		t.Fatalf("the group grew from %d to %d sessions", before, after)
	}
	// A new stem is its own fan.
	if r := fanCall(as(firstWindow(other.Name())), "fan/other"); r["error"] != nil {
		t.Fatalf("a new fan was refused: %v", r["error"])
	}
	// The pane that started the fan, and a pane of it, add attempts.
	if r := fanCall(as(firstWindow(lead.Name())), "fan/retry"); r["error"] != nil {
		t.Fatalf("the fan's own lead was refused: %v", r["error"])
	}
	if r := fanCall(as(firstWindow(mine[0])), "fan/retry"); r["error"] != nil {
		t.Fatalf("a pane of the fan was refused: %v", r["error"])
	}
	// So does the person.
	d.setApprovalPeer(nil)
	if r := fanCall(admin, "fan/retry"); r["error"] != nil {
		t.Fatalf("the person was refused: %v", r["error"])
	}
}
