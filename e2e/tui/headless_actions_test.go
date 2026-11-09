package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHeadlessKeymapActions: with no TUI client on a session, run-command runs
// the keymap actions that are only an op on session state (parity gap 1,
// step 1): select_window_N, last_pane, send_to_workspace_N,
// move_and_follow_N, break_pane, move_workspace_left/right, minimize_window,
// restore_all and close_workspace. Before, each answered that it needs an
// attached client. Every step reads the daemon's state back with list-windows.
// The transcript goes to headless-actions.txt.
//
// How this could pass wrongly, written down first:
//   - A step could change nothing and the check still hold, because the state
//     already was so. Each step's check names a state the step before it did
//     not have: another focused window, another workspace, a window moved.
//   - An action could do something else that looks right: select_window_3 must
//     focus the third window by window order, not the last one created; the
//     check names it.
//   - An action past its range (select_window_9 with three windows, break_pane
//     with every workspace used, move_workspace_left from workspace 1) must
//     fail or do nothing and leave the state as it was.
func TestHeadlessKeymapActions(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	const sess = "headless"
	if out, err := tuiosCLI(t, base, "new", sess, "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	for _, name := range []string{"b", "c"} {
		if out, err := tuiosCLI(t, base, "new-window", "-s", sess, name); err != nil {
			t.Fatalf("new-window %s: %v\n%s", name, err, out)
		}
	}
	var log strings.Builder
	type win struct {
		ID        string `json:"window_id"`
		Name      string `json:"display_name"`
		Workspace int    `json:"workspace"`
		Minimized bool   `json:"minimized"`
	}
	type list struct {
		Current int    `json:"current_workspace"`
		Focused string `json:"focused_window_id"`
		Windows []win  `json:"windows"`
	}
	read := func() list {
		t.Helper()
		out, err := tuiosCLI(t, base, "list-windows", "-s", sess, "--json")
		if err != nil {
			t.Fatalf("list-windows: %v\n%s", err, out)
		}
		var l list
		if err := json.Unmarshal([]byte(out), &l); err != nil {
			t.Fatalf("read list-windows: %v\n%s", err, out)
		}
		return l
	}
	name := func(l list, id string) string {
		for _, w := range l.Windows {
			if w.ID == id {
				return w.Name
			}
		}
		return "?" + id
	}
	where := func(l list, n string) win {
		for _, w := range l.Windows {
			if w.Name == n {
				return w
			}
		}
		return win{}
	}
	run := func(action string, wantOK bool) {
		t.Helper()
		out, err := tuiosCLI(t, base, "run-command", "-s", sess, action)
		fmt.Fprintf(&log, "run-command %s: err %v %s\n", action, err, strings.TrimSpace(out))
		t.Logf("run-command %s: err %v %s", action, err, strings.TrimSpace(out))
		if strings.Contains(out, "requires an attached client") || strings.Contains(out, "needs the tuios TUI") {
			t.Fatalf("%s still needs a client:\n%s", action, out)
		}
		if wantOK && err != nil {
			t.Fatalf("%s: %v\n%s", action, err, out)
		}
		if !wantOK && err == nil {
			t.Fatalf("%s succeeded, want a refusal:\n%s", action, out)
		}
	}
	step := func(what string, ok func(list) bool) {
		t.Helper()
		l := read()
		var ws []string
		for _, w := range l.Windows {
			s := fmt.Sprintf("%s@%d", w.Name, w.Workspace)
			if w.Minimized {
				s += "(min)"
			}
			ws = append(ws, s)
		}
		fmt.Fprintf(&log, "  %s: current %d, focused %s, windows %s\n", what, l.Current, name(l, l.Focused), strings.Join(ws, " "))
		t.Logf("  %s: current %d, focused %s, windows %s", what, l.Current, name(l, l.Focused), strings.Join(ws, " "))
		if !ok(l) {
			t.Fatalf("%s: current %d, focused %s, windows %s", what, l.Current, name(l, l.Focused), strings.Join(ws, " "))
		}
	}

	first := read()
	if len(first.Windows) != 3 {
		t.Fatalf("want 3 windows, got %d", len(first.Windows))
	}
	a := first.Windows[0].Name
	third := first.Windows[2].Name

	run("select_window_1", true)
	step("select_window_1 focuses the first window", func(l list) bool { return name(l, l.Focused) == a })
	run("select_window_3", true)
	step("select_window_3 focuses the third window", func(l list) bool { return name(l, l.Focused) == third })
	run("select_window_9", false)
	step("select_window_9 of three leaves the focus", func(l list) bool { return name(l, l.Focused) == third })
	run("last_pane", true)
	step("last_pane goes back to the first window", func(l list) bool { return name(l, l.Focused) == a })

	// a is focused on workspace 1 with b and c.
	run("break_pane", true)
	step("break_pane moves the focused window to the first empty workspace and follows", func(l list) bool {
		return where(l, a).Workspace == 2 && l.Current == 2
	})
	if out, err := tuiosCLI(t, base, "run-command", "-s", sess, "SwitchWorkspace", "1"); err != nil {
		t.Fatalf("SwitchWorkspace 1: %v\n%s", err, out)
	}
	run("select_window_1", true)
	x := ""
	step("select_window_1 on workspace 1 focuses a window there", func(l list) bool {
		x = name(l, l.Focused)
		return where(l, x).Workspace == 1 && x != a
	})
	run("send_to_workspace_2", true)
	y := ""
	step("send_to_workspace_2 moves the focused window, stays, and focuses a pane still here", func(l list) bool {
		y = name(l, l.Focused)
		return where(l, x).Workspace == 2 && l.Current == 1 && y != x && where(l, y).Workspace == 1
	})
	run("move_and_follow_3", true)
	step("move_and_follow_3 moves the focused window off the others' workspace and follows it", func(l list) bool {
		// Workspace 1 empties, and the daemon numbers workspaces with no
		// gap, so the window lands on 2, below the 3 asked for.
		return where(l, y).Workspace == l.Current && name(l, l.Focused) == y &&
			where(l, a).Workspace != l.Current && where(l, x).Workspace != l.Current
	})
	run("move_workspace_left", true)
	step("move_workspace_left swaps the current workspace with the one before and follows", func(l list) bool {
		return l.Current == 1 && where(l, y).Workspace == 1 && where(l, a).Workspace == 2 && where(l, x).Workspace == 2
	})
	run("move_workspace_left", true)
	step("move_workspace_left at workspace 1 does nothing", func(l list) bool {
		return l.Current == 1 && where(l, y).Workspace == 1 && where(l, a).Workspace == 2
	})

	run("minimize_window", true)
	step("minimize_window minimizes the focused window", func(l list) bool { return where(l, y).Minimized })
	run("restore_all", true)
	step("restore_all restores it", func(l list) bool { return !where(l, y).Minimized })

	run("close_workspace", true)
	step("close_workspace closes the current workspace's windows", func(l list) bool {
		return where(l, y).ID == "" && len(l.Windows) == 2
	})

	if err := os.WriteFile(filepath.Join(artifactDir(t), "headless-actions.txt"), []byte(log.String()), 0o600); err != nil {
		t.Logf("write the transcript: %v", err)
	}
}
