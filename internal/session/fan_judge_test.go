package session

import (
	"runtime"
	"testing"
)

// The judge of a fan reaches the attempts by its pane, and only to read
// them and record a verdict. Security boundary test: each case is a way a
// pane could take a judge's reach or a verdict it is not owed.
func TestFanJudgeReach(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the panes here are POSIX programs")
	}
	d, sp, repo := worktreeFixture(t)
	admin := dialVerb(t, sp)
	makeSessionWithWindow(t, d, "lead")
	other := makeSessionWithWindow(t, d, "other")
	mine := fakeFan(t, d, admin, repo, "fan/retry", 2, "lead")
	record := func(s string) (string, string) {
		wt := d.manager.GetSession(s).Worktree()
		return wt.JudgeSession, wt.JudgeWindow
	}

	// A judge-fan that fails records nothing.
	r := callP(admin, t, "judge-fan", map[string]any{"session": mine[0], "agent": "/nonexistent/agent", "ready_timeout": 300})
	if r["error"] == nil {
		t.Fatalf("judge-fan with a missing agent answered %v", r["result"])
	}
	for _, s := range mine {
		if js, jw := record(s); js != "" || jw != "" {
			t.Fatalf("a failed judge-fan left a judge on %s: %q %q", s, js, jw)
		}
	}

	// The person starts a judge.
	r = callP(admin, t, "judge-fan", map[string]any{"session": mine[0], "agent": "sleep 60", "ready_timeout": 300})
	res, ok := r["result"].(map[string]any)
	if !ok {
		t.Fatalf("judge-fan failed: %v", r["error"])
	}
	judgeName, _ := res["judge_session"].(string)
	judgeWin, _ := res["window_id"].(string)
	judge := d.manager.GetSession(judgeName)
	if judge == nil || judgeWin == "" {
		t.Fatalf("judge-fan made no judge: %v", res)
	}
	for _, s := range mine {
		if js, jw := record(s); js != judge.ID || jw != judgeWin {
			t.Fatalf("%s names judge %q %q, want %q %q", s, js, jw, judge.ID, judgeWin)
		}
	}
	// A second pane in the judge's session.
	r = callP(admin, t, "new-window", map[string]any{"session": judgeName, "name": "shell"})
	shellRes, _ := r["result"].(map[string]any)
	shellWin, _ := shellRes["window_id"].(string)
	if shellWin == "" {
		t.Fatalf("new-window in the judge's session: %v", r["error"])
	}

	cur := ""
	d.setApprovalPeer(func(*connState) (bool, string) { return true, cur })
	as := func(window string) *verbConn {
		cur = window
		return dialVerb(t, sp)
	}
	firstWindow := func(name string) string { return d.manager.GetSession(name).GetState().Windows[0].ID }
	setStrict(d, "read", "write", "fan")

	// The judge's pane reads an attempt, and cannot type into it or run a
	// command in its session.
	j := as(judgeWin)
	r = callP(j, t, "compare-fan", map[string]any{"session": mine[1], "changes": false})
	if rows, _ := r["result"].(map[string]any)["rows"].([]any); len(rows) != 2 {
		t.Fatalf("the judge reads %d attempts, want 2: %v", len(rows), r)
	}
	// It reads screens and scrollback, and nothing past what judging needs.
	if r := callP(j, t, "capture-pane", map[string]any{"session": mine[1], "window": firstWindow(mine[1])}); r["error"] != nil {
		t.Fatalf("the judge cannot read an attempt's screen: %v", r["error"])
	}
	wantForbidden(t, "command-history on an attempt from the judge", callP(j, t, "command-history", map[string]any{"session": mine[1], "window": firstWindow(mine[1])}))
	for _, verb := range []string{"display-env", "stash-list", "read-agent-messages", "session-info"} {
		wantForbidden(t, verb+" on an attempt from the judge", callP(j, t, verb, map[string]any{"session": mine[1]}))
	}
	wantForbidden(t, "send-keys into an attempt from the judge", callP(j, t, "send-keys", map[string]any{"session": mine[1], "window": firstWindow(mine[1]), "keys": "x"}))
	wantForbidden(t, "verify-fan from the judge", callP(j, t, "verify-fan", map[string]any{"session": mine[1], "command": "true"}))
	// The judge holds read alone: it cannot join the attempts' fan by its
	// stem, nor start an agent in its own session.
	wantForbidden(t, "fan into the attempts' group from the judge", callP(j, t, "fan", map[string]any{"repo": repo, "name": "fan/retry", "count": 1, "agent": "sleep 60", "prompt": "x", "ready_timeout": 300}))
	wantForbidden(t, "start-agent from the judge", callP(j, t, "start-agent", map[string]any{"session": judgeName, "agent": "sleep 60", "ready_timeout": 300}))

	// Another pane of the judge's session reaches nothing of the attempts.
	wantForbidden(t, "compare-fan from another pane of the judge's session", callP(as(shellWin), t, "compare-fan", map[string]any{"session": mine[1], "changes": false}))

	// An attempt cannot record a verdict, nor start a judge of its own.
	att := as(firstWindow(mine[0]))
	wantForbidden(t, "set-fan-verdict from an attempt", callP(att, t, "set-fan-verdict", map[string]any{"session": mine[0], "pick": mine[0], "summary": "Keep A."}))
	wantForbidden(t, "judge-fan from an attempt", callP(att, t, "judge-fan", map[string]any{"session": mine[0], "agent": "sleep 60"}))
	if v := d.manager.GetSession(mine[1]).Worktree().Verdict; v != nil {
		t.Fatalf("an attempt recorded a verdict: %+v", v)
	}
	// An outsider cannot either.
	wantForbidden(t, "set-fan-verdict from an outsider", callP(as(firstWindow(other.Name())), t, "set-fan-verdict", map[string]any{"session": mine[0], "pick": mine[0], "summary": "Keep A."}))

	// The judge records one, and the daemon says it was the judge.
	if r := callP(j, t, "set-fan-verdict", map[string]any{"session": mine[0], "pick": mine[1], "summary": "Keep B."}); r["error"] != nil {
		t.Fatalf("the judge cannot record its verdict: %v", r["error"])
	}
	if v := d.manager.GetSession(mine[0]).Worktree().Verdict; v == nil || v.Pick != mine[1] || v.By != "judge" {
		t.Fatalf("the judge's verdict reads %+v", v)
	}

	// The judge's session ends: the attempts no longer name it, and a
	// session that takes its name reaches nothing.
	d.setApprovalPeer(nil)
	if r := callP(admin, t, "kill-session", map[string]any{"session": judgeName}); r["error"] != nil {
		t.Fatalf("kill-session: %v", r["error"])
	}
	for _, s := range mine {
		if js, jw := record(s); js != "" || jw != "" {
			t.Fatalf("%s still names the ended judge: %q %q", s, js, jw)
		}
	}
	squat := makeSessionWithWindow(t, d, judgeName)
	d.setApprovalPeer(func(*connState) (bool, string) { return true, cur })
	wantForbidden(t, "compare-fan from a session that took the judge's name", callP(as(firstWindow(squat.Name())), t, "compare-fan", map[string]any{"session": mine[1], "changes": false}))
	wantForbidden(t, "set-fan-verdict from a session that took the judge's name", callP(as(firstWindow(squat.Name())), t, "set-fan-verdict", map[string]any{"session": mine[0], "pick": mine[0], "summary": "Keep A."}))

	// A restart brings the judge's session and pane back as a fresh shell:
	// the attempts no longer name it, and that pane reaches nothing.
	d.setApprovalPeer(nil)
	r = callP(admin, t, "judge-fan", map[string]any{"session": mine[0], "agent": "sleep 60", "ready_timeout": 300})
	res, _ = r["result"].(map[string]any)
	j2Name, _ := res["judge_session"].(string)
	j2Win, _ := res["window_id"].(string)
	if j2Name == "" {
		t.Fatalf("judge-fan: %v", r["error"])
	}
	states := []*SessionState{
		d.manager.GetSession(mine[0]).ResurrectionState(),
		d.manager.GetSession(mine[1]).ResurrectionState(),
		d.manager.GetSession(j2Name).ResurrectionState(),
	}
	d2, sp2 := startTestDaemon(t)
	for _, st := range states {
		if _, err := d2.restoreSession(st); err != nil {
			t.Fatalf("restore %s: %v", st.Name, err)
		}
	}
	for _, sn := range mine {
		if wt := d2.manager.GetSession(sn).Worktree(); wt.JudgeSession != "" || wt.JudgeWindow != "" {
			t.Fatalf("%s names a judge after a restart: %q %q", sn, wt.JudgeSession, wt.JudgeWindow)
		}
	}
	d2.setApprovalPeer(func(*connState) (bool, string) { return true, j2Win })
	setStrict(d2, "read", "write", "fan")
	wantForbidden(t, "compare-fan from the judge's pane after a restart", callP(dialVerb(t, sp2), t, "compare-fan", map[string]any{"session": mine[1], "changes": false}))
}
