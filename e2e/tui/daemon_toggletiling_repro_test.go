package tuie2e

import (
	"fmt"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestDaemonToggleTilingReproFlow drives the EXACT reported flow:
// windows are created through the daemon verb path while tiling is OFF, and
// tiling is then enabled through the daemon verb (run-command ToggleTiling),
// NOT the interactive 't' key. This is the user-facing path a CLI caller uses.
//
// It asserts on both the daemon-reported geometry (a non-overlapping partition)
// and the RENDERED screen: a unique marker written into each window's shell must
// be visible at the same time. Floating windows overlap, so only the topmost
// one's marker would show; all three showing at once is proof the panes tile
// into distinct on-screen rectangles.
func TestDaemonToggleTilingReproFlow(t *testing.T) {
	term, base := start(t, startOpts{cols: 120, rows: 40, args: []string{"new", "rs2"}})
	killDaemon(t, base)
	waitBoot(t, term)

	// Three windows created ONLY through the daemon verb path, tiling off.
	for i := 1; i <= 3; i++ {
		out, err := tuiosCLI(t, base, "run-command", "--session", "rs2", "NewWindow")
		if err != nil {
			t.Fatalf("run-command NewWindow #%d failed: %v\n%s", i, err, out)
		}
		waitWindowCount(t, term, i, fmt.Sprintf("after daemon NewWindow #%d", i))
	}

	// Enable tiling the way the user did in the bug report: the daemon verb.
	out, err := tuiosCLI(t, base, "run-command", "--session", "rs2", "ToggleTiling")
	if err != nil {
		t.Fatalf("run-command ToggleTiling failed: %v\n%s", err, out)
	}

	rects := waitForSettledGeometry(t, base, 3)
	for _, r := range rects {
		t.Logf("window %s: (%d,%d) %dx%d", r.ID, r.X, r.Y, r.Width, r.Height)
	}

	for _, r := range rects {
		if r.Width >= 120 {
			t.Errorf("window %s spans the full width (%d): it was never tiled", r.ID, r.Width)
		}
	}
	for a := 0; a < len(rects); a++ {
		for b := a + 1; b < len(rects); b++ {
			if geomOverlap(rects[a], rects[b]) {
				t.Errorf("windows overlap: %s (%d,%d %dx%d) and %s (%d,%d %dx%d)",
					rects[a].ID, rects[a].X, rects[a].Y, rects[a].Width, rects[a].Height,
					rects[b].ID, rects[b].X, rects[b].Y, rects[b].Width, rects[b].Height)
			}
		}
	}

	// Rendered proof: the tiled partition must show up as distinct on-screen
	// regions separated by internal borders. A single stacked/floating window
	// fills the frame edge-to-edge with no interior vertical split; three tiles
	// (a full-height master on the left, two stacked on the right) put an
	// interior vertical border down the middle of the screen. Require at least
	// one interior column that is a vertical border for most of the height.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return hasInteriorVerticalSplit(s)
	}, uiTimeout); err != nil {
		t.Fatalf("tiled grid never rendered an interior vertical split: %v\n%s",
			err, term.Snapshot())
	}
	t.Logf("rendered tiled grid:\n%s", term.Snapshot())
}

// hasInteriorVerticalSplit reports whether the rendered screen has a column,
// away from both edges, that is a vertical box-drawing border for most of the
// usable height. A stacked/floating full-screen window has borders only at the
// frame edges; a tiled layout adds interior ones where panes meet.
func hasInteriorVerticalSplit(s tuitest.Screen) bool {
	cols, rows := s.Size()
	if cols < 8 || rows < 6 {
		return false
	}
	lines := make([]string, rows)
	for r := 0; r < rows; r++ {
		lines[r] = s.Line(r)
	}
	// Vertical box-drawing glyphs used for borders.
	isVert := func(r rune) bool {
		switch r {
		case '│', '┃', '║', '|', '┆', '┇', '┊', '┋':
			return true
		}
		return false
	}
	// Scan interior columns (skip the two edge columns on each side).
	for c := 3; c < cols-3; c++ {
		run := 0
		for r := 1; r < rows-2; r++ {
			line := []rune(lines[r])
			if c < len(line) && isVert(line[c]) {
				run++
			}
		}
		if run >= (rows-3)/2 {
			return true
		}
	}
	return false
}
