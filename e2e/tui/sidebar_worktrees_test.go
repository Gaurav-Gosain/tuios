package tuie2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
	"github.com/Gaurav-Gosain/tuitest"
)

// The rail's worktree groups, end to end: a throwaway repository, two
// worktree sessions made by the command a person runs, a real daemon, and a
// real client drawing the rail. Nothing here touches any repository but the
// one testutil.GitRepo makes under the test.

// tuiosCLIIn is tuiosCLI with a working directory of its own. It exists for
// the one command whose directory decides what the daemon records: this suite
// runs inside a git worktree of tuios itself, so a session created from here
// is a worktree session and cannot be the control.
func tuiosCLIIn(t *testing.T, base, dir string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(tuiosBin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		cmd.Env = append(cmd.Env, key+"="+filepath.Join(base, key))
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// railLineWith is the screen row holding needle, or -1. The rail is at the top
// of the screen, so the topmost match is the rail's row.
func railLineWith(s tuitest.Screen, needle string) int {
	_, rows := s.Size()
	for r := 0; r < rows; r++ {
		if strings.Contains(s.Line(r), needle) {
			return r
		}
	}
	return -1
}

// TestSidebarGroupsWorktrees drives the whole promise the way a person meets
// it: two worktree sessions of one repository appear under one row named after
// the repository, labelled by their branches, and a click on that row folds
// them away and says how many it is holding.
//
// The name is short on purpose. The harness roots XDG_RUNTIME_DIR at
// t.TempDir(), whose path carries the test's name, and the daemon's unix
// socket under it has 108 bytes to live in.
func TestSidebarGroupsWorktrees(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	repo := testutil.GitRepo(t)
	// The worktrees land under the test's own directory. tuiosCLI passes this
	// process's environment through, so the daemon it starts sees it too.
	t.Setenv("TUIOS_WORKTREE_DIR", filepath.Join(base, "worktrees"))

	// A session that is not a worktree, which is the control on screen and the
	// one this client attaches to.
	// It is created from a directory that is in no repository, and it starts
	// the daemon, so the daemon's own directory is that one too.
	if out, err := tuiosCLIIn(t, base, t.TempDir(), "new", "plain", "--detach"); err != nil {
		t.Fatalf("create the plain session: %v: %s", err, out)
	}
	for _, branch := range []string{"feat/one", "feat/two"} {
		out, err := tuiosCLI(t, base, "worktree", "new", branch, "--repo", repo, "--detach")
		if err != nil {
			t.Fatalf("worktree new %s: %v: %s", branch, err, out)
		}
		if !strings.Contains(out, "Created session '") {
			t.Fatalf("worktree new %s did not report a session:\n%s", branch, out)
		}
	}

	term := startIn(t, base, startOpts{args: []string{"attach", "plain"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == 1
	}, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)

	toggleSidebarViaPalette(t, term)

	// The repository row and both branches under it. The wait is what makes
	// this a test of the daemon's record rather than of the CLI: the rows only
	// appear once the client's listing carries the worktree of each session.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "· repo") &&
			strings.Contains(text, "├─ feat/one") &&
			strings.Contains(text, "└─ feat/two")
	}, bootTimeout); err != nil {
		t.Fatalf("the rail never grouped the worktree sessions: %v\n%s", err, term.Snapshot())
	}

	// The parent is above its members, and the plain session keeps its own row.
	screen := term.Screen()
	parent := railLineWith(screen, "· repo")
	first := railLineWith(screen, "├─ feat/one")
	if parent < 0 || first < 0 || parent >= first {
		t.Errorf("the repository row is on line %d and its first branch on %d, want the row above\n%s",
			parent, first, term.Snapshot())
	}
	if railLineWith(screen, "plain") < 0 {
		t.Errorf("the session that is not a worktree lost its row:\n%s", term.Snapshot())
	}
	saveFrame(t, term, "rail-worktrees")

	// A click on the repository row folds the group.
	col, row := findOnScreen(t, term, "· repo")
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		if strings.Contains(text, "feat/one") || strings.Contains(text, "feat/two") {
			return false
		}
		line := railLineWith(s, "repo")
		return line >= 0 && strings.Contains(s.Line(line), "2")
	}, uiTimeout); err != nil {
		t.Fatalf("the click did not fold the group into a row carrying its count: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "rail-worktrees-collapsed")

	alive(t, term, "after folding a worktree group")
}
