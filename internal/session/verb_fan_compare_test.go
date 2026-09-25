package session

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// Every test here works on a throwaway repository under the test's own
// temporary directory, with tuios's worktree directory pointed at another, and
// makes its fan with new-worktree plus the group fan would record, so no agent
// runs.

// fakeFan makes n worktree sessions of one fan from main, the way fan records
// them, and returns their names in fan order. launchedFrom, when set, is the
// session each one says it was launched from.
func fakeFan(t *testing.T, d *Daemon, c *verbConn, repo, stem string, n int, launchedFrom string) []string {
	t.Helper()
	var names []string
	for i := range n {
		branch := stem
		if i > 0 {
			branch = stem + "-" + string(rune('1'+i))
		}
		res := newWorktreeCall(t, c, repo, branch, map[string]any{"base": "main"})
		name := res["session"].(string)
		sess := d.manager.GetSession(name)
		info := sess.Worktree()
		info.Group = stem
		info.LaunchedFrom = launchedFrom
		info.Agent = "claude"
		if err := sess.SetWorktree(info); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	return names
}

// wantReached fails when a call was refused by the scope, grant or link check
// rather than answered by its handler.
func wantReached(t *testing.T, what string, resp map[string]any) {
	t.Helper()
	if e, ok := resp["error"].(map[string]any); ok {
		if code, _ := e["code"].(string); code == ErrVerbForbidden {
			t.Errorf("%s was refused before its handler: %v", what, e["message"])
		}
		if msg, _ := e["message"].(string); strings.Contains(msg, "not built yet") {
			t.Errorf("%s is still a stub: %v", what, msg)
		}
	}
}

func writeIn(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func rowsOf(t *testing.T, res map[string]any) []map[string]any {
	t.Helper()
	raw, _ := res["rows"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, r := range raw {
		out = append(out, r.(map[string]any))
	}
	return out
}

func rowSessions(rows []map[string]any) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r["session"].(string))
	}
	return out
}

// waitVerify waits for a session's check to leave running and returns it.
func waitVerify(t *testing.T, d *Daemon, name string) *FanVerify {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if wt := d.manager.GetSession(name).Worktree(); wt != nil && wt.Verify != nil && wt.Verify.State != VerifyRunning {
			return wt.Verify
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the check in %s never finished: %+v", name, d.manager.GetSession(name).Worktree().Verify)
	return nil
}

// TestFanVerbsReachOnlyTheSiblingsThePaneReaches: a pane without admin names
// a session it reaches, and the rows and checks leave out a sibling of the
// same group it could not name itself.
func TestFanVerbsReachOnlyTheSiblingsThePaneReaches(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the check runs with sh")
	}
	d, sp, repo := worktreeFixture(t)
	c := dialVerb(t, sp)
	caller := makeSessionWithWindow(t, d, "lead")
	makeSessionWithWindow(t, d, "other")
	mine := fakeFan(t, d, c, repo, "fan/retry", 2, "lead")
	// A third attempt of the same group, launched from another session.
	stray := newWorktreeCall(t, c, repo, "fan/retry-3", map[string]any{"base": "main"})["session"].(string)
	sess := d.manager.GetSession(stray)
	info := sess.Worktree()
	info.Group, info.LaunchedFrom = "fan/retry", "other"
	_ = sess.SetWorktree(info)

	setStrict(d, "read", "fan")
	window := caller.GetState().Windows[0].ID
	d.setApprovalPeer(func(*connState) (bool, string) { return true, window })
	pane := dialVerb(t, sp)

	rows := rowsOf(t, result(t, callP(pane, t, "compare-fan", map[string]any{"session": mine[0], "changes": false})))
	if got := rowSessions(rows); !slices.Equal(got, mine) {
		t.Errorf("rows = %v, want only %v", got, mine)
	}
	res := result(t, callP(pane, t, "verify-fan", map[string]any{"session": mine[0], "command": "true"}))
	started := []string{}
	for _, s := range res["sessions"].([]any) {
		started = append(started, s.(string))
	}
	if !slices.Equal(started, mine) {
		t.Errorf("checks started in %v, want only %v", started, mine)
	}
	if v := d.manager.GetSession(stray).Worktree().Verify; v != nil {
		t.Errorf("the sibling out of reach got a check: %+v", v)
	}
	wantForbidden(t, "compare-fan naming the sibling out of reach", callP(pane, t, "compare-fan", map[string]any{"session": stray}))
	wantForbidden(t, "keep-fan from a pane without admin", callP(pane, t, "keep-fan", map[string]any{"session": mine[0]}))
	for _, name := range mine {
		waitVerify(t, d, name)
	}
}

