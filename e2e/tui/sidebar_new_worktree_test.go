package tuie2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
	"github.com/Gaurav-Gosain/tuitest"
)

// TestRailRightClickMakesAWorktree drives the dialog the way a person meets
// it: a throwaway repository, a worktree session the CLI made so the rail has
// a repository row, a right-click on that row, the branch and the session name
// typed into the dialog, and a daemon whose ls is the authority on what came
// out of it. The client should end up attached to the session it named, with
// the configured startup layout applied even though the daemon opened its
// first pane before the client switched to it. Floating stays opt-in too.
func TestRailRightClickMakesAWorktree(t *testing.T) {
	for _, tc := range []struct {
		name   string
		layout string
		tiled  bool
	}{
		{name: "bsp", layout: "bsp", tiled: true},
		{name: "master-stack", layout: "master-stack", tiled: true},
		{name: "scrolling", layout: "scrolling", tiled: true},
		{name: "floating", layout: "bsp", tiled: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			railRightClickMakesAWorktree(t, tc.layout, tc.tiled)
		})
	}
}

func railRightClickMakesAWorktree(t *testing.T, startupLayout string, tiled bool) {
	t.Helper()
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, fmt.Sprintf("[startup]\ntiled = %t\nlayout = %q\n", tiled, startupLayout))
	repo := testutil.GitRepo(t)
	t.Setenv("TUIOS_WORKTREE_DIR", filepath.Join(base, "worktrees"))

	// A session that is not a worktree, which is what this client attaches to,
	// and one CLI-made worktree so the rail has the repository row to click.
	// The plain one starts the daemon, so it is created from a directory in no
	// repository.
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
	waitForAll(t, term, uiTimeout, "sidebar with the repository row", sidebarHeader, "repo")

	// Right-click the repository's group header and take its worktree row.
	col, row := findOnScreen(t, term, "repo")
	mouseClick(t, term, col, row, tuitest.MouseRight, 0)
	if err := term.WaitForText("New worktree...", uiTimeout); err != nil {
		t.Fatalf("the repository row's menu offered no worktree: %v\n%s", err, term.Snapshot())
	}
	col, row = findOnScreen(t, term, "New worktree...")
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
	if err := term.WaitForText("new worktree", uiTimeout); err != nil {
		t.Fatalf("the menu row did not open the dialog: %v\n%s", err, term.Snapshot())
	}

	// Type the branch. The session field's empty state reads as the name the
	// daemon will derive, which is the assertion that the derivation the rail
	// shows is the derivation the daemon does.
	if err := term.SendKeys("feat/ui"); err != nil {
		t.Fatalf("type the branch: %v", err)
	}
	if err := term.WaitForText("repo-feat-ui", uiTimeout); err != nil {
		t.Fatalf("the dialog did not derive the session name from the branch: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "rail-worktree-dialog")

	// Tab to the session name and type one, so the create is by the name the
	// user chose and not the derived one.
	if err := term.SendKeys("\t"); err != nil {
		t.Fatalf("tab to the session name: %v", err)
	}
	if err := term.SendKeys("custom"); err != nil {
		t.Fatalf("type the session name: %v", err)
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("submit the dialog: %v", err)
	}

	// The daemon is the authority on what was made: the session by the name
	// the user typed, the worktree on disk, and no session for the derived
	// name that stopped being the answer the moment a name was typed.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return hasName(sessionNames(t, base), "custom")
	}, uiTimeout); err != nil {
		t.Fatalf("tuios never listed the created session: %v\n%s", err, term.Snapshot())
	}
	names := sessionNames(t, base)
	if !hasName(names, "repo-feat-one") {
		t.Errorf("tuios ls lists %v, want the CLI-made worktree session untouched", names)
	}
	if hasName(names, "repo-feat-ui") {
		t.Errorf("tuios ls lists %v, want the typed name in place of the derived one", names)
	}
	if out, err := exec.Command("git", "-C", filepath.Join(base, "worktrees", "repo", "feat-ui"), "branch", "--show-current").CombinedOutput(); err != nil || strings.TrimSpace(string(out)) != "feat/ui" {
		t.Errorf("the worktree on disk is not on feat/ui: %v: %s", err, out)
	}

	// The new session joins the repository's group on the rail once the
	// client's listing carries it, and the client is attached to it: the rail
	// is the screen the user is looking at, and the gutter mark on the row is
	// the screen's own word for "this is the session you are in".
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		r := railLineWith(s, "feat/ui")
		return r >= 0 && strings.Contains(s.Line(r), "▎")
	}, uiTimeout); err != nil {
		t.Fatalf("the rail never showed the created worktree as the attached session: %v\n%s", err, term.Snapshot())
	}

	wantTiling := "floating"
	if tiled {
		wantTiling = "tiling"
	}
	waitTilingMode(t, base, "custom", wantTiling)
	if tiled {
		waitLayoutMode(t, base, "custom", startupLayout)
	}
	rects := waitForSettledGeometryIn(t, base, "custom", 1)
	info := sessionInfoOf(t, base, "custom")
	if tiled && rects[0].Height < info.Height*3/4 {
		t.Fatalf("the worktree's first pane is still floating in %s: %+v in a %dx%d session",
			startupLayout, rects[0], info.Width, info.Height)
	}
	t.Logf("worktree session: %s %s, first pane: %+v\n%s",
		info.TilingMode, info.LayoutMode, rects[0], term.Snapshot())
	saveFrame(t, term, "rail-worktree-created-"+wantTiling+"-"+startupLayout)

	// A later daemon-created pane must join the layout without a manual
	// tiling toggle; checking the session flag alone would miss stale boxes.
	if tiled {
		if out, err := tuiosCLI(t, base, "new-window", "-s", "custom"); err != nil {
			t.Fatalf("open another worktree pane: %v: %s", err, out)
		}
		rects = waitForSettledGeometryIn(t, base, "custom", 2)
		if len(rects) != 2 || geomOverlap(rects[0], rects[1]) {
			t.Fatalf("the worktree's panes did not tile in %s: %+v", startupLayout, rects)
		}
		t.Logf("worktree panes after new-window (%s): %+v", startupLayout, rects)
		saveFrame(t, term, "rail-worktree-two-panes-"+startupLayout)
	}

	alive(t, term, "after making a worktree from the rail")
}

