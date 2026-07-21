package tuie2e

import (
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestKeybindTileThenNewWindows drives the owner's EXACT reported sequence
// through a real attached client: `tuios new`, then the keystrokes t, n, n, n.
// `t` toggles tiling on (interactive keybinding), and each `n` creates a window
// (interactive keybinding). The result must be a clean, non-overlapping tiled
// split, not a stack of windows at one coordinate.
//
// This differs from the verb-path tests: `n` and `t` are the interactive
// keybindings the user actually presses, and tiling is enabled while there is
// only the initial window, then windows are added one at a time into an
// already-tiled session.
func TestKeybindTileThenNewWindows(t *testing.T) {
	term, base := start(t, startOpts{cols: 120, rows: 40, args: []string{"new"}})
	killDaemon(t, base)
	waitBoot(t, term)

	// Toggle tiling ON first (owner presses `t`).
	if err := term.SendKeys("t"); err != nil {
		t.Fatalf("send 't': %v", err)
	}
	time.Sleep(400 * time.Millisecond)

	// Then create three windows one at a time (owner presses `n` `n` `n`).
	for i := 1; i <= 3; i++ {
		newWindow(t, term)
	}

	// The daemon is the source of truth for placed geometry (the client syncs
	// its placement back). Read it and assert a non-overlapping partition.
	n := settledWindowCount(t, term)
	rects := waitForSettledGeometry(t, base, n)
	for _, r := range rects {
		t.Logf("window %s: (%d,%d) %dx%d", r.ID, r.X, r.Y, r.Width, r.Height)
	}
	if n < 3 {
		t.Fatalf("expected at least 3 windows after t n n n, got %d", n)
	}

	for _, r := range rects {
		if r.Width >= 120 {
			t.Errorf("window %s spans the full width (%d): it was never tiled", r.ID, r.Width)
		}
	}
	overlaps := 0
	for a := 0; a < len(rects); a++ {
		for b := a + 1; b < len(rects); b++ {
			if geomOverlap(rects[a], rects[b]) {
				overlaps++
				t.Errorf("windows overlap: %s (%d,%d %dx%d) and %s (%d,%d %dx%d)",
					rects[a].ID, rects[a].X, rects[a].Y, rects[a].Width, rects[a].Height,
					rects[b].ID, rects[b].X, rects[b].Y, rects[b].Width, rects[b].Height)
			}
		}
	}

	// Rendered proof: a proper split shows an interior vertical border.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return hasInteriorVerticalSplit(s)
	}, uiTimeout); err != nil {
		t.Errorf("tiled grid never rendered an interior vertical split: %v", err)
	}
	t.Logf("rendered layout:\n%s", term.Snapshot())
}
