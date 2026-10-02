package tuie2e

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// Discussion #347: in a BSP layout, a new window always split the last pane in
// the tree, the bottom-right one under the spiral scheme, whichever pane had
// focus. A preselection was lost on the way too, because the daemon makes the
// window and the client adopted it through a state sync that never read the
// preselection. The docs, and bspwm which the layout follows, say a new window
// splits the focused pane, on the preselected side when one is set.
//
// Each case lays out three tiled panes over a daemon session, focuses the
// leftmost pane (the full-height one, never the last in the tree), optionally
// preselects a side, and presses n. The new pane must sit inside the box the
// focused pane had, on the chosen side, and the two other panes must not move.
//
// NEGATIVE CONTROL: against main before the fix every case fails with "the
// new pane is not inside the focused pane's old box", because the new pane
// splits the bottom-right pane instead.

type preselectCase struct {
	name   string
	scheme string // palette row that sets the tree's scheme, or empty for spiral
	key    any    // the preselect chord, or nil for none
	side   string // left, right, up, down, or "" for any side
}

func TestBSPNewWindowSplitsTheFocusedPane(t *testing.T) {
	cases := []preselectCase{
		{"spiral_no_preselection", "", nil, ""},
		{"spiral_preselect_left", "", tuitest.Alt('h'), "left"},
		{"longest_side_preselect_up", "Set tiling scheme: longest side", tuitest.Alt('k'), "up"},
		{"alternate_preselect_down", "Set tiling scheme: alternate", tuitest.Alt('j'), "down"},
		{"smart_split_preselect_right", "Set tiling scheme: smart split", tuitest.Alt('l'), "right"},
		{"smart_split_no_preselection", "Set tiling scheme: smart split", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runPreselectCase(t, tc)
		})
	}
}

func runPreselectCase(t *testing.T, tc preselectCase) {
	session := "presel"
	term, base := tiledPanes(t, session, "", 3)
	if tc.scheme != "" {
		send(t, term, tuitest.Ctrl('p'))
		waitPaletteOpen(t, term, "to pick a tiling scheme")
		send(t, term, tc.scheme, tuitest.Enter)
		waitPaletteClosed(t, term, "after picking a tiling scheme")
	}
	before := waitForSettledGeometryIn(t, base, session, 3)

	// Focus the leftmost pane. Under every scheme here that is a pane other
	// than the last one in the tree, which is the one the bug always split.
	target := before[0]
	for _, r := range before {
		if r.X < target.X || (r.X == target.X && r.Y < target.Y) {
			target = r
		}
	}
	for range 4 {
		if _, f := focusedLayout(t, base, session); f == target.ID {
			break
		}
		send(t, term, "h")
	}
	if _, f := focusedLayout(t, base, session); f != target.ID {
		t.Fatalf("could not focus the leftmost pane %s\n%s", target.ID[:8], term.Snapshot())
	}

	dir := artifactDir(t)
	if tc.key != nil {
		send(t, term, tc.key)
		if err := term.WaitForText("Preselection: "+tc.side, uiTimeout); err != nil {
			t.Fatalf("no preselection message for %s: %v\n%s", tc.side, err, term.Snapshot())
		}
	}
	saveArtifact(t, term, dir, "before")

	send(t, term, "n")
	waitWindowCount(t, term, 4, "after n")
	after := waitForSettledGeometryIn(t, base, session, 4)
	time.Sleep(500 * time.Millisecond)
	saveArtifact(t, term, dir, "after")

	var added winRect
	afterByID := map[string]winRect{}
	for _, r := range after {
		afterByID[r.ID] = r
	}
	for _, r := range after {
		known := false
		for _, b := range before {
			if b.ID == r.ID {
				known = true
			}
		}
		if !known {
			added = r
		}
	}
	if added.ID == "" {
		t.Fatalf("no new pane in %v", after)
	}
	describe := func() string {
		s := fmt.Sprintf("focused pane before: %+v\nnew pane: %+v\n", target, added)
		for _, r := range after {
			s += fmt.Sprintf("after: %s (%d,%d) %dx%d\n", r.ID[:8], r.X, r.Y, r.Width, r.Height)
		}
		return s + term.Snapshot()
	}

	// Two cells of slack cover a shared border or a separator column.
	inside := func(r, box winRect) bool {
		return r.X >= box.X-2 && r.Y >= box.Y-2 &&
			r.X+r.Width <= box.X+box.Width+2 && r.Y+r.Height <= box.Y+box.Height+2
	}
	if !inside(added, target) {
		t.Fatalf("the new pane is not inside the focused pane's old box\n%s", describe())
	}
	for _, b := range before {
		if b.ID == target.ID {
			continue
		}
		if a := afterByID[b.ID]; a != b {
			t.Fatalf("pane %s moved from %+v to %+v; only the focused pane should split\n%s",
				b.ID[:8], b, a, describe())
		}
	}
	old := afterByID[target.ID]
	if !inside(old, target) {
		t.Fatalf("the focused pane left its own box: %+v\n%s", old, describe())
	}
	if !onSide(added, old, tc.side) {
		t.Fatalf("the new pane is not %s the focused pane\n%s", sidePhrase[tc.side], describe())
	}
	t.Logf("frames in %s", dir)
}

