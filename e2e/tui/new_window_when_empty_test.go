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

func paneRequestsDone(t *testing.T, base, what string) {
	t.Helper()
	var out string
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		var err error
		out, err = tuiosCLI(t, base, "run-command", "--json", "GetSessionInfo")
		if err == nil {
			var res map[string]any
			if json.Unmarshal([]byte(out), &res) == nil {
				info := res
				if d, ok := res["data"].(map[string]any); ok {
					info = d
				}
				if reqs, _ := info["pane_requests"].([]any); len(reqs) == 0 {
					return
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("%s: a pane request is still in flight\n%s", what, out)
}

func clientWorkspace(t *testing.T, base string) int {
	t.Helper()
	out, err := tuiosCLI(t, base, "run-command", "--json", "GetSessionInfo")
	if err != nil {
		t.Fatalf("GetSessionInfo: %v\n%s", err, out)
	}
	var res map[string]any
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("GetSessionInfo json: %v\n%s", err, out)
	}
	info := res
	if d, ok := res["data"].(map[string]any); ok {
		info = d
	}
	ws, _ := info["current_workspace"].(float64)
	return int(ws)
}

func clientMode(t *testing.T, base string) string {
	t.Helper()
	out, err := tuiosCLI(t, base, "run-command", "--json", "GetSessionInfo")
	if err != nil {
		t.Fatalf("GetSessionInfo: %v\n%s", err, out)
	}
	mode, err := sessionInfoMode(out)
	if err != nil {
		t.Fatalf("GetSessionInfo json: %v\n%s", err, out)
	}
	return mode
}

func daemonClientMode(base string) string {
	out, err := tuiosOut(base, "run-command", "--json", "GetSessionInfo")
	if err != nil {
		return ""
	}
	if mode, _ := sessionInfoMode(out); mode != "unknown" {
		return mode
	}
	return ""
}

func sessionInfoMode(out string) (string, error) {
	var res map[string]any
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		return "", err
	}
	info := res
	if d, ok := res["data"].(map[string]any); ok {
		info = d
	}
	mode, _ := info["mode"].(string)
	return mode, nil
}

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

func exactlyPanesOn(t *testing.T, term *tuitest.Terminal, base, sess string, ws, n int, what string) {
	t.Helper()
	waitPanesOn(t, term, base, sess, ws, n, what)
	paneRequestsDone(t, base, what)
	if got := panesOn(t, base, sess, ws); got != n {
		t.Fatalf("ASSERTION: %s: workspace %d has %d panes, want exactly %d\n%s", what, ws, got, n, term.Snapshot())
	}
}

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
	writeConfig(t, base, "[workspaces]\nnew_window_when_empty = true\nreturn_when_empty = false\n"+
		"[appearance]\nnew_window_inherit_cwd = false\n"+
		"[keybindings.workspaces]\nmove_and_follow_7 = [\"alt+m\"]\n")
	start := projectDir(t, base, "start")
	elsewhere := projectDir(t, base, "elsewhere")

	term := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"new", sess, "--cwd", start}})
	waitBoot(t, term)
	paneRequestsDone(t, base, "startup")
	if n := len(xpanesRowsIn(t, base, sess)); n != 0 {
		t.Fatalf("ASSERTION: startup opened %d panes, want none\n%s", n, term.Snapshot())
	}
	newWindow(t, term)
	waitWindowCount(t, term, 1, "setup")
	enterTerminalMode(t, term)
	runInShell(t, term, "cd '"+elsewhere+"' && echo HOME-$((6*7))", "HOME-42", shellTimeout)

	sendKeys(t, term, tuitest.Alt("2"))
	waitShowing(t, base, sess, 2, "", term)
	exactlyPanesOn(t, term, base, sess, 2, 1, "the switch to workspace 2")
	if got := cwdOnWorkspace(t, base, sess, 2, elsewhere); got != elsewhere {
		t.Errorf("ASSERTION: the pane on workspace 2 is in %q, want %q, the folder of the pane the switch came from", got, elsewhere)
	}
	if got := clientMode(t, base); got != "terminal" {
		t.Fatalf("ASSERTION: after a switch in terminal mode the new pane arrived in %q mode, want terminal (#1173)\n%s", got, term.Snapshot())
	}
	runInShell(t, term, "echo NEW-$((7*7))", "NEW-49", shellTimeout)

	if err := term.SendKeys("exit", tuitest.Enter); err != nil {
		t.Fatalf("type exit: %v", err)
	}
	waitPanesOn(t, term, base, sess, 2, 0, "the shell on workspace 2 exits")
	sendKeys(t, term, tuitest.Alt("5"))
	waitShowing(t, base, sess, 2, "", term)
	exactlyPanesOn(t, term, base, sess, 2, 1, "the switch to a new workspace from an empty workspace")
	if got := cwdOnWorkspace(t, base, sess, 2, start); got != start {
		t.Errorf("ASSERTION: the pane on workspace 2 is in %q, want the session's start directory %q", got, start)
	}
	if got := clientMode(t, base); got != "window_management" {
		t.Errorf("ASSERTION: after a switch in window mode the new pane arrived in %q mode, want window_management", got)
	}

	var moved string
	for _, r := range xpanesRowsIn(t, base, sess) {
		if r.Workspace == 2 {
			moved = r.ID
		}
	}
	toWindowMode(t, term, base)
	sendKeys(t, term, tuitest.Alt("m"))
	waitShowing(t, base, sess, 6, "", term)
	exactlyPanesOn(t, term, base, sess, 6, 1, "move_and_follow to workspace 7")
	for _, r := range xpanesRowsIn(t, base, sess) {
		if r.Workspace == 6 && r.ID != moved {
			t.Errorf("ASSERTION: workspace 6 holds %s, not the pane that moved there (%s)", r.ID, moved)
		}
	}

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
			if ws != 1 && ws != 6 && n >= 2 {
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

	if out, err := tuiosCLI(t, base, "run-command", "SwitchWorkspace", "4"); err != nil {
		t.Fatalf("run-command SwitchWorkspace 4: %v\n%s", err, out)
	}
	waitShowing(t, base, sess, 4, "", term)
	exactlyPanesOn(t, term, base, sess, 4, 0, "run-command SwitchWorkspace")
	if out, err := tuiosCLI(t, base, "select-workspace", "--session", sess, "5"); err != nil {
		t.Fatalf("select-workspace 5: %v\n%s", err, out)
	}
	waitShowing(t, base, sess, 5, "", term)
	exactlyPanesOn(t, term, base, sess, 5, 0, "select-workspace")

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

	toWindowMode(t, term, base)
	sendKeys(t, term, tuitest.Alt("2"))
	waitShowing(t, base, sess, 2, "", term)
	exactlyPanesOn(t, term, base, sess, 2, 1, "the switch to workspace 2 with two clients")
	sendKeys(t, second, tuitest.Esc)
	exactlyPanesOn(t, second, base, sess, 2, 1, "the second client after the switch")
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
	ws := 2
	deadline := time.Now().Add(uiTimeout)
	for round := 0; panesOn(t, base, sess, ws) == 0; round++ {
		if time.Now().After(deadline) || round == 8 {
			t.Fatalf("ASSERTION: no switch opened a pane after the file turned the setting on\n%s", term.Snapshot())
		}
		sendKeys(t, term, tuitest.Alt("1"))
		waitShowing(t, base, sess, 1, "", term)
		sendKeys(t, term, tuitest.Alt(strconv.Itoa(ws+1+round%3)))
		waitShowing(t, base, sess, ws, "", term)
		time.Sleep(time.Second)
	}
	exactlyPanesOn(t, term, base, sess, ws, 1, "the switch after the reload")
	alive(t, term, "after the reload")
}

