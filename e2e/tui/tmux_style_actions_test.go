package tuie2e

import (
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestTmuxStyleActionsRunWhenBound binds the actions that ship with no key and
// presses the keys: cycle_layout moves the session off BSP, and new_workspace
// goes to the first workspace with no pane. Workspace 1 holds the one pane, so
// the first empty one is 2.
//
// The two bindings are in config.toml and nowhere else, so a press that moves
// the session proves the action is registered with the dispatcher, not only
// named in a table.
//
// Negative control: delete the d.Register("new_workspace", handleNewWorkspace)
// line, and the Register("cycle_layout", ...) line, in
// internal/input/prefix_tree_actions.go. The keys bind to a name nothing runs:
// the session stays on workspace 1 and on bsp, and each wait times out.
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
