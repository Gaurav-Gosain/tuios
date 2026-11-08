package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
	"github.com/Gaurav-Gosain/tuitest"
)

// TestTheBoardShowsTheCardLifecycle: three cards an agent was done with, one
// whose verify failed, one that passed, one still verifying. The board says
// each (Attempt 1 failed and why, Verify passed, Verifying). On the card in
// review, v opens the Changes view of the agent's worktree, and M is Ready to
// merge: c commits the work with the card's title, and p shows what a push
// sends, then enter pushes it to the repository's origin.
//
// How it could pass wrongly: the words could come from somewhere else on the
// screen, so the commit and the push are checked in git itself: the subject
// is the card's title, and the origin has the commit.
//
// Negative control: with ckLifeLine returning nothing, "Attempt 1 failed" is
// not on the board.
func TestTheBoardShowsTheCardLifecycle(t *testing.T) {
	base, src := fanFixture(t)
	useShippedLooks(base)
	origin := filepath.Join(base, "origin.git")
	testutil.Git(t, base, "clone", "-q", "--bare", src, origin)
	here := filepath.Join(base, "dev", "api")
	testutil.Git(t, base, "clone", "-q", origin, here)
	testutil.Git(t, here, "config", "user.email", "dev@example.com")
	testutil.Git(t, here, "config", "user.name", "Dev")
	mustCLI(t, base, "new", "home", "--detach", "--cwd", here)
	waitForSessionInfo(t, base, "home")
	a := strings.TrimSpace(mustCLI(t, base, "new-window", "-s", "home", "agent", "--no-focus", "--print-id", "--cwd", here))
	mustCLI(t, base, "set-agent-state", "-s", "home", "-w", a, "working", "--harness", "claude-code", "-m", "on it")
	ok, hang := filepath.Join(base, "ok"), filepath.Join(base, "hang")
	mustCLI(t, base, "task", "dispatch", "verify", "-s", "home",
		"if [ -f "+hang+" ]; then sleep 600; fi; test -f "+ok+` || { echo "go test: 2 tests failed"; exit 1; }`)
	for _, title := range []string{"Fix the retry", "Write the retry docs", "Add a timeout"} {
		mustCLI(t, base, "task", "add", "-s", "home", "-p", "Do it: "+title, title)
	}
	doneWith := func(id string) {
		t.Helper()
		mustCLI(t, base, "task", "claim", id, "-s", "home", "-w", a)
		if out := inPane(t, base, "home", a, "task done "+id); !strings.Contains(out, "EXIT=0") {
			t.Fatalf("tuios task done %s: %s", id, out)
		}
	}
	doneWith("t2")
	waitLife(t, base, "home", uiTimeout, "t2 did not fail its verify",
		func(c map[string]lifeCard) bool { return c["t2"].Attempts == 1 })
	if err := os.WriteFile(ok, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(here, "retry.go"), []byte("package api\n\n// Retry tries a call again on a 503.\nfunc Retry() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	doneWith("t1")
	waitLife(t, base, "home", uiTimeout, "t1 did not pass its verify",
		func(c map[string]lifeCard) bool { return c["t1"].Stage == "review" })
	if err := os.WriteFile(hang, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	doneWith("t3")
	waitLife(t, base, "home", uiTimeout, "t3 is not verifying",
		func(c map[string]lifeCard) bool { return c["t3"].Stage == "verify" })

	term := attachIn(t, base, "home", startOpts{cols: 160, rows: 45, shippedLooks: true})
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the agents view", "b board")
	if err := term.SendKeys("b"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the lifecycle on the board", "Attempt 1 failed", "Verify passed", "Verifying")
	_ = term.WaitStable(uiTimeout)
	saveCockpitFrames(t, term, "board-lifecycle-160x45")

	// The list, where j walks the cards: to t1, the card in review.
	if err := term.SendKeys("B"); err != nil {
		t.Fatal(err)
	}
	for i := 0; !strings.Contains(term.Screen().Text(), "ready to merge"); i++ {
		if i == 20 {
			t.Fatalf("ASSERTION: no card offered ready to merge:\n%s", term.Screen().Text())
		}
		if err := term.SendKeys("j"); err != nil {
			t.Fatal(err)
		}
		_ = term.WaitStable(2 * time.Second)
	}
	_ = term.WaitStable(uiTimeout)
	saveCockpitFrames(t, term, "board-lifecycle-review-160x45")

	// v: the Changes view of the agent's worktree.
	if err := term.SendKeys("v"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the Changes view of t1's work", "retry.go")
	_ = term.WaitStable(uiTimeout)
	saveCockpitFrames(t, term, "board-lifecycle-changes-160x45")
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatal(err)
	}
	_ = term.WaitStable(uiTimeout)

	// M, c: commit with the card's title.
	if err := term.SendKeys("M"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "Ready to merge", "open PR")
	saveCockpitFrames(t, term, "board-lifecycle-ready-160x45")
	if err := term.SendKeys("c"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the commit", "Commit done")
	if got := testutil.Git(t, here, "log", "-1", "--format=%s"); strings.TrimSpace(got) != "Fix the retry" {
		t.Fatalf("ASSERTION: the last commit is %q, want the card's title", got)
	}

	// M, p: what the push sends, then enter pushes.
	if err := term.SendKeys("M"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "Ready to merge", "open PR")
	if err := term.SendKeys("p"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "what the push sends", "Push", "to origin")
	_ = term.WaitStable(uiTimeout)
	saveCockpitFrames(t, term, "board-lifecycle-push-160x45")
	if got := testutil.Git(t, origin, "log", "-1", "--format=%s"); strings.TrimSpace(got) == "Fix the retry" {
		t.Fatalf("ASSERTION: the push went before enter")
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the push", "Push done")
	if got := testutil.Git(t, origin, "log", "-1", "--format=%s"); strings.TrimSpace(got) != "Fix the retry" {
		t.Fatalf("ASSERTION: the origin's last commit is %q, want the card's", got)
	}
}