// TestVerifyFanEnvIsRefusedOverALink: a link may not send its machine's
// variables, the rule fan has.
func TestVerifyFanEnvIsRefusedOverALink(t *testing.T) {
	d, sp, repo := worktreeFixture(t)
	c := dialVerb(t, sp)
	names := fakeFan(t, d, c, repo, "fan/retry", 1, "")
	link := dialLink(t, sp)
	mustRefuse(t, callP(link, t, "verify-fan", map[string]any{"session": names[0], "command": "true", "env": map[string]any{"A": "b"}}),
		ErrVerbForbidden, "verify-fan with env over a link")
	if v := d.manager.GetSession(names[0]).Worktree().Verify; v != nil {
		t.Errorf("a refused call started a check: %+v", v)
	}
}

// TestCompareFanRefusalNamesOnlyFansThePaneReaches: a pane that names a
// worktree session outside any fan gets a hint listing the fan sessions it
// reaches, and no name of a fan it could not reach.
func TestCompareFanRefusalNamesOnlyFansThePaneReaches(t *testing.T) {
	d, sp, repo := worktreeFixture(t)
	c := dialVerb(t, sp)
	caller := makeSessionWithWindow(t, d, "lead")
	makeSessionWithWindow(t, d, "other")
	mine := fakeFan(t, d, c, repo, "fan/mine", 1, "lead")
	theirs := fakeFan(t, d, c, repo, "fan/theirs", 2, "other")
	solo := newWorktreeCall(t, c, repo, "solo", nil)["session"].(string)
	sess := d.manager.GetSession(solo)
	info := sess.Worktree()
	info.LaunchedFrom = "lead"
	if err := sess.SetWorktree(info); err != nil {
		t.Fatal(err)
	}

	setStrict(d, "read", "fan")
	window := caller.GetState().Windows[0].ID
	d.setApprovalPeer(func(*connState) (bool, string) { return true, window })
	pane := dialVerb(t, sp)

	for verb, params := range map[string]map[string]any{
		"compare-fan": {"session": solo},
		"verify-fan":  {"session": solo, "command": "true"},
	} {
		resp := callP(pane, t, verb, params)
		mustRefuse(t, resp, ErrVerbInvalidParams, verb+" from a pane on a worktree that is not a fan")
		hint, _ := resp["error"].(map[string]any)["hint"].(map[string]any)
		var avail []string
		if raw, _ := hint["available"].([]any); raw != nil {
			for _, a := range raw {
				avail = append(avail, a.(string))
			}
		}
		if !slices.Equal(avail, mine) {
			t.Errorf("%s hint names %v, want only %v", verb, avail, mine)
		}
		for _, name := range theirs {
			if slices.Contains(avail, name) {
				t.Errorf("%s hint leaks %s, a fan session the pane does not reach", verb, name)
			}
		}
	}
}

// TestAVerifyFinishingDuringARead reads as finished: a compare that read the
// record while the check ran, and the table after the check finished, reports
// the finished record rather than a restart.
func TestAVerifyFinishingDuringARead(t *testing.T) {
	d, sp, repo := worktreeFixture(t)
	c := dialVerb(t, sp)
	names := fakeFan(t, d, c, repo, "fan/retry", 1, "")
	sess := d.manager.GetSession(names[0])
	running := &FanVerify{Command: "go test ./...", State: VerifyRunning, StartedAt: 7}
	code := 0
	sess.setFanVerify(&FanVerify{Command: "go test ./...", State: VerifyPassed, StartedAt: 7, FinishedAt: 9, Exit: &code})
	got := d.fanVerifyReport(sess, running)
	if got == nil || got.State != VerifyPassed || got.Note != "" {
		t.Errorf("report = %+v, want the passed record", got)
	}
}

// TestFanRowChangesRunsEveryGitCallUnderTheBound: a row whose time is up runs
// no git at all, the merge base and the base lookup included, and says why it
// has no counts.
func TestFanRowChangesRunsEveryGitCallUnderTheBound(t *testing.T) {
	d, sp, repo := worktreeFixture(t)
	c := dialVerb(t, sp)
	names := fakeFan(t, d, c, repo, "fan/retry", 1, "")
	wt := d.manager.GetSession(names[0]).Worktree()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, base := range []string{"main", ""} {
		info := *wt
		info.Base = base
		var row fanCompareRow
		fanRowChanges(ctx, &row, &info)
		if row.BaseSHA != "" || row.Files != nil || row.Ahead != nil || row.Dirty != nil {
			t.Errorf("base %q: a row past its bound ran git: %+v", base, row)
		}
		if !strings.Contains(row.Note, "context canceled") {
			t.Errorf("base %q: note = %q, want the bound named", base, row.Note)
		}
	}
}
