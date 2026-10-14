package tuie2e

import (
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestSplitPrefixNestedChord drives the new split prefix: leader, t (window
// prefix), s (split prefix, nested inside the window prefix), y (split
// vertical). This is a second level of prefix nesting, which nothing else in
// tuios does, so the flow proves both that window_prefix_split correctly
// transitions into the new scope and that split_prefix_vertical dispatches
// the same real split handler split_vertical/prefix_split_vertical already
// use, rather than sitting inert in the config map.
//
// NEGATIVE CONTROL: delete the d.Register(sp.Gate, ...) line in
// internal/input/prefix_actions.go, or the window_prefix_split default binding
// in internal/config/userconfig.go, or the d.Register("split_prefix_vertical",
// ...) line in internal/input/prefix_tree_actions.go. The chord dies at that
// link, the window count never moves and the test fails on waitWindowCount.
func TestSplitPrefixNestedChord(t *testing.T) {
	term, base := start(t, startOpts{cols: 120, rows: 40, args: []string{"new", "split-y"}})
	killDaemon(t, base)
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "first window")

	if err := term.SendKeys(tuitest.Ctrl('b'), "t", "s", "y"); err != nil {
		t.Fatalf("send leader t s y: %v", err)
	}

	waitWindowCount(t, term, 2, "after leader t s y (split vertical)")

	if err := term.WaitFor(hasInteriorVerticalSplit, uiTimeout); err != nil {
		t.Fatalf("split_prefix_vertical never rendered an interior vertical split: %v\n%s",
			err, term.Snapshot())
	}
}

// TestSplitPrefixHorizontal is the same chord ending in x (split horizontal)
// instead of y, checked against window count only: the horizontal case
// exercises the same routing path as the vertical one above, so this test's
// job is narrower, just confirming x is wired to a different real action
// than y and not the same one twice.
func TestSplitPrefixHorizontal(t *testing.T) {
	term, base := start(t, startOpts{cols: 120, rows: 40, args: []string{"new", "split-x"}})
	killDaemon(t, base)
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "first window")

	if err := term.SendKeys(tuitest.Ctrl('b'), "t", "s", "x"); err != nil {
		t.Fatalf("send leader t s x: %v", err)
	}

	waitWindowCount(t, term, 2, "after leader t s x (split horizontal)")
}

// TestSplitPrefixFromTerminalMode is the same chord pressed from terminal mode,
// which routes the open menu through HandleTerminalModeKey rather than
// routeKey: two copies of one rule, and a cut in either is a dead chord for
// half the app.
//
// NEGATIVE CONTROL: delete the `if o.SubPrefix != ""` block in
// HandleTerminalModeKey (internal/input/keyboard_terminal.go). The chord opens
// the split menu and the next key is typed into the shell, so the window count
// never reaches 2.
func TestSplitPrefixFromTerminalMode(t *testing.T) {
	term, base := start(t, startOpts{cols: 120, rows: 40, args: []string{"new", "split-term"}})
	killDaemon(t, base)
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "first window")
	enterTerminalMode(t, term)

	if err := term.SendKeys(tuitest.Ctrl('b'), "t", "s", "y"); err != nil {
		t.Fatalf("send leader t s y: %v", err)
	}

	waitWindowCount(t, term, 2, "after leader t s y from terminal mode")
}

