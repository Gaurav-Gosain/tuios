package tuie2e

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
	"github.com/Gaurav-Gosain/tuitest"
)

// workspaceNameIn is the name the daemon holds for workspace ws of session
// sess, read through list-workspaces so the test sees daemon state and not the
// client's copy.
func workspaceNameIn(t *testing.T, base, sess string, ws int) string {
	t.Helper()
	out, err := tuiosCLI(t, base, "list-workspaces", "--json", "--session", sess)
	if err != nil {
		t.Fatalf("list-workspaces: %v\n%s", err, out)
	}
	var res struct {
		Workspaces []struct {
			Workspace int    `json:"workspace"`
			Name      string `json:"name"`
		} `json:"workspaces"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("list-workspaces json: %v\n%s", err, out)
	}
	for _, w := range res.Workspaces {
		if w.Workspace == ws {
			return w.Name
		}
	}
	return ""
}

// TestSwitchSessionByNumberAndRenameWorkspace binds switch_session_3,
// switch_session_9 and rename_workspace in config.toml and drives them in a
// client with three sessions. The third rail position is charlie, so the
// session key lands there from terminal mode. Slot 9 is empty, so it says so
// and stays put. The rename goes through the daemon, so the name is read back
// with list-workspaces.
//
// Negative control: build origin/main before #460. The three actions do not
// exist there, and the test fails at the wait for "Session: charlie".
func TestSwitchSessionByNumberAndRenameWorkspace(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, `
[keybindings.window_management]
switch_session_3 = ["alt+shift+c"]
switch_session_9 = ["alt+shift+z"]
rename_workspace = ["f6"]
`)
	killDaemon(t, base)
	for _, name := range []string{"alpha", "bravo", "charlie"} {
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("create %s: %v: %s", name, err, out)
		}
	}

	term := startIn(t, base, startOpts{cols: 140, rows: 40, args: []string{"attach", "alpha"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == 1
	}, bootTimeout); err != nil {
		t.Fatalf("client never attached to alpha: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	enterTerminalMode(t, term)

	// switch_session_N is terminal-safe, like next_session, so the chord works
	// while a shell has the keys.
	if err := term.SendKeys(tuitest.Alt('C')); err != nil {
		t.Fatalf("send switch_session_3: %v", err)
	}
	if err := term.WaitForText("Session: charlie", uiTimeout); err != nil {
		t.Fatalf("switch_session_3 never landed on charlie: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "switch-session-3")
	time.Sleep(insertGuard + 150*time.Millisecond)

	if err := term.SendKeys(tuitest.Alt('Z')); err != nil {
		t.Fatalf("send switch_session_9: %v", err)
	}
	if err := term.WaitForText("No session 9", uiTimeout); err != nil {
		t.Fatalf("switch_session_9 with three sessions did not say so: %v\n%s", err, term.Snapshot())
	}

	windowManagementMode(t, term)
	if err := term.SendKeys("\x1b[17~"); err != nil { // F6
		t.Fatalf("send rename_workspace: %v", err)
	}
	if err := term.WaitForText("rename workspace 1", uiTimeout); err != nil {
		t.Fatalf("rename_workspace never opened the editor: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("zebra"); err != nil {
		t.Fatalf("type the name: %v", err)
	}
	if err := term.WaitForText("› zebra", uiTimeout); err != nil {
		t.Fatalf("the editor never showed the name: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("commit the rename: %v", err)
	}

	deadline := time.Now().Add(uiTimeout)
	for workspaceNameIn(t, base, "charlie", 1) != "zebra" {
		if time.Now().After(deadline) {
			t.Fatalf("the daemon never held the name zebra for charlie's workspace 1\n%s", term.Snapshot())
		}
		time.Sleep(100 * time.Millisecond)
	}
	if got := workspaceNameIn(t, base, "alpha", 1); got != "" {
		t.Fatalf("alpha's workspace 1 is %q, want no name: the rename went to the wrong session", got)
	}
	saveFrame(t, term, "rename-workspace")
}

// TestSessionKeysFollowRail is #1299: next_session and switch_session_N walk
// the sessions in the order the rail draws them, and the number beside a row
// is the key that opens it. A space pulls its sessions above the rest, and a
// repository pulls its worktree sessions together, so the rail's order differs
// from the order the sessions were made.
//
// The controls are in NEGATIVE_CONTROLS.md.
func TestSessionKeysFollowRail(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, `
[appearance.sidebar]
enabled = true
width = 28
show_numbers = true

[keybindings.window_management]
switch_session_1 = ["alt+shift+a"]
switch_session_3 = ["alt+shift+c"]
switch_session_5 = ["alt+shift+e"]
`)
	killDaemon(t, base)
	repo := testutil.GitRepo(t)
	t.Setenv("TUIOS_WORKTREE_DIR", filepath.Join(base, "worktrees"))

	// Each plain session starts outside any repository: this suite can run
	// inside a git worktree of tuios, and a session made there would group too.
	plain := t.TempDir()
	for _, args := range [][]string{
		{"new", "loose", "--detach"},
		{"new", "filed", "--detach", "--space", "work"},
		{"worktree", "new", "feat/one", "--repo", repo, "--detach"},
		{"new", "notes", "--detach"},
		{"worktree", "new", "feat/two", "--repo", repo, "--detach"},
	} {
		if out, err := tuiosCLIIn(t, base, plain, args...); err != nil {
			t.Fatalf("tuios %v: %v: %s", args, err, out)
		}
	}

	term := startIn(t, base, startOpts{cols: 140, rows: 40, args: []string{"attach", "loose"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == 1
	}, bootTimeout); err != nil {
		t.Fatalf("client never attached to loose: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	enterTerminalMode(t, term)

	// The rail draws the space first, then the rest with the repository's
	// sessions together. The number on each row counts down that order.
	rows := []string{"1 filed", "2 loose", "3 ", "4 ", "5 notes"}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return railRowsIncreasing(spaceRailRows(s, rows...)) &&
			railRowOf(s, "feat/one") == railRowOf(s, "3 ") &&
			railRowOf(s, "feat/two") == railRowOf(s, "4 ")
	}, bootTimeout); err != nil {
		t.Fatalf("the rail's numbers do not count its rows: %v\n%s", err, term.Snapshot())
	}
	railShot(t, term, "session-keys-rail")

	press := func(key tuitest.Key, what, want string) {
		t.Helper()
		if err := term.SendKeys(key); err != nil {
			t.Fatalf("send %s: %v", what, err)
		}
		clientShows(t, base, want)
		if err := term.WaitForText("Session: "+want, uiTimeout); err != nil {
			t.Fatalf("%s never landed on %s: %v\n%s", what, want, err, term.Snapshot())
		}
		time.Sleep(insertGuard + 150*time.Millisecond)
	}
	press(tuitest.Alt('A'), "switch_session_1", "filed")
	press(tuitest.Alt('N'), "next_session from filed", "loose")
	press(tuitest.Alt('C'), "switch_session_3", "repo-feat-one")
	press(tuitest.Alt('N'), "next_session from repo-feat-one", "repo-feat-two")
	press(tuitest.Alt('E'), "switch_session_5", "notes")
	railShot(t, term, "session-keys-notes")
}
