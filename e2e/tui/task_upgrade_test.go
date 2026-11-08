package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// dispatchStatus is `tuios task dispatch status --json`, and the raw text.
func dispatchStatus(t *testing.T, base string) (session.DispatchStatus, string) {
	t.Helper()
	raw := mustCLI(t, base, "task", "dispatch", "status", "--json")
	var st session.DispatchStatus
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		t.Fatalf("task dispatch status --json: %v\n%s", err, raw)
	}
	return st, raw
}

// boardRow is the row of board key in st, or nil.
func boardRow(st session.DispatchStatus, key string) *session.DispatchBoardRow {
	for i := range st.Boards {
		if st.Boards[i].Board == key {
			return &st.Boards[i]
		}
	}
	return nil
}

// TestTaskDispatchSurvivesALiveUpgrade: the person sets a board to pull,
// with a verify command, quiet hours and a pause, and an agent's card is in
// verify when the daemon is upgraded live. After the upgrade every setting
// reads as before, the verify runs again and lands, the run the old image
// started does not run on beside it nor stay as a zombie, and a setting
// changed after the upgrade is the one a cold restart reads back.
//
// How it could pass wrongly: an upgrade that fell back to a cold start
// would load the settings by the cold path, so the daemon's pid must be the
// same and its image new. The verify's own output says how many runs ended,
// so a card moved to review some other way does not count as the resume.
//
// Negative controls (NEGATIVE_CONTROLS.md, TestTaskDispatchSurvivesALiveUpgrade).
func TestTaskDispatchSurvivesALiveUpgrade(t *testing.T) {
	base := t.TempDir()
	startHandoffDaemon(t, base)
	t.Cleanup(func() { dumpDaemonLogOnFail(t, base) })
	const sess = "e2e-upgrade-board"
	const board = "session:" + sess
	mustCLI(t, base, "new", sess, "--detach")
	waitForSessionInfo(t, base, sess)
	var report strings.Builder
	note := func(format string, args ...any) { fmt.Fprintf(&report, format+"\n", args...) }
	defer func() { saveText(t, "task-dispatch-upgrade", report.String()) }()

	repo := filepath.Join(base, "demo")
	lifeRepo(t, repo)
	runs := filepath.Join(base, "verify-runs")
	a := strings.TrimSpace(mustCLI(t, base, "new-window", "-s", sess, "agent-a", "--no-focus", "--print-id", "--cwd", repo))
	mustCLI(t, base, "set-agent-state", "-s", sess, "-w", a, "working", "--harness", "claude-code", "-m", "on it")

	mustCLI(t, base, "task", "dispatch", "mode", "pull", "-s", sess)
	mustCLI(t, base, "task", "dispatch", "quiet", "22:00-07:00")
	mustCLI(t, base, "task", "dispatch", "verify", "-s", sess, "sleep 5; echo ran >> "+runs)
	mustCLI(t, base, "task", "dispatch", "pause")
	before, raw := dispatchStatus(t, base)
	note("before the upgrade:\n%s", raw)

	mustCLI(t, base, "task", "add", "-s", sess, "-p", "do the work", "the work") // t1
	mustCLI(t, base, "task", "claim", "t1", "-s", sess, "-w", a)
	if out := inPane(t, base, sess, a, "task done t1"); !strings.Contains(out, "EXIT=0") {
		t.Fatalf("ASSERTION: tuios task done t1 in the agent's pane failed:\n%s", out)
	}
	waitLife(t, base, sess, uiTimeout, "t1 did not go to verify",
		func(c map[string]lifeCard) bool {
			return c["t1"].Stage == "verify" && c["t1"].Verify != nil && c["t1"].Verify.State == "running"
		})

	img := helloImage(t, base)
	res, upOut, err := daemonUpgrade(t, base, copyTuios(t))
	if err != nil {
		t.Fatalf("ASSERTION: daemon upgrade: %v\n%s", err, upOut)
	}
	after := helloImage(t, base)
	note("upgrade: %v", res)
	if after.PID != img.PID || after.Instance == img.Instance || res["fallback"] != false {
		t.Fatalf("ASSERTION: the upgrade was not a live one: %+v, then %+v, %v", img, after, res)
	}

	got, raw := dispatchStatus(t, base)
	note("after the upgrade:\n%s", raw)
	row, want := boardRow(got, board), boardRow(before, board)
	if want == nil || want.Mode != "pull" || !before.Paused || before.Quiet != "22:00-07:00" {
		t.Fatalf("ASSERTION: the settings were not set before the upgrade:\n%s", raw)
	}
	if row == nil || row.Mode != want.Mode || row.Verify != want.Verify || got.Paused != before.Paused || got.Quiet != before.Quiet {
		t.Errorf("ASSERTION: after the live upgrade the dispatch settings read board %+v, paused %v, quiet %q; want board %+v, paused %v, quiet %q",
			row, got.Paused, got.Quiet, *want, before.Paused, before.Quiet)
	}

	// The verify that ran at the upgrade runs again and lands.
	cards, raw := waitLife(t, base, sess, 30*time.Second, "t1 did not reach review after a live upgrade mid-verify",
		func(c map[string]lifeCard) bool { return c["t1"].Stage == "review" })
	note("after the resumed verify:\n%s", raw)
	if cards["t1"].Verify == nil || cards["t1"].Verify.State != "passed" {
		t.Errorf("ASSERTION: t1 in review has verify %+v, want passed", cards["t1"].Verify)
	}
	// The old image's run would end 5 s after it started; give it that long.
	time.Sleep(6 * time.Second)
	ran, _ := os.ReadFile(runs)
	children := childCommands(t, after.PID)
	note("verify runs: %q\ndaemon children: %q", ran, children)
	if n := strings.Count(string(ran), "ran"); n != 1 {
		t.Errorf("ASSERTION: the verify command ran to its end %d times across the upgrade, want 1 (the old run stopped, the new one ran)", n)
	}
	for _, c := range children {
		if strings.Contains(c, "<defunct>") || strings.Contains(c, "sleep 5") {
			t.Errorf("ASSERTION: the daemon has a child left from the old image's verify: %s", c)
		}
	}

	// A change made after the upgrade is saved: a cold restart reads it.
	mustCLI(t, base, "task", "dispatch", "quiet", "off")
	mustCLI(t, base, "task", "dispatch", "resume")
	if out, err := handoffCLI(t, base, 30*time.Second, "kill-server"); err != nil {
		t.Fatalf("kill-server: %v\n%s", err, out)
	}
	if out, err := handoffCLI(t, base, 30*time.Second, "start-server"); err != nil {
		t.Fatalf("start-server: %v\n%s", err, out)
	}
	waitForSessionInfo(t, base, sess)
	cold, raw := dispatchStatus(t, base)
	note("after a cold restart:\n%s", raw)
	if cold.Paused || cold.Quiet != "" {
		t.Errorf("ASSERTION: a cold restart reads paused %v and quiet %q, the settings from before the upgrade; the resume and quiet off made after it were not saved",
			cold.Paused, cold.Quiet)
	}
	if r := boardRow(cold, board); r == nil || r.Mode != "pull" {
		t.Errorf("ASSERTION: a cold restart reads board %+v, want pull", r)
	}
}
