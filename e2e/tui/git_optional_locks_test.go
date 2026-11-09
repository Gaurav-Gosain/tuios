package tuie2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestTuiosGitTakesNoOptionalLock runs a review fan with an attached client,
// opens the review and lists the worktrees, with a git on PATH that records
// every git tuios starts. Each must run with GIT_OPTIONAL_LOCKS=0.
//
// git status, and a diff against the work tree, refresh the index and write it
// back under .git/index.lock when they may. tuios runs them in the background
// on the person's worktrees: for the rail, the review, the worktree list. One
// that holds the lock when the person (or an agent, or a test) runs git
// commit, checkout or add there makes that command fail with "Unable to
// create '.git/index.lock': File exists". That is the race the review test's
// fixture hit (TestReviewFileKeysFollowTheirBindings). With optional locks
// off, a read never writes the index, and the person's command never sees a
// lock tuios holds.
//
// The recorder marks tuios's git by GIT_SEQUENCE_EDITOR=true, which
// childproc.GitEnv sets and the test's own git does not. The calls it records
// are kept under artifactDir as git-calls.txt.
func TestTuiosGitTakesNoOptionalLock(t *testing.T) {
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	wrapper := "#!/bin/sh\nif [ \"$GIT_SEQUENCE_EDITOR\" = true ]; then printf '%s\\t%s\\n' \"${GIT_OPTIONAL_LOCKS-unset}\" \"$*\" >> '" + log + "'; fi\nexec '" + real + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(wrapper), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	base, repo := fanFixture(t)
	session, path := reviewFanAt(t, base, repo, "locks")
	if err := os.WriteFile(filepath.Join(path, "README"), []byte("hello\nchanged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	term := attachIn(t, base, session, startOpts{})
	sendKeys(t, term, tuitest.Ctrl('b'), "v")
	waitScreen(t, term, "the review never opened", "Review", "README")
	sendKeys(t, term, tuitest.Esc)
	for range 3 {
		_ = worktreeRows(t, base, "--group", "try/locks")
		time.Sleep(time.Second)
	}

	raw, _ := os.ReadFile(log)
	_ = os.WriteFile(filepath.Join(artifactDir(t), "git-calls.txt"), raw, 0o644)
	reads := 0
	var bad []string
	for line := range strings.SplitSeq(strings.TrimSpace(string(raw)), "\n") {
		locks, args, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		if strings.HasPrefix(args, "status") || strings.HasPrefix(args, "diff") {
			reads++
		}
		if locks != "0" {
			bad = append(bad, line)
		}
	}
	if reads == 0 {
		t.Fatalf("the recorder saw no git status or diff from tuios; the test drove nothing:\n%s", raw)
	}
	if len(bad) > 0 {
		t.Fatalf("ASSERTION: %d of tuios's git calls may take the index's optional lock (GIT_OPTIONAL_LOCKS is not 0):\n%s", len(bad), strings.Join(bad, "\n"))
	}
	alive(t, term, "after the git calls")
}