// TestResizeMenuStaysOpenUntilAnUnboundKey is the resize menu's tmux -r
// behaviour: leader t R then h h narrows the focused pane twice on one chord,
// with the which-key panel listing the menu's keys while it is open; z, which the menu
// does not bind, closes it, so the next h is the ordinary focus key and the
// pane stops shrinking.
//
// The first press is the positive half: the pane does shrink, so the unchanged
// width at the end is the menu closing and not a resize that never worked.
//
// Negative control: delete the `m.SubPrefix = section` re-open in
// HandleSubPrefixCommand (internal/input/prefix_routing.go). The first h
// resizes and closes the menu, the second h is a focus key, and the width does
// not shrink a second time.
func TestResizeMenuStaysOpenUntilAnUnboundKey(t *testing.T) {
	term, base := panesSession(t, "sticky", "")
	waitForSettledGeometryIn(t, base, "sticky", 1)
	b := newWindowIn(t, base, "sticky")
	waitForSettledGeometryIn(t, base, "sticky", 2)
	// b is the focused pane, right of a.
	width := func() int { return listPanes(t, base, "sticky")[b].Width }
	w0 := width()

	if err := term.SendKeys(tuitest.Ctrl('b'), "t", "R"); err != nil {
		t.Fatalf("send leader t R: %v", err)
	}
	if err := term.WaitForText("Narrow the focused pane by 20", uiTimeout); err != nil {
		t.Fatalf("the resize menu's panel never showed: %v\n%s", err, term.Snapshot())
	}
	waitShrinks := func(from int, what string) int {
		t.Helper()
		var got int
		deadline := time.Now().Add(uiTimeout)
		for time.Now().Before(deadline) {
			if got = width(); got < from {
				return got
			}
			time.Sleep(150 * time.Millisecond)
		}
		t.Fatalf("ASSERTION: %s: pane b is still %d wide, want less than %d", what, got, from)
		return got
	}
	if err := term.SendKeys("h"); err != nil {
		t.Fatalf("send h: %v", err)
	}
	w1 := waitShrinks(w0, "the first h")
	if err := term.SendKeys("h"); err != nil {
		t.Fatalf("send second h: %v", err)
	}
	w2 := waitShrinks(w1, "the second h, on the same chord")

	// z is bound nowhere in the menu: it closes it. The h after it is a plain
	// key in window mode, which moves focus and leaves the width alone.
	if err := term.SendKeys("z", "h"); err != nil {
		t.Fatalf("send z h: %v", err)
	}
	time.Sleep(700 * time.Millisecond)
	if got := width(); got != w2 {
		t.Fatalf("pane b is %d wide after z h, want %d: the menu was still open", got, w2)
	}
}

// TestMoveMenuClosesAfterOneKey: leader t m l swaps the focused pane with the
// one on its right, and the menu is gone after that one key, so a second l does
// not swap it back.
//
// Negative control: delete the d.Register(sp.Gate, ...) line in
// internal/input/prefix_actions.go. The chord never opens the menu and the
// panes do not swap.
func TestMoveMenuClosesAfterOneKey(t *testing.T) {
	term, base := panesSession(t, "mover2", "")
	a := waitForSettledGeometryIn(t, base, "mover2", 1)[0].ID
	b := newWindowIn(t, base, "mover2")
	waitForSettledGeometryIn(t, base, "mover2", 2)
	if out, err := tuiosCLI(t, base, "focus-window", "-s", "mover2", a); err != nil {
		t.Fatalf("focus a: %v\n%s", err, out)
	}
	if rows := listPanes(t, base, "mover2"); rows[a].X >= rows[b].X {
		t.Fatalf("setup: a %+v is not left of b %+v", rows[a], rows[b])
	}

	if err := term.SendKeys(tuitest.Ctrl('b'), "t", "m", "l"); err != nil {
		t.Fatalf("send leader t m l: %v", err)
	}
	waitPane(t, base, "mover2", a, "a swaps to the right of b", func(p paneRow) bool {
		return p.X > listPanes(t, base, "mover2")[b].X
	})

	// One more l: a closed menu leaves it to window mode, which does not
	// move panes. A menu still open would swap a back.
	if err := term.SendKeys("l"); err != nil {
		t.Fatalf("send l: %v", err)
	}
	time.Sleep(700 * time.Millisecond)
	if rows := listPanes(t, base, "mover2"); rows[a].X <= rows[b].X {
		t.Fatalf("a went back to %+v, b %+v: the move menu stayed open", rows[a], rows[b])
	}
}
