package tuie2e

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// The task state a live upgrade must keep beside the cards: which cards a
// pane holds in another session or on another machine (remoteHolds), and
// which cards auto runs (its cap). State machine plan S3, PR 1, and
// notes/sm2/design-S3.md (H1, H2, H3).
//
// Negative controls (NEGATIVE_CONTROLS.md, "Task holds across a live upgrade").

// sessionsWith counts the sessions in `tuios ls` whose name has sub.
func sessionsWith(t *testing.T, base, sub string) (int, string) {
	t.Helper()
	ls := mustCLI(t, base, "ls")
	return strings.Count(ls, sub), ls
}

// TestAutoCardsKeepTheirHoldsAcrossALiveUpgrade: an auto board with a cap of
// one runs an agent on t1 in its own worktree session, and t2 waits at the
// cap. After `tuios daemon upgrade` a new card (a kick) starts no second
// agent: auto still counts the one it runs. Then the agent's pane closes:
// t1 is let go, as before the upgrade, and the freed slot starts t2.
//
// How it could pass wrongly: a second start could come late, so the test
// waits past a start of the stand-in agent before it counts the sessions; an
// upgrade that fell back to a cold start lets every holder go by itself, so
// t1 must still be held right after it, and the daemon's pid kept.
func TestAutoCardsKeepTheirHoldsAcrossALiveUpgrade(t *testing.T) {
	base, origin := fanFixture(t)
	t.Setenv("TUIOS_WORKTREE_DIR", "")
	var report strings.Builder
	note := func(format string, args ...any) { fmt.Fprintf(&report, format+"\n", args...) }
	defer func() { saveText(t, "task-adopt-auto", report.String()) }()
	t.Cleanup(func() { dumpDaemonLogOnFail(t, base) })
	here := filepath.Join(base, "work", "api")
	cloneWithOrigin(t, origin, here, "git@example.com:acme/api")
	mustCLI(t, base, "new", "home", "--detach", "--cwd", here)
	waitForSessionInfo(t, base, "home")

	note("%s", mustCLI(t, base, "task", "dispatch", "cap", "1"))
	mustCLI(t, base, "task", "add", "-s", "home", "-p", "One.", "First card")  // t1
	mustCLI(t, base, "task", "add", "-s", "home", "-p", "Two.", "Second card") // t2
	note("%s", mustCLI(t, base, "task", "dispatch", "mode", "auto", "--agent", "claude", "-s", "home"))
	cards, _ := waitAutoCards(t, base, "home", "auto did not start t1", func(c map[string]autoCard) bool { return held(c, "t1") })
	agent := cards["t1"].Holder.Window
	untilTrue(t, 30*time.Second, "the agent of t1 never came up", func() bool {
		return claudeAgents(t, mustCLI(t, base, "list-agents", "--all-sessions", "--json")) == 1
	})
	note("before the upgrade:\n%s", waitDispatchText(t, base, "at the cap of 1", "t2 does not wait at the cap"))

	img := helloImage(t, base)
	res, out, err := daemonUpgrade(t, base, copyTuios(t))
	if err != nil {
		t.Fatalf("ASSERTION: daemon upgrade: %v\n%s", err, out)
	}
	if after := helloImage(t, base); after.PID != img.PID || after.Instance == img.Instance || res["fallback"] != false {
		t.Fatalf("ASSERTION: the upgrade was not a live one: %v", res)
	}
	cards, raw := autoCards(t, base, "home")
	if !held(cards, "t1") || cards["t1"].Holder.Window != agent {
		t.Fatalf("ASSERTION: t1 is not held by its agent right after the upgrade:\n%s", raw)
	}

	// A kick after the upgrade: the cap still holds.
	mustCLI(t, base, "task", "add", "-s", "home", "-p", "Three.", "Third card") // t3
	time.Sleep(8 * time.Second)
	n, ls := sessionsWith(t, base, "api-task-")
	status := mustCLI(t, base, "task", "dispatch", "-s", "home")
	note("after the upgrade and a new card:\nsessions:\n%s\nstatus:\n%s", ls, status)
	if n != 1 {
		t.Errorf("ASSERTION: %d auto worktree sessions after the upgrade, want 1: auto lost count of the agent it runs and went past its cap of 1", n)
	}
	if !strings.Contains(status, "1 running") {
		t.Errorf("ASSERTION: the status after the upgrade does not say 1 running:\n%s", status)
	}

	// The agent's pane closes: t1 is let go, and t2 takes the slot.
	mustCLI(t, base, "close-window", "-s", "api-task-first-card", agent)
	_, raw = waitAutoCards(t, base, "home", "t1 was not let go when its agent's pane closed after the upgrade",
		func(c map[string]autoCard) bool { return !held(c, "t1") })
	note("after the agent's pane closed:\n%s", raw)
	_, raw = waitAutoCards(t, base, "home", "the slot t1 freed did not start t2",
		func(c map[string]autoCard) bool { return held(c, "t2") })
	note("after the slot was freed:\n%s", raw)
}