// TestEmptyWorkspaceFastSwitches sends each sequence as one write, with no
// wait between the keys, and checks where the session ends and how many
// panes each workspace holds once no pane request is in flight.
//
//   - Alt+2 Alt+1: the pane opens on 2, and the session ends on 1. The pane
//     must not pull the session back to 2.
//   - Alt+2 Alt+1 Alt+2: one pane on 2, not one per visit.
//   - Alt+2 Alt+3: one pane on each, and the session ends on 3.
//
// A local daemon can answer before the next key is read, which hides both
// races. knob hold_pane holds each pane request in the daemon for a
// second, as a slow daemon or a slow link would, so the pushes the later keys
// make queue behind it.
func TestEmptyWorkspaceFastSwitches(t *testing.T) {
	const sess = "nwf"
	base := t.TempDir()
	hold := filepath.Join(base, "hold-pane")
	writeConfig(t, base, "[workspaces]\nnew_window_when_empty = true\n")
	term := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"new", sess},
		env: holdEnv(hold)})
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "setup")
	toWindowMode(t, term, base)

	run := func(name string, keys ...any) {
		t.Helper()
		if err := os.WriteFile(hold, []byte("1000"), 0o644); err != nil {
			t.Fatal(err)
		}
		sendKeys(t, term, keys...)
	}
	check := func(name string, final int, want map[int]int) {
		t.Helper()
		began := time.Now()
		defer func() { t.Logf("%s: the check took %v", name, time.Since(began)) }()
		for ws, n := range want {
			waitPanesOn(t, term, base, sess, ws, n, name)
		}
		paneRequestsDone(t, base, name)
		if ws := currentWorkspace(t, base, sess); ws != final {
			t.Fatalf("ASSERTION: %s: the session shows workspace %d, want %d\n%s", name, ws, final, term.Snapshot())
		}
		if ws := clientWorkspace(t, base); ws != final {
			t.Fatalf("ASSERTION: %s: the client shows workspace %d, want %d\n%s", name, ws, final, term.Snapshot())
		}
		for ws, n := range want {
			if got := panesOn(t, base, sess, ws); got != n {
				t.Fatalf("ASSERTION: %s: workspace %d has %d panes, want %d\n%s", name, ws, got, n, term.Snapshot())
			}
		}
	}
	reset := func(wss ...int) {
		t.Helper()
		if err := os.Remove(hold); err != nil {
			t.Fatal(err)
		}
		if currentWorkspace(t, base, sess) != 1 {
			sendKeys(t, term, tuitest.Alt("1"))
			waitShowing(t, base, sess, 1, "", term)
		}
		for _, ws := range wss {
			if out, err := tuiosCLI(t, base, "close-workspace", "--session", sess, strconv.Itoa(ws)); err != nil {
				t.Fatalf("close-workspace %d: %v\n%s", ws, err, out)
			}
			waitPanesOn(t, term, base, sess, ws, 0, "reset")
		}
	}

	run("Alt+2 Alt+1", tuitest.Alt("2"), tuitest.Alt("1"))
	check("Alt+2 Alt+1", 1, map[int]int{1: 1, 2: 1})
	reset(2)

	run("Alt+2 Alt+1 Alt+2", tuitest.Alt("2"), tuitest.Alt("1"), tuitest.Alt("2"))
	check("Alt+2 Alt+1 Alt+2", 2, map[int]int{1: 1, 2: 1})
	reset(2)

	run("Alt+2 Alt+3", tuitest.Alt("2"), tuitest.Alt("3"))
	check("Alt+2 Alt+3", 3, map[int]int{1: 1, 2: 1, 3: 1})
	_ = os.Remove(hold)

	saveWindowList(t, base, sess, filepath.Join(artifactDir(t), "list-windows.json"))
	alive(t, term, "after the fast switches")
}

