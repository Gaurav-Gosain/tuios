package tuie2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// TestRailWorktreeRefusals opens the dialog from a worktree session's
// row, the other way in, and drives the three ways it ends without a new
// session: an empty branch the dialog refuses itself, a branch the daemon
// refuses, whose notification has to say which step failed, and esc. The
// daemon's ls is the authority that nothing was made.
//
// Then the third way in, the new_worktree key (leader U): refused from a pane
// in no repository, and from a pane in the repository it makes the session,
// groups it under the repository on the rail, and attaches to it.
//
// Every create here runs the after-new-worktree hook. It writes a file into
// the tree after a pause, and a first window that looks for the file must find
// it, which holds only when the daemon waits for the hook. A branch the hook
// fails on still gets its session, and the rail says why the hook failed.
func TestRailWorktreeRefusals(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	repo := testutil.GitRepo(t)
	t.Setenv("TUIOS_WORKTREE_DIR", filepath.Join(base, "worktrees"))
	hook := filepath.Join(base, "worktree-hook.sh")
	hookBody := `#!/bin/sh
case "$TUIOS_WORKTREE_BRANCH" in
feat/broken) echo "copying .env" >&2; echo "cannot copy .env: no such file" >&2; exit 3 ;;
esac
sleep 1
printf '%s|%s|%s|%s|%s|%s\n' "$TUIOS_EVENT" "$TUIOS_SESSION_ID" "$TUIOS_WORKTREE_BRANCH" \
	"$TUIOS_WORKTREE_CREATED_BRANCH" "$TUIOS_WORKTREE_REPO_ROOT" "$TUIOS_WORKTREE_PATH" > .tuios-hook
`
	if err := os.WriteFile(hook, []byte(hookBody), 0o755); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, base, "[hooks]\nafter-new-worktree = \""+hook+"\"\n")
	// wantHook reads what the hook wrote into one worktree.
	wantHook := func(session, branch, created string) {
		t.Helper()
		dir := filepath.Join(base, "worktrees", "repo", strings.ReplaceAll(branch, "/", "-"))
		got, err := os.ReadFile(filepath.Join(dir, ".tuios-hook"))
		if err != nil {
			t.Fatalf("the hook wrote nothing into the %s worktree: %v", branch, err)
		}
		want := strings.Join([]string{"after-new-worktree", session, branch, created, repo, dir}, "|")
		if strings.TrimSpace(string(got)) != want {
			t.Errorf("the hook for %s saw\n%s\nwant\n%s", branch, got, want)
		}
	}

	if out, err := tuiosCLIIn(t, base, t.TempDir(), "new", "plain", "--detach"); err != nil {
		t.Fatalf("create the plain session: %v: %s", err, out)
	}
	if out, err := tuiosCLI(t, base, "worktree", "new", "feat/one", "--repo", repo, "--detach"); err != nil {
		t.Fatalf("worktree new feat/one: %v: %s", err, out)
	}
	wantHook("repo-feat-one", "feat/one", "1")

	// A branch that exists already, with a first window that reports whether
	// the hook's file was there when it started.
	if out, err := exec.Command("git", "-C", repo, "branch", "feat/seen").CombinedOutput(); err != nil {
		t.Fatalf("make the feat/seen branch: %v: %s", err, out)
	}
	if _, err := callVerb(t, base, "new-worktree", map[string]any{
		"repo": repo, "branch": "feat/seen",
		"command": []string{"sh", "-c", "if [ -e .tuios-hook ]; then echo HOOK-READY; else echo HOOK-MISSING; fi; exec sleep 600"},
	}); err != nil {
		t.Fatal(err)
	}
	wantHook("repo-feat-seen", "feat/seen", "0")
	var pane string
	for deadline := time.Now().Add(uiTimeout); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		pane, _ = tuiosCLI(t, base, "capture-pane", "-s", "repo-feat-seen")
		if strings.Contains(pane, "HOOK-") {
			break
		}
	}
	if !strings.Contains(pane, "HOOK-READY") {
		t.Fatalf("the first window started before the hook finished:\n%s", pane)
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

	// The key from the plain session's pane, which is in no repository: a
	// message, and no dialog.
	leaderKey(t, term, "U")
	if err := term.WaitForText("This pane is not in a git repository.", uiTimeout); err != nil {
		t.Fatalf("the key did not say the pane is in no repository: %v\n%s", err, term.Snapshot())
	}
	if strings.Contains(term.Screen().Text(), "Worktree of repo") {
		t.Fatalf("the key opened the dialog from a pane in no repository:\n%s", term.Snapshot())
	}
	saveFrame(t, term, "worktree-key-no-repo")

	// A pane in a subfolder of the repository: the key finds the repository
	// from any folder in it, not only from its top. The new pane takes the
	// focus, so the key acts on it.
	sub := filepath.Join(repo, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatalf("make a subfolder in the repository: %v", err)
	}
	if out, err := tuiosCLI(t, base, "new-window", "-s", "plain", "--cwd", sub); err != nil {
		t.Fatalf("open a pane in the repository: %v: %s", err, out)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 2 }, uiTimeout); err != nil {
		t.Fatalf("the pane in the repository never showed: %v\n%s", err, term.Snapshot())
	}
	leaderKey(t, term, "U")
	if err := term.WaitForText("Worktree of repo", uiTimeout); err != nil {
		t.Fatalf("the key did not open the dialog on the pane's repository: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("feat/key"); err != nil {
		t.Fatalf("type the branch: %v", err)
	}
	if err := term.WaitForText("repo-feat-key", uiTimeout); err != nil {
		t.Fatalf("the dialog did not derive the session name: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "worktree-key-dialog")
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("submit the dialog: %v", err)
	}

	// The daemon made the session, the client is attached to it, and the rail
	// shows it as the current row under the repository's group.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		got := attachedSessions(base)
		return len(got) == 1 && got[0] == "repo-feat-key"
	}, uiTimeout); err != nil {
		t.Fatalf("the client is not attached to repo-feat-key (ls: %v, attached: %v): %v\n%s",
			sessionNames(t, base), attachedSessions(base), err, term.Snapshot())
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		group, row := railLineWith(s, "· repo"), railLineWith(s, "feat/key")
		return group >= 0 && row > group && strings.Contains(s.Line(row), "─ feat/key") && strings.Contains(s.Line(row), "▎")
	}, uiTimeout); err != nil {
		t.Fatalf("the rail never showed feat/key as the current row in the repo group: %v\n%s", err, term.Snapshot())
	}
	if out, err := exec.Command("git", "-C", filepath.Join(base, "worktrees", "repo", "feat-key"), "branch", "--show-current").CombinedOutput(); err != nil || strings.TrimSpace(string(out)) != "feat/key" {
		t.Errorf("the worktree on disk is not on feat/key: %v: %s", err, out)
	}
	saveFrame(t, term, "worktree-key-created")
	wantHook("repo-feat-key", "feat/key", "1")

	// A hook that fails keeps the worktree and the session, and the rail
	// leads with the hook's exit and the end of its stderr.
	leaderKey(t, term, "U")
	if err := term.WaitForText("Worktree of repo", uiTimeout); err != nil {
		t.Fatalf("the key did not open the dialog: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("feat/broken"); err != nil {
		t.Fatalf("type the branch: %v", err)
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("submit the dialog: %v", err)
	}
	if err := term.WaitForText("The after-new-worktree hook exited with 3", uiTimeout); err != nil {
		t.Fatalf("the dock did not say the hook failed: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "worktree-hook-failed")
	// The dock cuts the message, and a click on it shows the whole of it,
	// which ends with the last line the hook wrote to stderr.
	col, row := findOnScreen(t, term, "The after-new-worktree hook")
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
	if err := term.WaitForText(".env: no such file", uiTimeout); err != nil {
		t.Fatalf("the message view did not show the hook's stderr: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "worktree-hook-failed-message")
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("close the message view: %v", err)
	}
	waitGone(t, term, "the message view", ".env: no such file")
	if !hasName(sessionNames(t, base), "repo-feat-broken") {
		t.Errorf("a failed hook lost the session: ls lists %v", sessionNames(t, base))
	}
	if _, err := os.Stat(filepath.Join(base, "worktrees", "repo", "feat-broken")); err != nil {
		t.Errorf("a failed hook lost the worktree: %v", err)
	}
	if out, err := tuiosCLI(t, base, "list-hooks", "--json"); err != nil || !strings.Contains(out, "after-new-worktree") || !strings.Contains(out, "cannot copy .env") {
		t.Errorf("list-hooks does not show the hook and its last error: %v\n%s", err, out)
	}

	alive(t, term, "after the worktree dialog's refusals and the worktree key")
}
