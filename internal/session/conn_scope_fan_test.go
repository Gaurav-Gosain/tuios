//go:build !slim

package session

import (
	"slices"
	"testing"
)

func TestFanRecordsTheSessionItWasLaunchedFrom(t *testing.T) {
	d, sp, repo := worktreeFixture(t)
	fakeClaudeOnPath(t)
	home := makeSessionWithWindow(t, d, "home")
	homeWin := home.GetState().Windows[0].ID
	d.setApprovalPeer(func(*connState) (bool, string) { return true, homeWin })

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
