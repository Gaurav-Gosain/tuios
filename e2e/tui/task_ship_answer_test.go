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

// TestReadyToMergeAnswerStaysWithItsCard: two cards in review, each with its
// work in its own repository. The first one's commit takes seconds (its
// repository has a slow pre-commit hook). The person starts it with M then
// c, closes Ready to merge with esc while it runs, and opens Ready to merge
// on the second card. It holds:
//
//   - the first commit's answer leaves the second card's Ready to merge
//     open, and the toast names the first card;
//   - the second card's repository gets no commit.
//
// How this could pass wrongly, written down first:
//   - The answer could come before the second sheet is open. The test waits
//     for the second sheet, then for the commit in the first repository, and
//     fails when the commit was there before the second sheet.
//   - The toast could name the card from somewhere else on the screen. The
//     check reads the words "Commit on t1 done."
//
// Negative control, recorded in NEGATIVE_CONTROLS.md: on main the answer
// closes the second card's sheet and says "Commit done." with no card.
func TestReadyToMergeAnswerStaysWithItsCard(t *testing.T) {
	base, _ := fanFixture(t)
	useShippedLooks(base)
	api, web := filepath.Join(base, "dev", "api"), filepath.Join(base, "dev", "web")
	for _, dir := range []string{api, web} {
		lifeRepo(t, dir)
		testutil.Git(t, dir, "config", "user.email", "dev@example.com")
		testutil.Git(t, dir, "config", "user.name", "Dev")
	}
	hook := filepath.Join(api, ".git", "hooks", "pre-commit")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nsleep 6\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustCLI(t, base, "new", "home", "--detach", "--cwd", api)
	waitForSessionInfo(t, base, "home")
	mustCLI(t, base, "task", "dispatch", "verify", "-s", "home", "true")
	mustCLI(t, base, "task", "add", "-s", "home", "-p", "Do it", "Fix the retry")
	mustCLI(t, base, "task", "add", "-s", "home", "-p", "Do it", "Write the docs")
	for _, c := range []struct{ id, dir, file string }{{"t1", api, "retry.go"}, {"t2", web, "docs.md"}} {
		w := strings.TrimSpace(mustCLI(t, base, "new-window", "-s", "home", "agent-"+c.id, "--no-focus", "--print-id", "--cwd", c.dir))
		mustCLI(t, base, "set-agent-state", "-s", "home", "-w", w, "working", "--harness", "claude-code", "-m", "on it")
		if err := os.WriteFile(filepath.Join(c.dir, c.file), []byte("work for "+c.id+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		mustCLI(t, base, "task", "claim", c.id, "-s", "home", "-w", w)
		if out := inPane(t, base, "home", w, "task done "+c.id); !strings.Contains(out, "EXIT=0") {
			t.Fatalf("tuios task done %s: %s", c.id, out)
		}
	}
	waitLife(t, base, "home", uiTimeout, "the two cards did not reach review",
		func(c map[string]lifeCard) bool { return c["t1"].Stage == "review" && c["t2"].Stage == "review" })
	commits := func(dir string) string { return strings.TrimSpace(testutil.Git(t, dir, "rev-list", "--count", "HEAD")) }
	webBefore := commits(web)

	term := attachIn(t, base, "home", startOpts{cols: 160, rows: 45, shippedLooks: true})
	sendKeys(t, term, tuitest.Ctrl('b'), "i")
	waitText(t, term, "the agents view", "b board")
	sendKeys(t, term, "b")
	waitText(t, term, "the board", "Verify passed")
	sendKeys(t, term, "B")
	// j walks the cards. M opens Ready to merge on the card under the
	// cursor, and its key bar names the card.
	ready := func(id string) {
		t.Helper()
		for i := 0; ; i++ {
			sendKeys(t, term, "M")
			_ = term.WaitStable(2 * time.Second)
			text := term.Screen().Text()
			if strings.Contains(text, "open PR") && strings.Contains(text, "Ready to merge "+id) {
				return
			}
			if strings.Contains(text, "open PR") {
				sendKeys(t, term, tuitest.Esc)
				_ = term.WaitStable(2 * time.Second)
			}
			if i == 20 {
				t.Fatalf("ASSERTION: Ready to merge never opened on %s:\n%s", id, text)
			}
			sendKeys(t, term, "j")
			_ = term.WaitStable(2 * time.Second)
		}
	}
	ready("t1")
	sendKeys(t, term, "c")
	time.Sleep(insertGuard)
	sendKeys(t, term, tuitest.Esc)
	ready("t2")
	if commits(api) != "1" {
		t.Fatalf("the first commit landed before the second sheet opened; the test proves nothing")
	}
	dir := artifactDir(t)
	deadline := time.Now().Add(30 * time.Second)
	for commits(api) == "1" {
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: the commit on t1 never landed\n%s", term.Snapshot())
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "Commit on t1 done.") || strings.Contains(s.Text(), "Commit done.")
	}, uiTimeout); err != nil {
		t.Errorf("ASSERTION: no toast says the commit is done\n%s", term.Snapshot())
	}
	_ = term.WaitStable(uiTimeout)
	saveCockpitFrames(t, term, "ship-answer-160x45")
	text := term.Screen().Text()
	if !strings.Contains(text, "Commit on t1 done.") {
		t.Errorf("ASSERTION: the toast does not name the card the commit was for\n%s", text)
	}
	if !strings.Contains(text, "open PR") || !strings.Contains(text, "Ready to merge t2") {
		t.Errorf("ASSERTION: t1's answer closed Ready to merge on t2\n%s", text)
	}
	if got := commits(web); got != webBefore {
		t.Errorf("ASSERTION: t2's repository has %s commits, want %s", got, webBefore)
	}
	t.Logf("frames in %s", dir)
}
