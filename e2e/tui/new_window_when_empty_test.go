package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// workspaces.new_window_when_empty (#477): a switch to a workspace with no
// panes opens one there, as the new-window key would.
//
// How these could pass wrongly, written down first:
//   - The pane could come from somewhere else: the startup, a second
//     client, or the test itself. The test counts the panes on the workspace
//     with list-windows, checks that startup opened none, and opens no pane
//     by key on any workspace it switches to.
//   - Two clients could each open one and the count could be read before the
//     second lands. Every count waits noPaneWait after the first pane before it
//     is read again.
//   - The directory could match by accident. The pane the switch comes from
//     is in "elsewhere", the session starts in "start", the daemon runs in
//     base/cwd, and inheriting from the focused pane is off, so each folder
//     has one way to be chosen.
//   - A switch that brings its own panes could pass because no pane opens
//     at all. The same fixture first shows a plain switch opening one.
//
// The list-windows output at the end is saved under artifactDir.

// noPaneWait is how long a test waits for a pane that must not come.
const noPaneWait = 1500 * time.Millisecond

// cwdOnWorkspace waits for a pane on workspace ws of session sess to report
// want as its directory, and returns the last directory a pane there
// reported.
func cwdOnWorkspace(t *testing.T, base, sess string, ws int, want string) string {
	t.Helper()
	var got string
	deadline := time.Now().Add(shellTimeout)
	for time.Now().Before(deadline) {
		out, err := tuiosCLI(t, base, "list-windows", "-s", sess, "--json")
		if err == nil {
			var res struct {
				Windows []struct {
					Cwd       string `json:"cwd"`
					Workspace int    `json:"workspace"`
				} `json:"windows"`
			}
			if json.Unmarshal([]byte(out), &res) == nil {
				for _, w := range res.Windows {
					if w.Workspace == ws && w.Cwd != "" {
						got = w.Cwd
					}
				}
				if got == want {
					return got
				}
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return got
}

// exactlyPanesOn waits for workspace ws to hold n panes, waits noPaneWait, and
// fails when the count moved.
func exactlyPanesOn(t *testing.T, term *tuitest.Terminal, base, sess string, ws, n int, what string) {
	t.Helper()
	waitPanesOn(t, term, base, sess, ws, n, what)
	time.Sleep(noPaneWait)
	if got := panesOn(t, base, sess, ws); got != n {
		t.Fatalf("ASSERTION: %s: workspace %d has %d panes, want exactly %d\n%s", what, ws, got, n, term.Snapshot())
	}
}

// saveWindowList writes list-windows --json for session sess to path.
func saveWindowList(t *testing.T, base, sess, path string) {
	t.Helper()
	out, err := tuiosCLI(t, base, "list-windows", "-s", sess, "--json")
	if err != nil {
		t.Logf("list windows for %s: %v", path, err)
		return
	}
	if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
		t.Logf("save %s: %v", path, err)
	}
}

func TestEmptyWorkspaceOpensAPane(t *testing.T) {
	const sess = "nw"
	base := t.TempDir()
	// move_and_follow_7 gets a plain key, because alt+shift+7 reaches the
	// client as whatever the keyboard layout puts on shift+7.
	writeConfig(t, base, "[workspaces]\nnew_window_when_empty = true\nreturn_when_empty = false\n"+
		"[appearance]\nnew_window_inherit_cwd = false\n"+
		"[keybindings.workspaces]\nmove_and_follow_7 = [\"alt+m\"]\n")
	start := projectDir(t, base, "start")
	elsewhere := projectDir(t, base, "elsewhere")

	term := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"new", sess, "--cwd", start}})
	waitBoot(t, term)
	// Startup is not a switch: the splash stays and no pane opens.
	time.Sleep(noPaneWait)
	if n := len(xpanesRowsIn(t, base, sess)); n != 0 {
		t.Fatalf("ASSERTION: startup opened %d panes, want none\n%s", n, term.Snapshot())
	}
	newWindow(t, term)
	waitWindowCount(t, term, 1, "setup")
	enterTerminalMode(t, term)
	runInShell(t, term, "cd '"+elsewhere+"' && echo HOME-$((6*7))", "HOME-42", shellTimeout)

	// 1. A switch by key opens one pane, in the directory of the pane the
	// switch came from.
	toWindowMode(t, term)
	sendKeys(t, term, tuitest.Alt("2"))
	waitShowing(t, base, sess, 2, "", term)
	exactlyPanesOn(t, term, base, sess, 2, 1, "the switch to workspace 2")
	if got := cwdOnWorkspace(t, base, sess, 2, elsewhere); got != elsewhere {
		t.Errorf("ASSERTION: the pane on workspace 2 is in %q, want %q, the folder of the pane the switch came from", got, elsewhere)
	}

	// 2. A switch from a workspace with no pane takes the session's start
	// directory. The pane on 2 exits, and return_when_empty is off, so the
	// session stays on 2 with nothing focused.
	enterTerminalMode(t, term)
	if err := term.SendKeys("exit", tuitest.Enter); err != nil {
		t.Fatalf("type exit: %v", err)
	}
	waitPanesOn(t, term, base, sess, 2, 0, "the shell on workspace 2 exits")
	// The splash on an empty workspace is window mode already.
	sendKeys(t, term, tuitest.Alt("5"))
	waitShowing(t, base, sess, 5, "", term)
	exactlyPanesOn(t, term, base, sess, 5, 1, "the switch to workspace 5 from an empty workspace")
	if got := cwdOnWorkspace(t, base, sess, 5, start); got != start {
		t.Errorf("ASSERTION: the pane on workspace 5 is in %q, want the session's start directory %q", got, start)
	}

	// 3. move_and_follow brings its own pane: workspace 7 holds that pane
	// and no other.
	var moved string
	for _, r := range xpanesRowsIn(t, base, sess) {
		if r.Workspace == 5 {
			moved = r.ID
		}
	}
	toWindowMode(t, term)
	sendKeys(t, term, tuitest.Alt("m"))
	waitShowing(t, base, sess, 7, "", term)
	exactlyPanesOn(t, term, base, sess, 7, 1, "move_and_follow to workspace 7")
	for _, r := range xpanesRowsIn(t, base, sess) {
		if r.Workspace == 7 && r.ID != moved {
			t.Errorf("ASSERTION: workspace 7 holds %s, not the pane that moved there (%s)", r.ID, moved)
		}
	}

	// 4. tuios xpanes brings its own panes: its workspace holds the two it
	// opened and no third.
	if out, err := tuiosCLI(t, base, "xpanes", "--session", sess, "--no-sync", "a", "b"); err != nil {
		t.Fatalf("xpanes: %v\n%s", err, out)
	}
	xws := 0
	deadline := time.Now().Add(shellTimeout)
	for xws == 0 {
		counts := map[int]int{}
		for _, r := range xpanesRowsIn(t, base, sess) {
			counts[r.Workspace]++
		}
		for ws, n := range counts {
			if ws != 1 && ws != 5 && ws != 7 && n >= 2 {
				xws = ws
			}
		}
		if xws == 0 && time.Now().After(deadline) {
			t.Fatalf("xpanes never opened its panes\n%s", term.Snapshot())
		}
		time.Sleep(100 * time.Millisecond)
	}
	waitShowing(t, base, sess, xws, "", term)
	exactlyPanesOn(t, term, base, sess, xws, 2, "tuios xpanes")

	// 5. A switch a script makes opens nothing: run-command, which the
	// client runs as a tape command, and select-workspace, which the
	// daemon makes.
	if out, err := tuiosCLI(t, base, "run-command", "SwitchWorkspace", "4"); err != nil {
		t.Fatalf("run-command SwitchWorkspace 4: %v\n%s", err, out)
	}
	waitShowing(t, base, sess, 4, "", term)
	exactlyPanesOn(t, term, base, sess, 4, 0, "run-command SwitchWorkspace")
	if out, err := tuiosCLI(t, base, "select-workspace", "--session", sess, "6"); err != nil {
		t.Fatalf("select-workspace 6: %v\n%s", err, out)
	}
	waitShowing(t, base, sess, 6, "", term)
	exactlyPanesOn(t, term, base, sess, 6, 0, "select-workspace")

	saveWindowList(t, base, sess, filepath.Join(artifactDir(t), "list-windows.json"))
	alive(t, term, "after the switches")
}