func holdEnv(hold string) []string {
	return []string{"TUIOS_E2E=1", knob("hold_pane=" + hold)}
}

func setHold(t *testing.T, hold, value string) {
	t.Helper()
	if err := os.WriteFile(hold, []byte(value), 0o644); err != nil {
		t.Fatal(err)
	}
}

func startHeld(t *testing.T, base, sess, hold string) *tuitest.Terminal {
	t.Helper()
	writeConfig(t, base, "[workspaces]\nnew_window_when_empty = true\n")
	term := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"new", sess}, env: holdEnv(hold)})
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "setup")
	toWindowMode(t, term, base)
	return term
}

// TestEmptyWorkspaceSlowRequestOpensOnePane: the daemon holds the first
// request for 7 s, longer than a client waits before it asks again. The
// client switches away and back at 5.5 s, so it asks a second time. The
// workspace still gets one pane: the daemon refuses a second one.
func TestEmptyWorkspaceSlowRequestOpensOnePane(t *testing.T) {
	const sess = "nws"
	base := t.TempDir()
	hold := filepath.Join(base, "hold-pane")
	term := startHeld(t, base, sess, hold)

	setHold(t, hold, "7000")
	sendKeys(t, term, tuitest.Alt("2"))
	time.Sleep(5500 * time.Millisecond)
	sendKeys(t, term, tuitest.Alt("1"), tuitest.Alt("2"))
	if err := os.Remove(hold); err != nil {
		t.Fatal(err)
	}
	exactlyPanesOn(t, term, base, sess, 2, 1, "a request slower than the client's wait")
	if ws := currentWorkspace(t, base, sess); ws != 2 {
		t.Fatalf("ASSERTION: the session shows workspace %d, want 2\n%s", ws, term.Snapshot())
	}
	saveWindowList(t, base, sess, filepath.Join(artifactDir(t), "list-windows.json"))
	alive(t, term, "after the slow request")
}

