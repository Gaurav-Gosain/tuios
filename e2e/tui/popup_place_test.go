package tuie2e

import (
	"fmt"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// panelBox is the screen rectangle of the borderless panel whose text
// includes marker: the run of cells on the marker's row that share the
// ground of the cell left of the marker (the panel's padding), and the run of
// rows down the panel's first column that share it. x1 and y1 are one past
// the last cell. The marker must be on a row with the panel's own ground,
// not the cursor's row, whose ground reaches the panel's edge.
func panelBox(s tuitest.Screen, marker string) (x0, y0, x1, y1 int, ok bool) {
	mx, my, found := dockCellFind(s, marker)
	if !found || mx == 0 {
		return 0, 0, 0, 0, false
	}
	cols, rows := s.Size()
	bg := s.Cell(mx-1, my).Bg
	x0, x1 = mx-1, mx
	for x0 > 0 && s.Cell(x0-1, my).Bg == bg {
		x0--
	}
	for x1 < cols && s.Cell(x1, my).Bg == bg {
		x1++
	}
	y0, y1 = my, my+1
	for y0 > 0 && s.Cell(x0, y0-1).Bg == bg {
		y0--
	}
	for y1 < rows && s.Cell(x0, y1).Bg == bg {
		y1++
	}
	return x0, y0, x1, y1, true
}

// TestContextMenuOpensFromThePointerAtEveryEdge opens the desktop menu at the
// middle of the screen and against each edge and corner (the bottom ones on
// the last row above the dock's two rows), and checks that the
// menu is whole on the screen with the pointer cell on one of its corners:
// down and right of the pointer where it fits, flipped across the pointer
// where it does not. It is the proof for moving contextMenuOrigin onto
// overlay.Place. Each frame is saved under artifactDir as menu-NAME.
//
// How this could pass wrongly: a menu shifted along the edge instead of
// flipped is also whole on the screen, so the corner is asserted, not only
// that every row fits. On the last column a shifted menu also ends on the
// pointer, so the right-hand points are four columns in from the edge. The box is read from the panel's ground, so a menu
// drawn somewhere else entirely does not match the pointer either.
func TestContextMenuOpensFromThePointerAtEveryEdge(t *testing.T) {
	const cols, rows = 80, 30
	term, _ := start(t, startOpts{cols: cols, rows: rows})
	waitBoot(t, term)
	art := artifactDir(t)
	for _, tc := range []struct {
		name string
		x, y int
	}{
		{"middle", 40, 14},
		{"left", 0, 14},
		{"right", cols - 4, 14},
		{"top-left", 0, 0},
		{"bottom", 40, rows - 3},
		{"bottom-right", cols - 4, rows - 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			shiftRightClick(t, term, tc.x, tc.y)
			waitMenu(t, term, tc.name, "Desktop", "Command palette")
			if err := term.WaitStable(uiTimeout); err != nil {
				t.Fatalf("the screen never settled: %v", err)
			}
			saveArtifact(t, term, art, "menu-"+tc.name)
			s := term.Screen()
			if !rowHasHint(s, "Command palette") {
				t.Errorf("%s: the menu's key hints are drawn off the screen edge\n%s", tc.name, term.Snapshot())
			}
			assertNoLineOverflow(t, s, cols, tc.name)
			x0, y0, x1, y1, ok := panelBox(s, "Command palette")
			if !ok {
				t.Fatalf("%s: no menu box around Command palette\n%s", tc.name, term.Snapshot())
			}
			if (tc.x != x0 && tc.x != x1-1) || (tc.y != y0 && tc.y != y1-1) {
				t.Errorf("%s: the pointer (%d, %d) is not on a corner of the menu %d..%d x %d..%d\n%s",
					tc.name, tc.x, tc.y, x0, x1-1, y0, y1-1, term.Snapshot())
			}
			if err := term.SendKeys(tuitest.Esc); err != nil {
				t.Fatalf("esc: %v", err)
			}
			waitMenuGone(t, term, "Command palette", tc.name)
		})
	}
	alive(t, term, "after the menu at every edge")
}

