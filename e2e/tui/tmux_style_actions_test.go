package tuie2e

import (
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestTmuxStyleActionsRunWhenBound binds the actions that ship with no key and
// presses the keys: cycle_layout moves the session off BSP, new_workspace goes
// to the first workspace with no pane, last_workspace goes back and forth, and
// break_pane takes the pane to the first empty workspace and follows it.
// Workspace 1 holds the one pane, so the first empty one is 2.
//
// The two bindings are in config.toml and nowhere else, so a press that moves
// the session proves the action is registered with the dispatcher, not only
// named in a table.
//
// Negative control: delete the d.Register("new_workspace", handleNewWorkspace)
// line, and the Register("cycle_layout", ...) line, in
// internal/input/prefix_tree_actions.go. The keys bind to a name nothing runs:
// the session stays on workspace 1 and on bsp, and each wait times out. A
// build without last_workspace stays on workspace 2 after alt+g; one without
// break_pane leaves the pane on workspace 1.
func TestTmuxStyleActionsRunWhenBound(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, `[startup]
open_default_window = true
tiled = true
layout = "bsp"

[keybindings.workspaces]
new_workspace = ["alt+n"]
cycle_layout = ["alt+y"]
last_workspace = ["alt+g"]
break_pane = ["alt+b"]
`)
	if out, err := tuiosCLI(t, base, "new", "tmuxish", "--detach"); err != nil {
		t.Fatalf("create tmuxish: %v\n%s", err, out)
	}
	term := attachIn(t, base, "tmuxish", startOpts{cols: 120, rows: 40})
	clientShows(t, base, "tmuxish")
	waitForSettledGeometryIn(t, base, "tmuxish", 1)

	// Positive half first: before any key, the session is on workspace 1 in bsp.
	if info := sessionInfoOf(t, base, "tmuxish"); info.CurrentWorkspace != 1 || info.LayoutMode != "bsp" {
		t.Fatalf("tmuxish starts on workspace %d in %q, want 1 in bsp", info.CurrentWorkspace, info.LayoutMode)
	}

	if err := term.SendKeys(tuitest.Alt('y')); err != nil {
		t.Fatalf("press alt+y: %v", err)
	}
	waitLayoutMode(t, base, "tmuxish", "master-stack")

	if err := term.SendKeys(tuitest.Alt('n')); err != nil {
		t.Fatalf("press alt+n: %v", err)
	}
	waitWorkspace(t, base, "tmuxish", 2)

	// last_workspace goes back to 1, and a second press goes to 2 again.
	for _, want := range []int{1, 2, 1} {
		if err := term.SendKeys(tuitest.Alt('g')); err != nil {
			t.Fatalf("press alt+g: %v", err)
		}
		waitWorkspace(t, base, "tmuxish", want)
	}

	// break_pane takes the pane of workspace 1 to the first empty workspace
	// and follows it. Workspace 2 may hold a pane by now, opened by the switch
	// to it when workspaces.new_window_when_empty is on, so the pane's new
	// workspace is read, not assumed.
	pane := ""
	for id, p := range listPanes(t, base, "tmuxish") {
		if p.Workspace == 1 {
			pane = id
		}
	}
	if pane == "" {
		t.Fatalf("setup: no pane on workspace 1: %+v", listPanes(t, base, "tmuxish"))
	}
	if err := term.SendKeys(tuitest.Alt('b')); err != nil {
		t.Fatalf("press alt+b: %v", err)
	}
	moved := waitPane(t, base, "tmuxish", pane, "break_pane moves the pane off workspace 1", func(p paneRow) bool { return p.Workspace > 1 })
	for _, p := range listPanes(t, base, "tmuxish") {
		if p.ID != pane && p.Workspace == moved.Workspace {
			t.Errorf("break_pane put the pane on workspace %d, which already holds %s", moved.Workspace, p.ID)
		}
	}
	waitWorkspace(t, base, "tmuxish", moved.Workspace)
}

// TestNewWorkspaceSkipsTheEmptyWorkspaceOnScreen: new_workspace goes where the
// dock's + button goes, which is never the workspace already on screen. With
// the pane on workspace 1 and the view on the empty workspace 2, the key goes
// to 3. Counting the workspace on screen as free would leave the view on 2 and
// open a pane there.
//
// The positive half is TestTmuxStyleActionsRunWhenBound: from workspace 1,
// which has a pane, the same key goes to 2, the lowest free one.
//
// Negative control: replace `i != m.dockWorkspace() &&` in nextFreeWorkspace
// (internal/app/dock_helpers.go) with nothing. The key from the empty
// workspace 2 stays on 2.
func TestNewWorkspaceSkipsTheEmptyWorkspaceOnScreen(t *testing.T) {
	term, base := panesSession(t, "skipper", `
[keybindings.workspaces]
new_workspace = ["alt+n"]
`)
	waitForSettledGeometryIn(t, base, "skipper", 1)

	if err := term.SendKeys(tuitest.Alt('2')); err != nil {
		t.Fatalf("press alt+2: %v", err)
	}
	waitWorkspace(t, base, "skipper", 2)
	if err := term.SendKeys(tuitest.Alt('n')); err != nil {
		t.Fatalf("press alt+n: %v", err)
	}
	waitWorkspace(t, base, "skipper", 3)
}

// TestSendPrefixAndLastSession: send_prefix hands the leader to the program in
// the pane, which cat -v prints as ^B, and last_session goes back to the
// session the client came from.
//
// Negative control: a build without the two actions prints no ^B and stays
// on the second session.
func TestSendPrefixAndLastSession(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, `[startup]
open_default_window = true

[keybindings.terminal_mode]
send_prefix = ["alt+s"]
last_session = ["alt+u"]
`)
	for _, name := range []string{"first", "second"} {
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("create %s: %v\n%s", name, err, out)
		}
	}
	term := attachIn(t, base, "first", startOpts{cols: 120, rows: 40})
	clientShows(t, base, "first")
	waitForSettledGeometryIn(t, base, "first", 1)
	enterTerminalMode(t, term)
	if err := term.SendKeys("cat -v", tuitest.Enter); err != nil {
		t.Fatalf("start cat -v: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	if err := term.SendKeys(tuitest.Alt('s'), tuitest.Enter); err != nil {
		t.Fatalf("press alt+s: %v", err)
	}
	if err := term.WaitForText("^B", uiTimeout); err != nil {
		t.Fatalf("send_prefix did not reach the pane as ^B: %v\n%s", err, term.Snapshot())
	}

	if err := term.SendKeys(tuitest.Ctrl('b'), ")"); err != nil {
		t.Fatalf("press ctrl+b ): %v", err)
	}
	clientShows(t, base, "second")
	if err := term.SendKeys(tuitest.Alt('u')); err != nil {
		t.Fatalf("press alt+u: %v", err)
	}
	clientShows(t, base, "first")
}
