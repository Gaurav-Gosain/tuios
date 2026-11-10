package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHeadlessDirectionActions: with no TUI client on a session, run-command
// runs the keymap actions that go by direction (parity gap 1, step 2):
// focus_up/down and terminal_focus_*, swap_*, and resize_pane_*_5/20. The
// layout is a | (b over c), split by split-window with no client, so the
// daemon lays it out. Each step reads the panes' rectangles back with
// list-windows. The transcript goes to headless-direction.txt.
//
// How this could pass wrongly, written down first:
//   - A focus step could land on the right pane by chance, as the last one
//     made. Each step starts from a pane the step before did not leave
//     focused, and names the pane it must reach.
//   - A resize could move the wrong border and still change the width. The
//     checks name the edge that moved and the one that stayed: a pane with a
//     neighbour on its right keeps its left edge, and a pane at the right edge
//     keeps its right edge and moves its left one, and both grow.
//   - A swap could reorder nothing but the focus. The check is the two
//     rectangles traded.
//   - A step at the edge (focus left from a) must fail and change nothing.
func TestHeadlessDirectionActions(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	const sess = "headless-dir"
	if out, err := tuiosCLI(t, base, "new", sess, "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	var log strings.Builder
	type win struct {
		ID   string `json:"window_id"`
		X, Y int
		W    int `json:"width"`
		H    int `json:"height"`
	}
	type list struct {
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
	a := read().Windows[0].ID
	b := splitSide(t, base, sess, a, "right", &log)
	c := splitSide(t, base, sess, b, "down", &log)
	label := map[string]string{a: "a", b: "b", c: "c"}
	rect := func(l list, id string) win {
		for _, w := range l.Windows {
			if w.ID == id {
				return w
			}
		}
		return win{}
	}
	show := func(l list) string {
		var parts []string
		for _, id := range []string{a, b, c} {
			r := rect(l, id)
			parts = append(parts, fmt.Sprintf("%s %d,%d %dx%d", label[id], r.X, r.Y, r.W, r.H))
		}
		return "focused " + label[l.Focused] + "; " + strings.Join(parts, "; ")
	}
	run := func(action string, args ...string) error {
		t.Helper()
		out, err := tuiosCLI(t, base, append([]string{"run-command", "-s", sess, action}, args...)...)
		line := fmt.Sprintf("run-command %s %s: err %v %s", action, strings.Join(args, " "), err, strings.TrimSpace(out))
		fmt.Fprintln(&log, line)
		t.Log(line)
		if strings.Contains(out, "requires an attached client") {
			t.Fatalf("%s still needs a client:\n%s", action, out)
		}
		return err
	}
	step := func(what string, ok func(list) bool) list {
		t.Helper()
		l := read()
		line := fmt.Sprintf("  %s: %s", what, show(l))
		fmt.Fprintln(&log, line)
		t.Log(line)
		if !ok(l) {
			t.Fatalf("%s: %s", what, show(l))
		}
		return l
	}
	near := func(got, want int) bool { return got >= want-1 && got <= want+1 }

	start := step("a | (b over c)", func(l list) bool {
		ra, rb, rc := rect(l, a), rect(l, b), rect(l, c)
		return rb.X > ra.X && rc.X == rb.X && rc.Y > rb.Y
	})

	if err := run("FocusWindow", c); err != nil {
		t.Fatalf("FocusWindow c: %v", err)
	}
	if err := run("focus_up"); err != nil {
		t.Fatalf("focus_up: %v", err)
	}
	step("focus_up from c reaches b", func(l list) bool { return l.Focused == b })
	if err := run("terminal_focus_left"); err != nil {
		t.Fatalf("terminal_focus_left: %v", err)
	}
	step("terminal_focus_left from b reaches a", func(l list) bool { return l.Focused == a })
	if err := run("terminal_focus_left"); err == nil {
		t.Fatalf("terminal_focus_left at the left edge succeeded")
	}
	step("terminal_focus_left at the left edge leaves the focus on a", func(l list) bool { return l.Focused == a })
	if err := run("focus_down"); err == nil {
		t.Fatalf("focus_down from a full-height pane succeeded")
	}
	if err := run("terminal_focus_right"); err != nil {
		t.Fatalf("terminal_focus_right: %v", err)
	}
	step("terminal_focus_right from a reaches the right column", func(l list) bool { return l.Focused == b || l.Focused == c })

	// Resize a, which has a neighbour on its right: its right edge moves.
	if err := run("FocusWindow", a); err != nil {
		t.Fatalf("FocusWindow a: %v", err)
	}
	ra0 := rect(start, a)
	if err := run("resize_pane_right_5"); err != nil {
		t.Fatalf("resize_pane_right_5: %v", err)
	}
	l := step("resize_pane_right_5 widens a by 5 from its right edge", func(l list) bool {
		r := rect(l, a)
		return r.X == ra0.X && near(r.W, ra0.W+5)
	})
	ra1 := rect(l, a)
	if err := run("resize_pane_left_20"); err != nil {
		t.Fatalf("resize_pane_left_20: %v", err)
	}
	l = step("resize_pane_left_20 narrows a by 20 from its right edge", func(l list) bool {
		r := rect(l, a)
		return r.X == ra1.X && near(r.W, ra1.W-20)
	})
	// Resize b, at the right edge: its left edge moves and it still grows.
	rb0 := rect(l, b)
	if err := run("FocusWindow", b); err != nil {
		t.Fatalf("FocusWindow b: %v", err)
	}
	if err := run("resize_pane_right_5"); err != nil {
		t.Fatalf("resize_pane_right_5 on b: %v", err)
	}
	l = step("resize_pane_right_5 at the right edge widens b from its left edge", func(l list) bool {
		r := rect(l, b)
		return r.X+r.W == rb0.X+rb0.W && near(r.W, rb0.W+5)
	})

	// Swap a with the pane on its right.
	before := l
	if err := run("FocusWindow", a); err != nil {
		t.Fatalf("FocusWindow a: %v", err)
	}
	if err := run("swap_right"); err != nil {
		t.Fatalf("swap_right: %v", err)
	}
	step("swap_right trades a's place with the pane on its right", func(l list) bool {
		na := rect(l, a)
		for _, other := range []string{b, c} {
			o0, o1 := rect(before, other), rect(l, other)
			if na.X == o0.X && na.Y == o0.Y && o1.X == rect(before, a).X && o1.Y == rect(before, a).Y {
				return true
			}
		}
		return false
	})

	if err := os.WriteFile(filepath.Join(artifactDir(t), "headless-direction.txt"), []byte(log.String()), 0o600); err != nil {
		t.Logf("write the transcript: %v", err)
	}
}
