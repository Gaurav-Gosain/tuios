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

// TestSidebarCloseOffersWorktreeDelete drives the close-confirmation dialog
// the way the rail raises it: a worktree session's row menu takes the
// confirmation, and that dialog must offer to delete the worktree with it. A
// plain session's dialog must offer no such row — the destructive capability
// is invisible where it cannot apply.
func TestSidebarCloseOffersWorktreeDelete(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	repo := testutil.GitRepo(t)
	t.Setenv("TUIOS_WORKTREE_DIR", filepath.Join(base, "worktrees"))

	// A session that is not a worktree, which is what this client attaches to,
	// and one worktree session whose close is the destructive case.
	if out, err := tuiosCLIIn(t, base, t.TempDir(), "new", "plain", "--detach"); err != nil {
		t.Fatalf("create the plain session: %v: %s", err, out)
	}
	if out, err := tuiosCLI(t, base, "worktree", "new", "feat/one", "--repo", repo, "--detach"); err != nil {
		t.Fatalf("worktree new feat/one: %v: %s", err, out)
	}

	term := startIn(t, base, startOpts{args: []string{"attach", "plain"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == 1
	}, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	toggleSidebarViaPalette(t, term)
	waitForAll(t, term, uiTimeout, "sidebar with the worktree session row", sidebarHeader, "feat/one")

	// Right-click the worktree session's row and take its kill row, which is
	// the menu row that raises the close confirmation for that session.
	col, row := findOnScreen(t, term, "feat/one")
	mouseClick(t, term, col, row, tuitest.MouseRight, 0)
	if err := term.WaitForText("Kill session", uiTimeout); err != nil {
		t.Fatalf("the worktree row's menu offered no kill: %v\n%s", err, term.Snapshot())
	}
	col, row = findOnScreen(t, term, "Kill session")
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)

	// The worktree session's dialog carries the third row.
	if err := term.WaitForText("Delete worktree", uiTimeout); err != nil {
		t.Fatalf("the worktree session's close dialog offered no worktree delete: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "close-worktree-dialog")
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("cancel the worktree dialog: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "Delete worktree")
	}, uiTimeout); err != nil {
		t.Fatalf("esc never dismissed the dialog: %v\n%s", err, term.Snapshot())
	}

	// The plain session this client is attached to raises the same dialog from
	// its own close path. It must come up without the delete row.
	if err := term.SendKeys(tuitest.Ctrl('b'), "X"); err != nil {
		t.Fatalf("open the attached session's close dialog: %v", err)
	}
	if err := term.WaitForText("Close session", uiTimeout); err != nil {
		t.Fatalf("the plain session's close dialog never opened: %v\n%s", err, term.Snapshot())
	}
	if text := term.Screen().Text(); strings.Contains(text, "Delete worktree") {
		t.Fatalf("the plain session's close dialog offered a worktree delete\n%s", text)
	}
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("cancel the plain dialog: %v", err)
	}

	alive(t, term, "after closing both dialogs")
}

// TestSidebarCloseDeletesWorktree drives the delete row through its two clean
// endings. Taking it must remove the worktree through the daemon's
// remove-worktree verb: the directory goes, the branch stays, the session
// dies, and the notification says what happened. A worktree whose directory
// was deleted out from under the daemon still closes cleanly, reported as
// already gone rather than as a failure.
func TestSidebarCloseDeletesWorktree(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	repo := testutil.GitRepo(t)
	t.Setenv("TUIOS_WORKTREE_DIR", filepath.Join(base, "worktrees"))

	// A session that is not a worktree, which is what this client attaches to,
	// and two worktree sessions: one deleted while its directory is intact,
	// one deleted after its directory is gone.
	if out, err := tuiosCLIIn(t, base, t.TempDir(), "new", "plain", "--detach"); err != nil {
		t.Fatalf("create the plain session: %v: %s", err, out)
	}
	if out, err := tuiosCLI(t, base, "worktree", "new", "feat/one", "--repo", repo, "--detach"); err != nil {
		t.Fatalf("worktree new feat/one: %v: %s", err, out)
	}
	if out, err := tuiosCLI(t, base, "worktree", "new", "feat/two", "--repo", repo, "--detach"); err != nil {
		t.Fatalf("worktree new feat/two: %v: %s", err, out)
	}
	if out, err := tuiosCLI(t, base, "worktree", "new", "feat/three", "--repo", repo, "--detach"); err != nil {
		t.Fatalf("worktree new feat/three: %v: %s", err, out)
	}

	term := startIn(t, base, startOpts{args: []string{"attach", "plain"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == 1
	}, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	toggleSidebarViaPalette(t, term)
	waitForAll(t, term, uiTimeout, "sidebar with both worktree rows", "feat/one", "feat/two", "feat/three")

	// deleteRow right-clicks a worktree session's row, takes its kill row to
	// raise the close confirmation, and answers it with the delete row. The
	// dialog opens on Cancel, so two downs reach the third answer.
	deleteRow := func(branch string) {
		t.Helper()
		col, row := findOnScreen(t, term, branch)
		mouseClick(t, term, col, row, tuitest.MouseRight, 0)
		if err := term.WaitForText("Kill session", uiTimeout); err != nil {
			t.Fatalf("the %s row's menu offered no kill: %v\n%s", branch, err, term.Snapshot())
		}
		col, row = findOnScreen(t, term, "Kill session")
		mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
		if err := term.WaitForText("Delete worktree", uiTimeout); err != nil {
			t.Fatalf("the %s close dialog offered no worktree delete: %v\n%s", branch, err, term.Snapshot())
		}
		if err := term.SendKeys(tuitest.Down, tuitest.Down, tuitest.Enter); err != nil {
			t.Fatalf("take the delete row for %s: %v", branch, err)
		}
	}

	// sessionGone waits for the daemon to stop listing the session and then
	// for the rail to drop its row. The two are waited in that order and the
	// second waits past the notification, whose text names the branch too.
	sessionGone := func(branch, session string) {
		t.Helper()
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			return !hasName(sessionNames(t, base), session)
		}, uiTimeout); err != nil {
			t.Fatalf("the daemon never killed the %s session: %v", branch, err)
		}
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			return railLineWith(s, branch) < 0
		}, uiTimeout); err != nil {
			t.Fatalf("the sidebar never dropped the %s session: %v\n%s", branch, err, term.Snapshot())
		}
	}

	// A clean worktree: the delete row removes the directory, keeps the
	// branch, kills the session and says so.
	deleteRow("feat/one")
	if err := term.WaitForText("Deleted worktree", uiTimeout); err != nil {
		t.Fatalf("no notification said the worktree was deleted: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "close-worktree-deleted")
	if _, err := os.Stat(filepath.Join(base, "worktrees", "repo", "feat-one")); !os.IsNotExist(err) {
		t.Errorf("the feat/one worktree directory survived the delete row: %v", err)
	}
	if out, err := exec.Command("git", "-C", repo, "show-ref", "--verify", "refs/heads/feat/one").CombinedOutput(); err != nil {
		t.Errorf("the feat/one branch did not survive the delete row: %v: %s", err, out)
	}
	sessionGone("feat/one", "repo-feat-one")

	// A worktree whose directory was already deleted closes cleanly: the
	// session still goes, and the notification reports the directory was
	// already gone rather than reporting a failure.
	if err := os.RemoveAll(filepath.Join(base, "worktrees", "repo", "feat-two")); err != nil {
		t.Fatalf("remove the feat/two directory: %v", err)
	}
	deleteRow("feat/two")
	if err := term.WaitForText("already gone", uiTimeout); err != nil {
		t.Fatalf("the gone worktree's close did not say the directory was already gone: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "close-worktree-gone")
	sessionGone("feat/two", "repo-feat-two")

	// A dirty worktree loses nothing: the delete row stashes the uncommitted
	// changes in the repository and the session goes with the worktree. The
	// drawn notification truncates long notes, so the stash is asserted where
	// it cannot lie: in the repository's own stash list.
	worktreeThree := filepath.Join(base, "worktrees", "repo", "feat-three")
	if err := os.WriteFile(filepath.Join(worktreeThree, "dirty.txt"), []byte("uncommitted\n"), 0o644); err != nil {
		t.Fatalf("dirty the feat/three worktree: %v", err)
	}
	deleteRow("feat/three")
	if err := term.WaitForText("Deleted worktree", uiTimeout); err != nil {
		t.Fatalf("no notification said the dirty worktree was deleted: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "close-worktree-stashed")
	if out, err := exec.Command("git", "-C", repo, "stash", "list").CombinedOutput(); err != nil || !strings.Contains(string(out), "tuios: feat/three") {
		t.Errorf("the feat/three changes did not land in git stash: %v: %s", err, out)
	}
	sessionGone("feat/three", "repo-feat-three")

	alive(t, term, "after deleting both worktrees")
}