var sidePhrase = map[string]string{
	"left": "left of", "right": "right of", "up": "above", "down": "below",
}

// onSide reports whether added sits on side of old, sharing its row or column.
func onSide(added, old winRect, side string) bool {
	switch side {
	case "left":
		return added.X < old.X && added.Y == old.Y
	case "right":
		return added.X > old.X && added.Y == old.Y
	case "up":
		return added.Y < old.Y && added.X == old.X
	case "down":
		return added.Y > old.Y && added.X == old.X
	}
	return true
}

// The standalone TUI makes the window itself rather than asking a daemon, so
// it takes another code path to the tree. There is no daemon to ask for
// geometry, so the panes are read off the screen by their border corners.
// Which of the two halves is the new pane is not visible there, so the side is
// checked as an axis: left and right split side by side, up and down stack.
func TestBSPNewWindowSplitsTheFocusedPaneStandalone(t *testing.T) {
	cases := []struct {
		name     string
		key      any
		sideways bool // the focused pane splits side by side, else stacked
	}{
		{"no_preselection", nil, false}, // spiral at depth one on a wide screen
		{"preselect_left", tuitest.Alt('h'), true},
		{"preselect_down", tuitest.Alt('j'), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			term, _ := start(t, startOpts{cols: 160, rows: 48})
			waitBoot(t, term)
			for range 3 {
				newWindow(t, term)
			}
			waitWindowCount(t, term, 3, "standalone setup")
			enableTiling(t, term)
			before := settledScreenBoxes(t, term, 3)
			target := before[0]
			for _, r := range before {
				if r.X < target.X || (r.X == target.X && r.Y < target.Y) {
					target = r
				}
			}
			// From any of the three panes, two presses of h reach the leftmost.
			send(t, term, "h")
			send(t, term, "h")
			dir := artifactDir(t)
			if tc.key != nil {
				send(t, term, tc.key)
			}
			saveArtifact(t, term, dir, "before")
			send(t, term, "n")
			waitWindowCount(t, term, 4, "after n")
			after := settledScreenBoxes(t, term, 4)
			saveArtifact(t, term, dir, "after")

			var fresh []winRect
			for _, a := range after {
				if !slices.Contains(before, a) {
					fresh = append(fresh, a)
				}
			}
			for _, b := range before {
				if b != target && !slices.Contains(after, b) {
					t.Fatalf("pane %+v moved; only the focused pane %+v should split\nbefore %v\nafter %v\n%s",
						b, target, before, after, term.Snapshot())
				}
			}
			if len(fresh) != 2 {
				t.Fatalf("want the focused pane split in two, got new boxes %v\nbefore %v\nafter %v\n%s",
					fresh, before, after, term.Snapshot())
			}
			for _, f := range fresh {
				if f.X < target.X || f.Y < target.Y || f.X+f.Width > target.X+target.Width || f.Y+f.Height > target.Y+target.Height {
					t.Fatalf("the new pane is not inside the focused pane's old box %+v: %v\n%s",
						target, fresh, term.Snapshot())
				}
			}
			sideways := fresh[0].Y == fresh[1].Y
			if sideways != tc.sideways {
				t.Fatalf("the focused pane split the wrong way: side by side %v, want %v (%v)\n%s",
					sideways, tc.sideways, fresh, term.Snapshot())
			}
			t.Logf("frames in %s", dir)
		})
	}
}

// settledScreenBoxes reads the pane boxes off the screen until n of them stay
// the same for three reads in a row.
func settledScreenBoxes(t *testing.T, term *tuitest.Terminal, n int) []winRect {
	t.Helper()
	var prev []winRect
	stable := 0
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		boxes := screenBoxes(term.Screen())
		if len(boxes) == n && slices.Equal(boxes, prev) {
			stable++
			if stable >= 3 {
				return boxes
			}
		} else {
			stable = 0
		}
		prev = boxes
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("the screen never settled on %d pane boxes (last %v)\n%s", n, prev, term.Snapshot())
	return nil
}

// screenBoxes finds every pane border: a top-left corner, the top-right corner
// on its row, and the bottom-left corner in its column.
func screenBoxes(s tuitest.Screen) []winRect {
	_, rows := s.Size()
	lines := make([][]rune, rows)
	for y := range rows {
		lines[y] = []rune(s.Line(y))
	}
	at := func(x, y int) rune {
		if y < 0 || y >= rows || x < 0 || x >= len(lines[y]) {
			return 0
		}
		return lines[y][x]
	}
	var out []winRect
	for y := range rows {
		for x, r := range lines[y] {
			if r != '╭' {
				continue
			}
			x1 := x + 1
			for x1 < len(lines[y]) && lines[y][x1] != '╮' && lines[y][x1] != '╭' {
				x1++
			}
			if at(x1, y) != '╮' {
				continue
			}
			y1 := y + 1
			for y1 < rows && at(x, y1) != '╰' && at(x, y1) != '╭' {
				y1++
			}
			if at(x, y1) != '╰' {
				continue
			}
			out = append(out, winRect{X: x, Y: y, Width: x1 - x + 1, Height: y1 - y + 1})
		}
	}
	return out
}
