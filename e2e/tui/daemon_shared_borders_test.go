package tuie2e

import (
	"fmt"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

func countTallVerticalBorders(s tuitest.Screen) int {
	cols, rows := s.Size()
	if cols < 8 || rows < 6 {
		return 0
	}
	isVert := func(r rune) bool {
		switch r {
		case '│', '┃', '║', '|', '┆', '┇', '┊', '┋':
			return true
		}
		return false
	}
	n := 0
	for c := 3; c < cols-3; c++ {
		run := 0
		for r := 1; r < rows-2; r++ {
			line := []rune(s.Line(r))
			if c < len(line) && isVert(line[c]) {
				run++
			}
		}
		if run >= (rows-3)/2 {
			n++
		}
	}
	return n
}

func countWideHorizontalBorders(s tuitest.Screen) int {
	cols, rows := s.Size()
	if cols < 8 || rows < 8 {
		return 0
	}
	isHoriz := func(r rune) bool {
		switch r {
		case '─', '━', '═', '┄', '┅', '┈', '┉':
			return true
		}
		return false
	}
	n := 0
	for r := 2; r < rows-3; r++ {
		line := []rune(s.Line(r))
		run := 0
		for c := 2; c < cols-2; c++ {
			if c < len(line) && isHoriz(line[c]) {
				run++
			}
		}
		if run >= (cols-4)/2 {
			n++
		}
	}
	return n
}

func closeFocusedWindow(t *testing.T, term *tuitest.Terminal, want int) {
	t.Helper()
	if err := term.SendKeys("w"); err != nil {
		t.Fatalf("send 'w': %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == want
	}, uiTimeout); err != nil {
		t.Fatalf("window count never fell to %d after close (last %d): %v\n%s",
			want, countWindows(term.Screen()), err, term.Snapshot())
	}
}

// TestDaemonSharedBordersSeparatorClearsOnClose reproduces the reported bug:
// with shared borders on in a daemon session, three tiled windows draw interior
// separators, and after every window is closed the separators must be gone. On
// the broken build the client rebuilds nothing when the last window leaves, so
// the BSP tree keeps the closed windows and CollectSplits still draws their
// dividers over the splash screen.
func TestDaemonSharedBordersSeparatorClearsOnClose(t *testing.T) {
	term, base := start(t, startOpts{cols: 120, rows: 40, args: []string{"--shared-borders", "new", "sb"}})
	killDaemon(t, base)
	waitBoot(t, term)

	baseV := countTallVerticalBorders(term.Screen())
	baseH := countWideHorizontalBorders(term.Screen())

	enableTiling(t, term)
	for i := 1; i <= 3; i++ {
		newWindow(t, term)
		waitWindowCount(t, term, i, fmt.Sprintf("after new window #%d", i))
	}

	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return hasInteriorVerticalSplit(s)
	}, uiTimeout); err != nil {
		t.Fatalf("three tiled windows never drew an interior separator: %v\n%s", err, term.Snapshot())
	}

	for remaining := 2; remaining >= 0; remaining-- {
		closeFocusedWindow(t, term, remaining)
	}
	waitWindowCount(t, term, 0, "after closing all windows")

	if err := term.WaitForText(welcomeText, uiTimeout); err != nil {
		t.Fatalf("splash never returned after closing all windows: %v\n%s", err, term.Snapshot())
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s := term.Screen()
		if countTallVerticalBorders(s) <= baseV && countWideHorizontalBorders(s) <= baseH {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	s := term.Screen()
	t.Fatalf("separators still drawn after all windows closed "+
		"(vertical %d>%d or horizontal %d>%d beyond the clean splash):\n%s",
		countTallVerticalBorders(s), baseV, countWideHorizontalBorders(s), baseH, term.Snapshot())
}

// TestDaemonForcedVerticalSplitStaysVertical reproduces the second bug: in a
// daemon session, ctrl+b | (and the bare '|' window-management binding it shares
// SplitFocusedVertical with) must force a vertical split every time. On the
// broken build the forced direction is dropped on the daemon path, so the new
// window is placed by the default spiral scheme and the splits alternate
// vertical/horizontal by depth. Three forced vertical splits must leave four
// panes all spanning the full usable height, i.e. sharing one Y and one Height.
func TestDaemonForcedVerticalSplitStaysVertical(t *testing.T) {
	term, base := start(t, startOpts{cols: 160, rows: 40, args: []string{"new", "vs"}})
	killDaemon(t, base)
	waitBoot(t, term)

	enableTiling(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "first window")

	for i := 2; i <= 4; i++ {
		if err := term.SendKeys("|"); err != nil {
			t.Fatalf("send '|' #%d: %v", i, err)
		}
		waitWindowCount(t, term, i, fmt.Sprintf("after forced vertical split #%d", i))
	}

	rects := waitForSettledGeometry(t, base, 4)
	for _, r := range rects {
		t.Logf("window %s: (%d,%d) %dx%d", r.ID, r.X, r.Y, r.Width, r.Height)
	}

	y0, h0 := rects[0].Y, rects[0].Height
	for _, r := range rects {
		if r.Y != y0 || r.Height != h0 {
			t.Errorf("window %s is (%d,%d) %dx%d: not full-height like %dx@Y=%d, "+
				"so a forced vertical split was turned horizontal",
				r.ID, r.X, r.Y, r.Width, r.Height, h0, y0)
		}
	}
}
