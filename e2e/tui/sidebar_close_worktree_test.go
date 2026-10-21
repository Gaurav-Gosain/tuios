package tuie2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	// dialog opens on Cancel, so two downs reach the third answer. The toll
	// is what the dialog says the delete row will take; the row waits for
	// it, because it is fetched from the daemon while the dialog is open.
	deleteRow := func(branch, toll string) {
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
		if err := term.WaitForText(toll, uiTimeout); err != nil {
			t.Fatalf("the %s close dialog said nothing about the worktree's toll: %v\n%s", branch, err, term.Snapshot())
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
		// The kill confirmed itself on the rail, so if it posted its own note
		// it is on screen now. It must never be: the deletion note is the one
		// record of where the changes went, and the kill's note would
		// replace it with less news.
		if strings.Contains(term.Snapshot(), "Killed session") {
			t.Errorf("the kill's own note replaced the deletion note after %s", branch)
		}
	}

	// A worktree that only git-ignores a file: the dialog says the ignored
	// file goes unsaved, and going through with it deletes the file — git
	// worktree remove takes ignored files without --force, and nothing
	// stashes them. The assertions are on the file and the repository's
	// stash list, which stays empty for this branch.
	one := filepath.Join(base, "worktrees", "repo", "feat-one")
	if err := os.WriteFile(filepath.Join(one, ".gitignore"), []byte(".env\n"), 0o644); err != nil {
		t.Fatalf("ignore .env in feat/one: %v", err)
	}
	if out, err := exec.Command("git", "-C", one, "add", ".gitignore").CombinedOutput(); err != nil {
		t.Fatalf("add the .gitignore: %v: %s", err, out)
	}
	if out, err := exec.Command("git", "-C", one, "commit", "-q", "-m", "ignore .env").CombinedOutput(); err != nil {
		t.Fatalf("commit the .gitignore: %v: %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(one, ".env"), []byte("SECRET=1\n"), 0o600); err != nil {
		t.Fatalf("write the ignored file: %v", err)
	}

	// A clean worktree: the delete row removes the directory, keeps the
	// branch, kills the session and says so.
	deleteRow("feat/one", "ignored file deleted unsaved")
	if err := term.WaitForText("Deleted worktree", uiTimeout); err != nil {
		t.Fatalf("no notification said the worktree was deleted: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "close-worktree-deleted")
	if _, err := os.Stat(filepath.Join(one, ".env")); !os.IsNotExist(err) {
		t.Errorf("the ignored .env survived the delete row: %v", err)
	}
	if out, err := exec.Command("git", "-C", repo, "stash", "list").CombinedOutput(); err != nil {
		t.Errorf("read the stash: %v: %s", err, out)
	} else if strings.Contains(string(out), "feat/one") {
		t.Errorf("a worktree holding only ignored files landed in git stash anyway: %s", out)
	}
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
	deleteRow("feat/two", "already gone")
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
	deleteRow("feat/three", "goes to git stash")
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

// TestSidebarCloseRefusesDetachedWorktree covers the one thing the delete
// row must not do: remove a worktree whose detached HEAD holds commits no
// branch has, which the removal would leave unreachable. The dialog refuses
// and stays open, the CLI verb refuses with the error, and nothing is
// removed. Force is the way out, and the test does not take it.
func TestSidebarCloseRefusesDetachedWorktree(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	repo := testutil.GitRepo(t)
	t.Setenv("TUIOS_WORKTREE_DIR", filepath.Join(base, "worktrees"))

	if out, err := tuiosCLIIn(t, base, t.TempDir(), "new", "plain", "--detach"); err != nil {
		t.Fatalf("create the plain session: %v: %s", err, out)
	}
	if out, err := tuiosCLI(t, base, "worktree", "new", "feat/detach", "--repo", repo, "--detach"); err != nil {
		t.Fatalf("worktree new feat/detach: %v: %s", err, out)
	}
	wt := filepath.Join(base, "worktrees", "repo", "feat-detach")
	if out, err := exec.Command("git", "-C", wt, "checkout", "-q", "--detach").CombinedOutput(); err != nil {
		t.Fatalf("detach the worktree's HEAD: %v: %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(wt, "own.txt"), []byte("on the detached HEAD\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "own.txt"}, {"commit", "-q", "-m", "own"}} {
		if out, err := exec.Command("git", append([]string{"-C", wt}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("commit on the detached HEAD: git %v: %v: %s", args, err, out)
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
	waitForAll(t, term, uiTimeout, "sidebar with the detached worktree row", "feat/detach")

	col, row := findOnScreen(t, term, "feat/detach")
	mouseClick(t, term, col, row, tuitest.MouseRight, 0)
	if err := term.WaitForText("Kill session", uiTimeout); err != nil {
		t.Fatalf("the row's menu offered no kill: %v\n%s", err, term.Snapshot())
	}
	col, row = findOnScreen(t, term, "Kill session")
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
	if err := term.WaitForText("Delete worktree", uiTimeout); err != nil {
		t.Fatalf("the close dialog offered no worktree delete: %v\n%s", err, term.Snapshot())
	}
	// The dialog names the refusal before the row is taken.
	if err := term.WaitForText("detached HEAD", uiTimeout); err != nil {
		t.Fatalf("the close dialog said nothing about the detached HEAD: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "close-worktree-detached-dialog")
	if err := term.SendKeys(tuitest.Down, tuitest.Down, tuitest.Enter); err != nil {
		t.Fatalf("take the delete row: %v", err)
	}
	// Refused: the dialog stays open, the directory and the session are
	// still there.
	if err := term.WaitForText("Delete worktree", uiTimeout); err != nil {
		t.Fatalf("the refusal dismissed the dialog anyway: %v\n%s", err, term.Snapshot())
	}
	if _, err := os.Stat(wt); err != nil {
		t.Errorf("the refusal removed the worktree directory anyway: %v", err)
	}
	if hasName(sessionNames(t, base), "repo-feat-detach") == false {
		t.Errorf("the refusal killed the session anyway")
	}
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("dismiss the dialog: %v", err)
	}

	// The CLI verb refuses the same worktree, because the dialog and the
	// command share the daemon's answer.
	if out, err := tuiosCLI(t, base, "worktree", "rm", "repo-feat-detach"); err == nil {
		t.Fatalf("the CLI removed a detached worktree with own commits: %s", out)
	} else if !strings.Contains(out, "detached") {
		t.Errorf("the CLI refusal said nothing about the detached HEAD: %v: %s", err, out)
	}
	alive(t, term, "after the refusals")
}

// TestSidebarKillQuitConfirmsOnWorktree gates the sidebar's instant
// kill-and-quit on the session being a worktree. Taking a worktree session
// down takes a directory with it, so the row must raise the same close
// confirmation the dock control raises; on a plain session it must stay
// instant, which is the behaviour everything else about the row assumes.
func TestSidebarKillQuitConfirmsOnWorktree(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	repo := testutil.GitRepo(t)
	t.Setenv("TUIOS_WORKTREE_DIR", filepath.Join(base, "worktrees"))

	if out, err := tuiosCLI(t, base, "worktree", "new", "feat/one", "--repo", repo, "--detach"); err != nil {
		t.Fatalf("worktree new feat/one: %v: %s", err, out)
	}
	if out, err := tuiosCLIIn(t, base, t.TempDir(), "new", "plain", "--detach"); err != nil {
		t.Fatalf("create the plain session: %v: %s", err, out)
	}

	// Attached to the worktree session itself: the row whose kill-and-quit
	// must learn to ask first.
	term := startIn(t, base, startOpts{args: []string{"attach", "repo-feat-one"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == 1
	}, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	toggleSidebarViaPalette(t, term)
	waitForAll(t, term, uiTimeout, "sidebar with the worktree row", sidebarHeader, "feat/one")

	col, row := findOnScreen(t, term, "feat/one")
	mouseClick(t, term, col, row, tuitest.MouseRight, 0)
	if err := term.WaitForText("Kill session and quit", uiTimeout); err != nil {
		t.Fatalf("the attached row's menu offered no kill-and-quit: %v\n%s", err, term.Snapshot())
	}
	col, row = findOnScreen(t, term, "Kill session and quit")
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)

	// The confirmation names the session it is about to take down, and the
	// client is still alive behind it.
	if err := term.WaitForText("Close repo-feat-one?", uiTimeout); err != nil {
		t.Fatalf("kill-and-quit on a worktree session did not confirm: %v\n%s", err, term.Snapshot())
	}
	alive(t, term, "with the confirmation up")
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("cancel the confirmation: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "Close repo-feat-one?")
	}, uiTimeout); err != nil {
		t.Fatalf("esc never dismissed the confirmation: %v\n%s", err, term.Snapshot())
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return hasName(sessionNames(t, base), "repo-feat-one")
	}, uiTimeout); err != nil {
		t.Fatalf("cancelling the confirmation killed the session: %v", err)
	}
	alive(t, term, "after cancelling the confirmation")

	// A plain session's kill-and-quit stays instant: no dialog, the client
	// quits, and the session is gone with it.
	second := startIn(t, base, startOpts{args: []string{"attach", "plain"}})
	if err := second.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == 1
	}, bootTimeout); err != nil {
		t.Fatalf("second client never attached: %v\n%s", err, second.Snapshot())
	}
	windowManagementMode(t, second)
	toggleSidebarViaPalette(t, second)
	waitForAll(t, second, uiTimeout, "sidebar with the plain row", sidebarHeader, "plain")

	col, row = findOnScreen(t, second, "plain")
	mouseClick(t, second, col, row, tuitest.MouseRight, 0)
	if err := second.WaitForText("Kill session and quit", uiTimeout); err != nil {
		t.Fatalf("the plain row's menu offered no kill-and-quit: %v\n%s", err, second.Snapshot())
	}
	col, row = findOnScreen(t, second, "Kill session and quit")
	mousePress(t, second, col, row, tuitest.MouseLeft, 0)
	// The press is the answer: a plain session's row quits the client on it,
	// so the release the gesture still owes goes nowhere once the process is
	// gone. Sent best-effort, exactly as a physical button lifts.
	_ = second.SendMouse(tuitest.MouseEvent{
		Col: col, Row: row, Button: tuitest.MouseLeft, Action: tuitest.MouseRelease,
	})

	// Instant means instant: the client is gone before anything could be
	// confirmed, and a confirmation would have kept it alive. WaitExit is the
	// one way to wait for the process itself; ExitCode inside a WaitFor on the
	// same terminal deadlocks on the terminal's own mutex.
	if _, err := second.WaitExit(uiTimeout); err != nil {
		t.Fatalf("kill-and-quit on a plain session did not quit instantly: %v\n%s", err, second.Snapshot())
	}
	if err := second.WaitFor(func(s tuitest.Screen) bool {
		names := sessionNames(t, base)
		return !hasName(names, "plain") && hasName(names, "repo-feat-one")
	}, uiTimeout); err != nil {
		t.Fatalf("the plain session survived its kill-and-quit, or took the worktree session with it: %v", err)
	}
}

// TestQuitMenuDeletesWorktree drives the quit menu's delete row. In a daemon
// session the quit key opens the menu rather than quitting, and a worktree
// session's menu must offer to take the worktree with it; taking the row must
// open the close confirmation — one confirmation, one place that shows the
// toll — whose delete row deletes and quits this client once the daemon
// confirms. A plain session's menu must offer no such row.
func TestQuitMenuDeletesWorktree(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	repo := testutil.GitRepo(t)
	t.Setenv("TUIOS_WORKTREE_DIR", filepath.Join(base, "worktrees"))

	if out, err := tuiosCLIIn(t, base, t.TempDir(), "new", "plain", "--detach"); err != nil {
		t.Fatalf("create the plain session: %v: %s", err, out)
	}
	if out, err := tuiosCLI(t, base, "worktree", "new", "feat/one", "--repo", repo, "--detach"); err != nil {
		t.Fatalf("worktree new feat/one: %v: %s", err, out)
	}

	term := startIn(t, base, startOpts{args: []string{"attach", "repo-feat-one"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == 1
	}, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(insertGuard + 150*time.Millisecond)

	// leader q opens the quit menu; the worktree session's menu carries the
	// delete row after the kill-and-quit row.
	if err := term.SendKeys(tuitest.Ctrl('b'), "q"); err != nil {
		t.Fatalf("send leader q: %v", err)
	}
	if err := term.WaitForText("Kill session and quit", uiTimeout); err != nil {
		t.Fatalf("quit menu never appeared: %v\n%s", err, term.Snapshot())
	}
	if err := term.WaitForText("Delete worktree and quit", uiTimeout); err != nil {
		t.Fatalf("the worktree session's quit menu offered no worktree delete: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "quit-menu-delete-row")

	// The menu opens on Detach, so four downs reach the fifth row.
	for i := 0; i < 4; i++ {
		if err := term.SendKeys(tuitest.Down); err != nil {
			t.Fatalf("move down to the delete row: %v", err)
		}
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("take the delete row: %v", err)
	}
	// The row opens the close confirmation — one confirmation, one place
	// that shows the toll — whose delete row does the deleting. The dialog
	// opens on Cancel, so two downs reach the delete row.
	if err := term.WaitForText("Delete worktree too", uiTimeout); err != nil {
		t.Fatalf("taking the quit menu's delete row opened no confirmation: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Down, tuitest.Down, tuitest.Enter); err != nil {
		t.Fatalf("take the dialog's delete row: %v", err)
	}

	// The row deletes the worktree, shows the note for the notification
	// duration — readable, which is the whole point of the client-side kill —
	// and then quits. The exit is the client's own deliberate quit: exit code
	// 0, never the "session killed" report a daemon-side kill would produce.
	if err := term.WaitForText("Deleted worktree", uiTimeout); err != nil {
		t.Fatalf("no notification said the worktree was deleted: %v\n%s", err, term.Snapshot())
	}
	code, err := term.WaitExit(uiTimeout)
	if err != nil {
		t.Fatalf("the delete row did not quit the client: %v\n%s", err, term.Snapshot())
	}
	if code != 0 {
		t.Errorf("the quit-menu delete exited %d, want the clean exit of a deliberate quit", code)
	}

	if _, err := os.Stat(filepath.Join(base, "worktrees", "repo", "feat-one")); !os.IsNotExist(err) {
		t.Errorf("the feat/one worktree directory survived the quit-menu delete row: %v", err)
	}
	if out, err := exec.Command("git", "-C", repo, "show-ref", "--verify", "refs/heads/feat/one").CombinedOutput(); err != nil {
		t.Errorf("the feat/one branch did not survive the quit-menu delete row: %v: %s", err, out)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !hasName(sessionNames(t, base), "repo-feat-one")
	}, uiTimeout); err != nil {
		t.Errorf("the repo-feat-one session survived the quit-menu delete row: %v", err)
	}

	// A plain session's quit menu must offer no worktree delete.
	second := startIn(t, base, startOpts{args: []string{"attach", "plain"}})
	if err := second.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == 1
	}, bootTimeout); err != nil {
		t.Fatalf("second client never attached: %v\n%s", err, second.Snapshot())
	}
	time.Sleep(insertGuard + 150*time.Millisecond)
	if err := second.SendKeys(tuitest.Ctrl('b'), "q"); err != nil {
		t.Fatalf("send leader q on the plain session: %v", err)
	}
	if err := second.WaitForText("Kill and quit", uiTimeout); err != nil {
		t.Fatalf("the plain session's quit menu never appeared: %v\n%s", err, second.Snapshot())
	}
	if text := second.Screen().Text(); strings.Contains(text, "Delete worktree") {
		t.Fatalf("the plain session's quit menu offered a worktree delete\n%s", text)
	}
	if err := second.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("cancel the plain session's quit menu: %v", err)
	}
	alive(t, second, "after cancelling the plain session's quit menu")
}

// TestQuitMenuDeleteFollowsASwitchDuringTheDwell pins who the quit fires at:
// the deletion leaves the session alive for the notification's duration, and
// the user has that whole window to switch to another session. The quit must
// follow the deleted session by name — the one this client sits in when the
// dwell runs out must survive it.
func TestQuitMenuDeleteFollowsASwitchDuringTheDwell(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	repo := testutil.GitRepo(t)
	t.Setenv("TUIOS_WORKTREE_DIR", filepath.Join(base, "worktrees"))

	if out, err := tuiosCLIIn(t, base, t.TempDir(), "new", "plain", "--detach"); err != nil {
		t.Fatalf("create the plain session: %v: %s", err, out)
	}
	if out, err := tuiosCLI(t, base, "worktree", "new", "feat/one", "--repo", repo, "--detach"); err != nil {
		t.Fatalf("worktree new feat/one: %v: %s", err, out)
	}

	term := startIn(t, base, startOpts{args: []string{"attach", "repo-feat-one"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == 1
	}, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	toggleSidebarViaPalette(t, term)

	// Take the quit menu's delete row: leader q, then four downs to the fifth
	// row, then enter. It opens the close confirmation, whose delete row —
	// two downs down — deletes, shows the note and starts the dwell.
	if err := term.SendKeys(tuitest.Ctrl('b'), "q"); err != nil {
		t.Fatalf("send leader q: %v", err)
	}
	if err := term.WaitForText("Delete worktree and quit", uiTimeout); err != nil {
		t.Fatalf("the worktree session's quit menu offered no worktree delete: %v\n%s", err, term.Snapshot())
	}
	for i := 0; i < 4; i++ {
		if err := term.SendKeys(tuitest.Down); err != nil {
			t.Fatalf("move down to the delete row: %v", err)
		}
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("take the delete row: %v", err)
	}
	if err := term.WaitForText("Delete worktree too", uiTimeout); err != nil {
		t.Fatalf("taking the quit menu's delete row opened no confirmation: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Down, tuitest.Down, tuitest.Enter); err != nil {
		t.Fatalf("take the dialog's delete row: %v", err)
	}
	if err := term.WaitForText("Deleted worktree", uiTimeout); err != nil {
		t.Fatalf("no notification said the worktree was deleted: %v\n%s", err, term.Snapshot())
	}

	// The dwell is interactive time: switch to the plain session while the
	// deleted one is still running.
	col, row := findOnScreen(t, term, "plain")
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		names := sessionNames(t, base)
		return hasName(names, "plain") && !hasName(names, "repo-feat-one")
	}, uiTimeout+6*time.Second); err != nil {
		t.Fatalf("the dwell's quit did not follow the deleted session: %v\n%s", err, term.Snapshot())
	}

	// The client lives on in the session it switched to — alive is the
	// assertion the pre-fix build fails, because its quit killed plain and
	// took this client with it.
	alive(t, term, "after the dwell's quit fired in another session")

	if _, err := os.Stat(filepath.Join(base, "worktrees", "repo", "feat-one")); !os.IsNotExist(err) {
		t.Errorf("the feat/one worktree directory survived the delete row: %v", err)
	}
	if out, err := exec.Command("git", "-C", repo, "show-ref", "--verify", "refs/heads/feat/one").CombinedOutput(); err != nil {
		t.Errorf("the feat/one branch did not survive the delete row: %v: %s", err, out)
	}
}