// TestEmptyWorkspaceOpensOnePaneForTwoClients: two clients show the session,
// one of them switches to an empty workspace, and the workspace gets one
// pane. The client that switched opens it. The other one follows the switch
// and opens nothing.
func TestEmptyWorkspaceOpensOnePaneForTwoClients(t *testing.T) {
	const sess = "nw2"
	base := t.TempDir()
	writeConfig(t, base, "[workspaces]\nnew_window_when_empty = true\n")
	term := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"new", sess}})
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "setup")
	enterTerminalMode(t, term)
	runInShell(t, term, "echo HOME-$((6*7))", "HOME-42", shellTimeout)

	second := attachIn(t, base, sess, startOpts{cols: 120, rows: 40})
	if err := second.WaitForText("HOME-42", uiTimeout); err != nil {
		t.Fatalf("the second client does not show the session: %v\n%s", err, second.Snapshot())
	}

	toWindowMode(t, term)
	sendKeys(t, term, tuitest.Alt("2"))
	waitShowing(t, base, sess, 2, "", term)
	exactlyPanesOn(t, term, base, sess, 2, 1, "the switch to workspace 2 with two clients")
	// The second client draws the pane the first one opened.
	if err := second.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "HOME-42") && !strings.Contains(s.Text(), "Terminal UI Operating System")
	}, uiTimeout); err != nil {
		t.Errorf("ASSERTION: the second client does not show the new pane on workspace 2: %v\n%s", err, second.Snapshot())
	}
	saveWindowList(t, base, sess, filepath.Join(artifactDir(t), "list-windows.json"))
	alive(t, term, "after the switch")
	alive(t, second, "after the switch")
}