// TestSubmenuAtTheRightEdgeOpensLeftOfThePointer opens the desktop menu near
// the right edge, where it opens to the left of the pointer, and opens a
// submenu from it. The submenu takes its parent's place on the same anchor,
// so it opens to the left of the pointer as well, is whole on the screen, and
// covers no parent: the parent's rows are gone. A submenu shifted along the
// edge until it fits (as bloom places one, over its own parent) would have
// its right edge at the screen's edge, four columns right of the pointer.
// The frames are saved under artifactDir.
//
// It pins today's behaviour, which it does not change: the submenu replaced
// its parent in place before overlay.Place, and the flip was already there.
func TestSubmenuAtTheRightEdgeOpensLeftOfThePointer(t *testing.T) {
	const cols, rows = 80, 30
	base := t.TempDir()
	writeConfig(t, base, `[menus.desktop]
add = [{ menu = "tools" }]

[menus.tools]
title = "My tools"
items = ["new_window", "toggle_tiling"]
`)
	term := startIn(t, base, startOpts{cols: cols, rows: rows})
	waitBoot(t, term)
	art := artifactDir(t)

	px, py := cols-5, 6
	shiftRightClick(t, term, px, py)
	waitMenu(t, term, "desktop menu", "Desktop", "My tools")
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("the screen never settled: %v", err)
	}
	saveArtifact(t, term, art, "parent")
	s := term.Screen()
	x0, y0, x1, _, ok := panelBox(s, "My tools")
	if !ok {
		t.Fatalf("no menu box around the submenu row\n%s", term.Snapshot())
	}
	if x1-1 != px || y0 != py {
		t.Errorf("the parent menu is at %d..%d from row %d, want its last column on the pointer column %d and its top on row %d\n%s",
			x0, x1-1, y0, px, py, term.Snapshot())
	}

	toolsX, toolsY, _ := dockCellFind(s, "My tools")
	leftClick(t, term, toolsX+2, toolsY)
	waitMenu(t, term, "submenu", "My tools", "New window")
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("the screen never settled: %v", err)
	}
	saveArtifact(t, term, art, "submenu")
	s = term.Screen()
	if screenHas(s, "Command palette") {
		t.Errorf("the parent menu is still drawn under the submenu\n%s", term.Snapshot())
	}
	assertNoLineOverflow(t, s, cols, "submenu")
	sx0, sy0, sx1, _, ok := panelBox(s, "Toggle tiling")
	if !ok {
		t.Fatalf("no submenu box around Toggle tiling\n%s", term.Snapshot())
	}
	if sx1-1 != px || sy0 != py {
		t.Errorf("the submenu is at %d..%d from row %d, want its last column on the pointer column %d and its top on row %d\n%s",
			sx0, sx1-1, sy0, px, py, term.Snapshot())
	}
	alive(t, term, "after the submenu at the right edge")
}

// TestWhichKeyOpensInEachPosition opens the which-key panel at every
// appearance.whichkey_position and checks it is whole on the screen in the
// corner it names, two cells in from the side edge. It is the proof for
// moving the panel's placement onto overlay.Place. The frames are saved
// under artifactDir as whichkey-POSITION.
func TestWhichKeyOpensInEachPosition(t *testing.T) {
	const cols, rows = 100, 40
	for _, pos := range []string{"bottom-right", "bottom-left", "top-right", "top-left", "center"} {
		t.Run(pos, func(t *testing.T) {
			base := t.TempDir()
			writeConfig(t, base, fmt.Sprintf("[appearance]\nwhichkey_position = %q\n", pos))
			term := startIn(t, base, startOpts{cols: cols, rows: rows})
			waitBoot(t, term)
			if err := term.SendKeys(tuitest.Ctrl('b')); err != nil {
				t.Fatal(err)
			}
			waitText(t, term, "the which-key panel", "Toggle tiling")
			if err := term.WaitStable(uiTimeout); err != nil {
				t.Fatalf("the screen never settled: %v", err)
			}
			saveArtifact(t, term, artifactDir(t), "whichkey-"+pos)
			s := term.Screen()
			assertNoLineOverflow(t, s, cols, pos)
			x0, y0, x1, y1, ok := panelBox(s, "Toggle tiling")
			if !ok {
				t.Fatalf("no which-key box around Toggle tiling\n%s", term.Snapshot())
			}
			left, right := x0 == 2, x1 == cols-2
			top, bottom := y0 <= 2, y1 >= rows-3
			var good bool
			switch pos {
			case "bottom-right":
				good = right && bottom
			case "bottom-left":
				good = left && bottom
			case "top-right":
				good = right && top
			case "top-left":
				good = left && top
			case "center":
				good = abs((x0+x1)/2-cols/2) <= 1 && abs((y0+y1)/2-rows/2) <= 1
			}
			if !good || y0 < 0 || y1 > rows {
				t.Errorf("%s: the panel is at %d..%d x %d..%d on a %dx%d screen\n%s", pos, x0, x1-1, y0, y1-1, cols, rows, term.Snapshot())
			}
		})
	}
}