// TestAFarCardIsLetGoAfterALiveUpgrade: home's auto board starts an agent on
// build for t1, so a pane on build holds home's card. build's daemon is
// upgraded live, then the pane closes: build must still tell home, and t1 is
// let go there.
//
// How it could pass wrongly: home could let the card go on its own if the
// link dropped for good, so the link is shown up after the upgrade (the card
// still held by build's pane), and the release is read on home within the
// time a release over the link takes.
func TestAFarCardIsLetGoAfterALiveUpgrade(t *testing.T) {
	pp := startPool(t, 2, true)
	t.Cleanup(func() { dumpDaemonLogOnFail(t, pp.base); dumpDaemonLogOnFail(t, pp.remote) })
	var report strings.Builder
	note := func(format string, args ...any) { fmt.Fprintf(&report, format+"\n", args...) }
	defer func() { saveText(t, "task-adopt-far", report.String()) }()

	const proj = "https://example.com/acme/api.git"
	mustCLI(t, pp.base, "task", "add", "-s", "home", "--project", proj, "-p", "Retry on a 503.", "Fix the retry") // t1
	note("%s", mustCLI(t, pp.base, "task", "dispatch", "mode", "auto", "--agent", "claude", "--machine", "build", "--project", proj, "-s", "home"))
	cards, raw := waitAutoCards(t, pp.base, "home", "auto did not start t1 on build",
		func(c map[string]autoCard) bool { return held(c, "t1") && c["t1"].Holder.Host != "" })
	note("t1 started on build:\n%s", raw)
	window := cards["t1"].Holder.Window
	untilTrue(t, 30*time.Second, "the agent of t1 never came up on build", func() bool {
		return claudeAgents(t, mustCLI(t, pp.remote, "list-agents", "--all-sessions", "--json")) == 1
	})

	img := helloImage(t, pp.remote)
	res, out, err := daemonUpgrade(t, pp.remote, copyTuios(t))
	if err != nil {
		t.Fatalf("ASSERTION: daemon upgrade on build: %v\n%s", err, out)
	}
	if after := helloImage(t, pp.remote); after.PID != img.PID || res["fallback"] != false {
		t.Fatalf("ASSERTION: build's upgrade was not a live one: %v", res)
	}
	note("build upgraded: %v", res)
	if cards, raw := autoCards(t, pp.base, "home"); !held(cards, "t1") {
		t.Fatalf("ASSERTION: t1 is not held right after build's upgrade:\n%s", raw)
	}

	// Close build's pane by its session.
	ls := ansiCSI.ReplaceAllString(mustCLI(t, pp.remote, "ls"), "")
	sess := regexp.MustCompile(`[a-z0-9-]*task-fix-the-retry[a-z0-9-]*`).FindString(ls)
	if sess == "" {
		t.Fatalf("no session on build holds t1's agent:\n%s", ls)
	}
	mustCLI(t, pp.remote, "close-window", "-s", sess, window)
	_, raw = waitAutoCards(t, pp.base, "home", "t1 on home was not let go when its pane on build closed after build's upgrade",
		func(c map[string]autoCard) bool { return !held(c, "t1") })
	note("after build's pane closed:\n%s", raw)
}

// ansiCSI matches the colour sequences of tuios ls.
var ansiCSI = regexp.MustCompile("\x1b\\[[0-9;]*m")

// untilTrue polls ok until it holds, within limit.
func untilTrue(t *testing.T, limit time.Duration, why string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: %s", why)
		}
		time.Sleep(200 * time.Millisecond)
	}
}
