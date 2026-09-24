package session

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
	"github.com/Gaurav-Gosain/tuios/internal/worktree"
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

func worktreePath(t *testing.T, d *Daemon, name string) string {
	t.Helper()
	return d.manager.GetSession(name).Worktree().Path
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

// TestCompareFanCountsEachAttemptAgainstItsBase: one row per attempt in fan
// order, each counted against the fan's base with its commits, uncommitted
// edits and untracked files, and with the last command its shells finished.
func TestCompareFanCountsEachAttemptAgainstItsBase(t *testing.T) {
	d, sp, repo := worktreeFixture(t)
	c := dialVerb(t, sp)
	names := fakeFan(t, d, c, repo, "fan/retry", 2, "")

	one := worktreePath(t, d, names[0])
	writeIn(t, one, "README", "hello\ncommitted\n")
	testutil.Git(t, one, "commit", "-q", "-am", "change")
	writeIn(t, one, "new.txt", "a\nb\nc\n")

	// The second attempt's shell finished make lint with status 1.
	two := d.manager.GetSession(names[1])
	pty := two.GetPTY(two.GetState().Windows[0].PTYID)
	pty.noteShellMark(vt.SemanticMarker{Type: vt.MarkerCommandExecuted, ExitCode: -1, CapturedText: "make lint"})
	pty.noteShellMark(vt.SemanticMarker{Type: vt.MarkerCommandFinished, ExitCode: 1})

	res := result(t, callP(c, t, "compare-fan", map[string]any{"session": names[1]}))
	if res["type"] != "fan_compare" || res["group"] != "fan/retry" || res["base"] != "main" || res["repo"] != "repo" {
		t.Errorf("header = %v", res)
	}
	rows := rowsOf(t, res)
	if got := rowSessions(rows); !slices.Equal(got, names) {
		t.Fatalf("rows = %v, want %v in fan order", got, names)
	}
	r1, r2 := rows[0], rows[1]
	// README +1 committed, new.txt +3 untracked.
	if r1["files"] != 2.0 || r1["added"] != 4.0 || r1["removed"] != 0.0 || r1["ahead"] != 1.0 || r1["dirty"] != true {
		t.Errorf("first row counts = files %v added %v removed %v ahead %v dirty %v", r1["files"], r1["added"], r1["removed"], r1["ahead"], r1["dirty"])
	}
	if r2["files"] != 0.0 || r2["dirty"] != false || r2["ahead"] != 0.0 {
		t.Errorf("second row counts = files %v dirty %v ahead %v", r2["files"], r2["dirty"], r2["ahead"])
	}
	if r1["agent"] != "claude" || r1["branch"] != "fan/retry" || r1["state"] == "" {
		t.Errorf("first row = %v", r1)
	}
	last, _ := r2["last_command"].(map[string]any)
	if last == nil || last["cmdline"] != "make lint" || last["exit"] != 1.0 || last["at"] == 0.0 {
		t.Errorf("last_command = %v, want make lint exiting 1", r2["last_command"])
	}
	if r1["last_command"] != nil {
		t.Errorf("a pane that finished nothing reports %v", r1["last_command"])
	}

	// changes false runs no git: the counts are left out.
	res = result(t, callP(c, t, "compare-fan", map[string]any{"session": names[0], "changes": false}))
	for _, r := range rowsOf(t, res) {
		for _, k := range []string{"files", "added", "removed", "ahead", "dirty"} {
			if _, ok := r[k]; ok {
				t.Errorf("changes false still reports %s on %v", k, r["session"])
			}
		}
	}
}

// TestCompareFanRefusesAWorktreeThatIsNotAFan: a worktree made on its own has
// no attempts to compare, and the refusal names the fans there are.
func TestCompareFanRefusesAWorktreeThatIsNotAFan(t *testing.T) {
	d, sp, repo := worktreeFixture(t)
	c := dialVerb(t, sp)
	fan := fakeFan(t, d, c, repo, "fan/retry", 1, "")
	solo := newWorktreeCall(t, c, repo, "solo", nil)["session"].(string)
	resp := callP(c, t, "compare-fan", map[string]any{"session": solo})
	mustRefuse(t, resp, ErrVerbInvalidParams, "compare-fan on a worktree that is not a fan")
	hint, _ := resp["error"].(map[string]any)["hint"].(map[string]any)
	if avail, _ := hint["available"].([]any); len(avail) != 1 || avail[0] != fan[0] {
		t.Errorf("hint names %v, want the fan session %s", hint["available"], fan[0])
	}
	mustRefuse(t, callP(c, t, "keep-fan", map[string]any{"session": solo}), ErrVerbInvalidParams, "keep-fan on a worktree that is not a fan")
	mustRefuse(t, callP(c, t, "verify-fan", map[string]any{"session": solo, "command": "true"}), ErrVerbInvalidParams, "verify-fan on a worktree that is not a fan")
	mustRefuse(t, callP(c, t, "verify-fan", map[string]any{"session": fan[0], "command": strings.Repeat("x", fanVerifyMaxCommand+1)}), ErrVerbInvalidParams, "a command past the bound")
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

// verifyWindows lists the windows named verify in a session.
func verifyWindows(d *Daemon, name string) []WindowState {
	var out []WindowState
	for _, w := range d.manager.GetSession(name).GetState().Windows {
		if w.CustomName == fanVerifyWindow {
			out = append(out, w)
		}
	}
	return out
}

func waitNoVerifyWindow(t *testing.T, d *Daemon, name string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for len(verifyWindows(d, name)) > 0 {
		if time.Now().After(deadline) {
			t.Fatalf("the verify window in %s is still open", name)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestVerifyFanRecordsPassedAndFailed: the check runs in each attempt's
// worktree with the caller's environment, a pass closes its window and a
// failure keeps it open, and the window holds no grants.
func TestVerifyFanRecordsPassedAndFailed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the check runs with sh")
	}
	d, sp, repo := worktreeFixture(t)
	c := dialVerb(t, sp)
	names := fakeFan(t, d, c, repo, "fan/retry", 2, "")
	writeIn(t, worktreePath(t, d, names[0]), "pass.txt", "ok\n")

	res := result(t, callP(c, t, "verify-fan", map[string]any{
		"session": names[1],
		"command": `test "$VERIFY_TOKEN" = yes && test -f pass.txt`,
		"env":     map[string]any{"VERIFY_TOKEN": "yes"},
	}))
	if started, _ := res["sessions"].([]any); len(started) != 2 {
		t.Fatalf("started in %v, want both attempts", res["sessions"])
	}

	pass := waitVerify(t, d, names[0])
	if pass.State != VerifyPassed || pass.Exit == nil || *pass.Exit != 0 || pass.FinishedAt == 0 || !strings.Contains(pass.Command, "pass.txt") {
		t.Errorf("first attempt = %+v, want passed with exit 0", pass)
	}
	fail := waitVerify(t, d, names[1])
	if fail.State != VerifyFailed || fail.Exit == nil || *fail.Exit != 1 || fail.Note != "" {
		t.Errorf("second attempt = %+v, want failed with exit 1", fail)
	}
	waitNoVerifyWindow(t, d, names[0])
	kept := verifyWindows(d, names[1])
	if len(kept) != 1 {
		t.Fatalf("the failed check's window is not kept open: %d verify windows", len(kept))
	}
	// Kept open means its process still runs: an attached client closes a
	// window whose process exited.
	if _, exited := d.manager.GetSession(names[1]).GetPTY(kept[0].PTYID).ExitStatus(); exited {
		t.Error("the failed check's process exited, so a client would close its window")
	}
	if g, explicit := d.manager.grants.effective(kept[0].ID); !explicit || g != 0 {
		t.Errorf("the verify window holds %s (explicit %v), want none", g.String(), explicit)
	}

	// compare-fan reports the checks.
	for _, r := range rowsOf(t, result(t, callP(c, t, "compare-fan", map[string]any{"session": names[0], "changes": false}))) {
		v, _ := r["verify"].(map[string]any)
		want := VerifyPassed
		if r["session"] == names[1] {
			want = VerifyFailed
		}
		if v == nil || v["state"] != want {
			t.Errorf("%v reports verify %v, want %s", r["session"], r["verify"], want)
		}
	}
}

// TestVerifyFanTimesOutAndANewCheckStopsTheOld: a check past its timeout is
// failed with a note and its window closed, and a second check stops a first
// that still runs.
func TestVerifyFanTimesOutAndANewCheckStopsTheOld(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the check runs with sh")
	}
	d, sp, repo := worktreeFixture(t)
	c := dialVerb(t, sp)
	names := fakeFan(t, d, c, repo, "fan/retry", 1, "")

	result(t, callP(c, t, "verify-fan", map[string]any{"session": names[0], "command": "sleep 30", "timeout_ms": 300}))
	v := waitVerify(t, d, names[0])
	if v.State != VerifyFailed || v.Exit != nil || !strings.Contains(v.Note, "timed out") {
		t.Errorf("timed out check = %+v", v)
	}
	waitNoVerifyWindow(t, d, names[0])

	result(t, callP(c, t, "verify-fan", map[string]any{"session": names[0], "command": "sleep 30"}))
	result(t, callP(c, t, "verify-fan", map[string]any{"session": names[0], "command": "true"}))
	v = waitVerify(t, d, names[0])
	if v.State != VerifyPassed || v.Command != "true" {
		t.Errorf("after a second check = %+v, want the second one passed", v)
	}
	waitNoVerifyWindow(t, d, names[0])
}

// TestAVerifyRecordFromBeforeARestartReadsFailed: a check the daemon was not
// watching, which is one a restart ended, is not reported as running forever.
func TestAVerifyRecordFromBeforeARestartReadsFailed(t *testing.T) {
	d, sp, repo := worktreeFixture(t)
	c := dialVerb(t, sp)
	names := fakeFan(t, d, c, repo, "fan/retry", 1, "")
	d.manager.GetSession(names[0]).setFanVerify(&FanVerify{Command: "go test ./...", State: VerifyRunning, StartedAt: 1})
	rows := rowsOf(t, result(t, callP(c, t, "compare-fan", map[string]any{"session": names[0], "changes": false})))
	v, _ := rows[0]["verify"].(map[string]any)
	if v == nil || v["state"] != VerifyFailed || !strings.Contains(v["note"].(string), "restarted") {
		t.Errorf("verify = %v, want failed with a note about the restart", rows[0]["verify"])
	}
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
	d.approvalPeer = func(*connState) (bool, string) { return true, window }
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

// TestKeepFanRemovesTheSiblingsAndLeavesDirtyOnes: every other attempt goes
// the way remove-worktree removes it, one with uncommitted work is left in
// place unless stash says what to do with it, and branches are kept.
func TestKeepFanRemovesTheSiblingsAndLeavesDirtyOnes(t *testing.T) {
	d, sp, repo := worktreeFixture(t)
	c := dialVerb(t, sp)
	names := fakeFan(t, d, c, repo, "fan/retry", 3, "")
	dirty := worktreePath(t, d, names[2])
	writeIn(t, dirty, "wip.txt", "unsaved\n")

	res := result(t, callP(c, t, "keep-fan", map[string]any{"session": names[0]}))
	if res["type"] != "fan_kept" || res["kept"] != names[0] || res["left"] != 1.0 || res["branch"] != "fan/retry" {
		t.Errorf("result = %v", res)
	}
	removed, _ := res["removed"].([]any)
	if len(removed) != 2 {
		t.Fatalf("removed = %v, want one entry per sibling", removed)
	}
	clean, left := removed[0].(map[string]any), removed[1].(map[string]any)
	if clean["session"] != names[1] || clean["removed"] != true || clean["session_killed"] != true {
		t.Errorf("clean sibling = %v", clean)
	}
	if left["session"] != names[2] || left["removed"] != false || left["code"] != ErrVerbWorktreeDirty {
		t.Errorf("dirty sibling = %v", left)
	}
	if d.manager.GetSession(names[1]) != nil || d.manager.GetSession(names[0]) == nil || d.manager.GetSession(names[2]) == nil {
		t.Error("the sessions left are not the kept one and the dirty one")
	}
	if _, err := os.Stat(dirty); err != nil {
		t.Errorf("the dirty worktree was removed: %v", err)
	}
	if !worktree.BranchExists(repo, "fan/retry-2") {
		t.Error("the removed sibling's branch was deleted")
	}

	res = result(t, callP(c, t, "keep-fan", map[string]any{"session": names[0], "stash": true}))
	removed, _ = res["removed"].([]any)
	if res["left"] != 0.0 || len(removed) != 1 || removed[0].(map[string]any)["stashed"] != true {
		t.Errorf("keep with stash = %v", res)
	}
	if d.manager.GetSession(names[2]) != nil {
		t.Error("the stashed sibling's session is still there")
	}
	if out := testutil.Git(t, repo, "stash", "list"); !strings.Contains(out, "tuios: fan/retry-3") {
		t.Errorf("the stash does not hold the sibling's work: %q", out)
	}
}

func TestFanOrderPutsTenAfterNine(t *testing.T) {
	names := []string{"api-fan-10", "api-fan-2", "api-fan", "api-fan-9", "api-fan-3"}
	slices.SortFunc(names, fanOrder)
	want := []string{"api-fan", "api-fan-2", "api-fan-3", "api-fan-9", "api-fan-10"}
	if !slices.Equal(names, want) {
		t.Errorf("order = %v, want %v", names, want)
	}
}
