package main

import (
	"strings"
	"testing"
)

// TestRemovedWorktreeSentencesSayWhereTheWorkWent pins the three outcomes of a
// removal as the person reads them: stashed, discarded, or a directory that was
// already gone. Each names the branch as kept, because that is the promise.
func TestRemovedWorktreeSentencesSayWhereTheWorkWent(t *testing.T) {
	stashed := removedWorktree{Session: "api-x", Branch: "x", Path: "/wt/x", Changes: 2, Stashed: true, StashMessage: "tuios: x", SessionKilled: true}.sentences()
	for _, want := range []string{"Removed worktree /wt/x. Branch x is kept.", "2 uncommitted changes are in git stash as 'tuios: x'.", "Killed session 'api-x'."} {
		if !strings.Contains(stashed, want) {
			t.Errorf("stashed sentences lack %q:\n%s", want, stashed)
		}
	}
	discarded := removedWorktree{Session: "api-x", Branch: "x", Path: "/wt/x", Changes: 1, Discarded: true}.sentences()
	for _, want := range []string{"1 uncommitted change was discarded.", "Session 'api-x' is still running."} {
		if !strings.Contains(discarded, want) {
			t.Errorf("discarded sentences lack %q:\n%s", want, discarded)
		}
	}
	gone := removedWorktree{Session: "api-x", Branch: "x", Gone: true, Note: "The directory was already gone.", SessionKilled: true}.sentences()
	if !strings.HasPrefix(gone, "The directory was already gone.") || strings.Contains(gone, "Removed worktree") {
		t.Errorf("gone sentences claim a removal:\n%s", gone)
	}
}

// TestWorktreeTableShowsGoneAndPromptStatus pins the two columns a person
// scans first when watching a fan-out: what became of the prompt, and whether
// the directory is still there.
func TestWorktreeTableShowsGoneAndPromptStatus(t *testing.T) {
	two := 2
	out := renderWorktreeTable([]worktreeRow{
		{Session: "api-a", Repo: "api", Branch: "fan/a", State: "working", PromptStatus: "sent", Changes: &two},
		{Session: "api-b", Repo: "api", Branch: "fan/b", State: "none", PromptStatus: "not_sent", Gone: true},
	})
	for _, want := range []string{"api-a", "fan/a", "working", "sent", "gone", "not sent"} {
		if !strings.Contains(out, want) {
			t.Errorf("table lacks %q:\n%s", want, out)
		}
	}
}
