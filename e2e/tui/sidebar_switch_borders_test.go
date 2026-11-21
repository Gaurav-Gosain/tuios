package tuie2e

import (
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

func isVertLine(r rune) bool {
	switch r {
	case '│', '┃', '║', '|', '┆', '┇', '┊', '┋':
		return true
	}
	return false
}

func isCorner(r rune) bool {
	switch r {
	case '╭', '╮', '╰', '╯', '┌', '┐', '└', '┘', '├', '┤', '┬', '┴', '┼':
		return true
	}
	return false
}

func tallVertCols(s tuitest.Screen) map[int]int {
	cols, rows := s.Size()
	out := map[int]int{}
	for c := range cols {
		run := 0
		for r := 1; r < rows-2; r++ {
			line := []rune(s.Line(r))
			if c < len(line) && isVertLine(line[c]) {
				run++
			}
		}
		if run >= (rows-3)/2 {
			out[c] = run
		}
	}
	return out
}

func cornersInColumn(s tuitest.Screen, col int) int {
	_, rows := s.Size()
	n := 0
	for r := 1; r < rows-2; r++ {
		line := []rune(s.Line(r))
		if col >= 0 && col < len(line) && isCorner(line[col]) {
			n++
		}
	}
	return n
}

func buildTiledSessionSwitch(t *testing.T, side string) *tuitest.Terminal {
	t.Helper()
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, "[appearance]\nsidebar_enabled = true\nsidebar_position = \""+side+"\"\nshared_borders = true\n")

	if out, err := tuiosCLI(t, base, "new", "alpha", "--detach"); err != nil {
		t.Fatalf("create alpha: %v: %s", err, out)
	}
	if out, err := tuiosCLI(t, base, "new", "bravo", "--detach"); err != nil {
		t.Fatalf("create bravo: %v: %s", err, out)
	}

	term := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"attach", "alpha"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == 1
	}, bootTimeout); err != nil {
		t.Fatalf("attach: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Alt(tuitest.Esc)); err != nil {
		t.Fatalf("wm mode: %v", err)
	}
	if err := term.WaitForText("Window management mode", uiTimeout); err != nil {
		t.Fatalf("no wm: %v\n%s", err, term.Snapshot())
	}

	waitForAll(t, term, uiTimeout, "sidebar", sidebarHeader, "bravo")
	enableTiling(t, term)
	newWindow(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 3, "three panes on alpha")
	time.Sleep(300 * time.Millisecond)

	col, row := findOnScreen(t, term, "bravo")
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
	if err := term.WaitForText("Session: bravo", uiTimeout); err != nil {
		t.Fatalf("switch to bravo: %v\n%s", err, term.Snapshot())
	}
	col, row = findOnScreen(t, term, "alpha")
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
	if err := term.WaitForText("Session: alpha", uiTimeout); err != nil {
		t.Fatalf("switch back to alpha: %v\n%s", err, term.Snapshot())
	}
	waitWindowCount(t, term, 3, "three panes after switch back")
	time.Sleep(600 * time.Millisecond)
	return term
}

// TestSidebarRightSharedBordersNoDoubleAfterSwitch is N8: with the sidebar on the
// right and shared borders on, switching sessions must not leave a corner cap in
// the content column against the sidebar edge, which reads as a doubled border.
func TestSidebarRightSharedBordersNoDoubleAfterSwitch(t *testing.T) {
	term := buildTiledSessionSwitch(t, "right")
	s := term.Screen()

	cols := tallVertCols(s)
	if len(cols) == 0 {
		t.Fatalf("no vertical rules on screen\n%s", term.Snapshot())
	}
	edge := -1
	for c := range cols {
		if c > edge {
			edge = c
		}
	}
	if n := cornersInColumn(s, edge-1); n != 0 {
		t.Errorf("doubled border: %d corner caps in content column %d against the sidebar edge at %d\n%s",
			n, edge-1, edge, term.Snapshot())
	}
	for c := range cols {
		if _, ok := cols[c-1]; ok {
			t.Errorf("adjacent doubled vertical rules at columns %d and %d\n%s", c-1, c, term.Snapshot())
		}
	}
}

// TestSidebarLeftSharedBordersSurvivesSwitch is the flagged sidebar-on-LEFT +
// session-switch case: the panes re-tile into the reduced content box and no
// doubled full-height divider appears.
func TestSidebarLeftSharedBordersSurvivesSwitch(t *testing.T) {
	term := buildTiledSessionSwitch(t, "left")
	s := term.Screen()

	cols := tallVertCols(s)
	if len(cols) < 2 {
		t.Fatalf("expected the sidebar edge and at least one pane divider, got %v\n%s", cols, term.Snapshot())
	}
	edge := 1 << 30
	for c := range cols {
		if c < edge {
			edge = c
		}
	}
	if n := cornersInColumn(s, edge+1); n != 0 {
		t.Errorf("doubled border: %d corner caps in content column %d against the sidebar edge at %d\n%s",
			n, edge+1, edge, term.Snapshot())
	}
	for c := range cols {
		if _, ok := cols[c-1]; ok {
			t.Errorf("adjacent doubled vertical rules at columns %d and %d\n%s", c-1, c, term.Snapshot())
		}
	}
	alive(t, term, "after sidebar-left session switch")
}