// TestEmptyWorkspacePaneSettingReloads: with the setting off a switch opens
// nothing. The file turns it on while tuios runs, and the next switch opens
// a pane.
func TestEmptyWorkspacePaneSettingReloads(t *testing.T) {
	const sess = "nwr"
	base := t.TempDir()
	cfg := filepath.Join(base, "XDG_CONFIG_HOME", "tuios", "config.toml")
	writeConfig(t, base, "[workspaces]\nnew_window_when_empty = false\n")
	term := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"new", sess}})
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "setup")

	sendKeys(t, term, tuitest.Alt("2"))
	waitShowing(t, base, sess, 2, "", term)
	exactlyPanesOn(t, term, base, sess, 2, 0, "the switch with the setting off")

	writeConfigAtomically(t, cfg, []byte("[workspaces]\nnew_window_when_empty = true\n"))
	// The reload lands some time after the write. Each round goes to
	// workspace 1 and back to an empty workspace, so a round before the
	// reload opens nothing and the first one after it opens a pane.
	ws := 2
	deadline := time.Now().Add(uiTimeout)
	for panesOn(t, base, sess, ws) == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: no switch opened a pane after the file turned the setting on\n%s", term.Snapshot())
		}
		ws++
		if ws > 9 {
			t.Fatalf("ASSERTION: no switch to workspaces 3 to 9 opened a pane after the reload\n%s", term.Snapshot())
		}
		sendKeys(t, term, tuitest.Alt("1"))
		waitShowing(t, base, sess, 1, "", term)
		sendKeys(t, term, tuitest.Alt(strconv.Itoa(ws)))
		waitShowing(t, base, sess, ws, "", term)
		time.Sleep(time.Second)
	}
	exactlyPanesOn(t, term, base, sess, ws, 1, "the switch after the reload")
	alive(t, term, "after the reload")
}