// TestEmptyWorkspaceTwoClientsSwitchAtOnce: two clients ask for the pane of
// one workspace. The first client switches to workspace 2, and its request
// is held. The second client follows the switch, goes to workspace 1 and
// comes back to 2 by key before the pane exists, so it asks too. The
// workspace gets one pane.
func TestEmptyWorkspaceTwoClientsSwitchAtOnce(t *testing.T) {
	const sess = "nw2c"
	base := t.TempDir()
	hold := filepath.Join(base, "hold-pane")
	term := startHeld(t, base, sess, hold)
	second := attachIn(t, base, sess, startOpts{cols: 120, rows: 40, env: holdEnv(hold)})
	waitWindowCount(t, second, 1, "the second client")

	setHold(t, hold, "2000")
	sendKeys(t, term, tuitest.Alt("2"))
	waitShowing(t, base, sess, 2, "", term)
	sendKeys(t, second, tuitest.Alt("1"), tuitest.Alt("2"))
	if err := os.Remove(hold); err != nil {
		t.Fatal(err)
	}
	exactlyPanesOn(t, term, base, sess, 2, 1, "two clients ask for one workspace")
	sendKeys(t, second, tuitest.Esc)
	exactlyPanesOn(t, second, base, sess, 2, 1, "the second client's request")
	saveWindowList(t, base, sess, filepath.Join(artifactDir(t), "list-windows.json"))
	alive(t, term, "after two requests")
	alive(t, second, "after two requests")
}

// TestEmptyWorkspaceFailedRequestKeepsTheSwitch: the daemon refuses the
// request. The switch to workspace 2 stands with no pane, and the next
// switch, to workspace 1, reaches the daemon.
func TestEmptyWorkspaceFailedRequestKeepsTheSwitch(t *testing.T) {
	const sess = "nwr2"
	base := t.TempDir()
	hold := filepath.Join(base, "hold-pane")
	term := startHeld(t, base, sess, hold)

	setHold(t, hold, "refuse")
	sendKeys(t, term, tuitest.Alt("2"))
	waitShowing(t, base, sess, 2, "", term)
	sendKeys(t, term, tuitest.Alt("1"))
	deadline := time.Now().Add(uiTimeout)
	for currentWorkspace(t, base, sess) != 1 {
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: after a refused request, Alt+1 left the session on workspace %d\n%s", currentWorkspace(t, base, sess), term.Snapshot())
		}
		time.Sleep(100 * time.Millisecond)
	}
	if n := panesOn(t, base, sess, 2); n != 0 {
		t.Fatalf("ASSERTION: workspace 2 has %d panes after a refused request, want 0", n)
	}
	alive(t, term, "after the refused request")
}

// TestEmptyWorkspacePaneFollowsSSH: with appearance.new_window_follow_ssh,
// a switch from a pane that runs ssh opens a pane that runs the same ssh, as
// the new-window key does.
func TestEmptyWorkspacePaneFollowsSSH(t *testing.T) {
	term, _, runs := startSSHSplit(t, "\n[appearance]\nnew_window_follow_ssh = true\n[workspaces]\nnew_window_when_empty = true\n")
	sshIn(t, term, "ssh -l pollen fakehost uptime", 0)
	sendKeys(t, term, tuitest.Alt("2"))
	wantArgs(t, term, "the pane on workspace 2", sshRunArgs(t, term, runs, 1),
		[]string{"-l", "pollen", "-o", "ControlMaster=no", "fakehost"})
	alive(t, term, "after the followed pane")
}
