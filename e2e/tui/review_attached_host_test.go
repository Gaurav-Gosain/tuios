package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestReviewWhileAttachedThroughAHost opens the review (prefix v) on a pane of
// a session on a linked machine, alpha, from a client attached there (#2426).
// The pane's repository is on alpha, so alpha's daemon reads the diff.
//
// The ways it can go wrong, each an assertion: the review refuses because the
// session is on another machine; the diff is asked of this machine's daemon,
// which has no such pane and says so; the overlay opens without the change.
func TestReviewWhileAttachedThroughAHost(t *testing.T) {
	f := startGlobalFleet(t)
	repo := filepath.Join(f.alpha, "farproj")
	lifeRepo(t, repo)
	if err := os.WriteFile(filepath.Join(repo, "far_note.txt"), []byte("a change made on alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := cliWithin(t, f.alpha, f.env, "new-window", "-s", "far", "--cwd", repo, "far-repo"); err != nil {
		t.Fatalf("open a pane in the repository on alpha: %v\n%s", err, out)
	}
	term := startIn(t, f.here, startOpts{args: []string{"attach", "--host", "alpha", "far"}, cols: 140, rows: 44,
		env: append([]string{"TERM=xterm-256color", "COLORTERM=truecolor", "TUIOS_KITTY_GRAPHICS=0"}, f.env...)})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 2 }, bootTimeout); err != nil {
		t.Fatalf("the client never attached through alpha: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	if out, err := cliWithin(t, f.alpha, f.env, "focus-window", "-s", "far", "far-repo"); err != nil {
		t.Fatalf("focus far-repo on alpha: %v\n%s", err, out)
	}
	repoPane := alphaWindowID(t, f, "far-repo")
	if err := term.WaitFor(func(_ tuitest.Screen) bool { return alphaFocused(t, f) == repoPane }, uiTimeout); err != nil {
		t.Fatalf("far-repo never took the focus: %v", err)
	}
	_ = term.WaitStable(uiTimeout)

	if err := term.SendKeys(tuitest.Ctrl('b'), "v"); err != nil {
		t.Fatalf("press ctrl+b v: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "far_note.txt") && strings.Contains(text, "a change made on alpha")
	}, uiTimeout); err != nil {
		saveCockpitFrames(t, term, "attached-review-missing")
		t.Fatalf("ASSERTION: the review of the pane on alpha did not show its change: %v\n%s", err, term.Snapshot())
	}
	if text := term.Screen().Text(); strings.Contains(text, "Review it on that machine") || strings.Contains(text, "No git repository") {
		t.Fatalf("ASSERTION: the review was refused or read on this machine:\n%s", term.Snapshot())
	}
	saveCockpitFrames(t, term, "attached-review-open")
	alive(t, term, "after the review on alpha")
}