// TestRailWorktreeRefusals opens the dialog from a worktree session's row, the
// other way in, and drives the three ways it ends without a new session: an
// empty branch the dialog refuses itself, a branch the daemon refuses, whose
// notification has to say which step failed, and esc. The daemon's ls is the
// authority that nothing was made.
func TestRailWorktreeRefusals(t *testing.T) {
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
	before := sessionNames(t, base)

	term := startIn(t, base, startOpts{args: []string{"attach", "plain"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == 1
	}, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	toggleSidebarViaPalette(t, term)
	waitForAll(t, term, uiTimeout, "sidebar with the worktree session row", sidebarHeader, "feat/one")

	// openFromSessionRow right-clicks the worktree session's row, labelled by
	// its branch, and takes its worktree row. The dialog names the session's
	// repository, which is what the row's carry resolved to.
	openFromSessionRow := func() {
		t.Helper()
		col, row := findOnScreen(t, term, "feat/one")
		mouseClick(t, term, col, row, tuitest.MouseRight, 0)
		if err := term.WaitForText("New worktree...", uiTimeout); err != nil {
			t.Fatalf("the session row's menu offered no worktree: %v\n%s", err, term.Snapshot())
		}
		col, row = findOnScreen(t, term, "New worktree...")
		mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
		if err := term.WaitForText("Worktree of repo", uiTimeout); err != nil {
			t.Fatalf("the menu row did not open the dialog on the session's repository: %v\n%s", err, term.Snapshot())
		}
	}

	// Enter on an empty branch is the dialog's own refusal: it stays open and
	// says what is missing, and the daemon is never asked.
	openFromSessionRow()
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("submit the empty dialog: %v", err)
	}
	if err := term.WaitForText("Type a branch name.", uiTimeout); err != nil {
		t.Fatalf("the dialog did not refuse an empty branch: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "rail-worktree-empty-branch")

	// A branch already checked out in the CLI-made worktree is the daemon's
	// refusal. The dialog has closed by then, so the notification is the only
	// place it shows, and it has to say the create is the step that failed.
	if err := term.SendKeys("feat/one"); err != nil {
		t.Fatalf("type the taken branch: %v", err)
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("submit the taken branch: %v", err)
	}
	if err := term.WaitForText("failed to create the worktree", uiTimeout); err != nil {
		t.Fatalf("the daemon's refusal did not say which step failed: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "rail-worktree-daemon-refusal")

	// Esc closes the dialog without asking the daemon anything.
	openFromSessionRow()
	if err := term.SendKeys("feat/esc"); err != nil {
		t.Fatalf("type a branch: %v", err)
	}
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("cancel the dialog: %v", err)
	}
	waitGone(t, term, "the worktree dialog", "Worktree of repo")

	if after := sessionNames(t, base); len(after) != len(before) {
		t.Errorf("tuios ls lists %v after three refusals, want %v", after, before)
	}
	if _, err := os.Stat(filepath.Join(base, "worktrees", "repo", "feat-esc")); !os.IsNotExist(err) {
		t.Errorf("a cancelled dialog left a worktree on disk: %v", err)
	}

	alive(t, term, "after the worktree dialog's refusals")
}
