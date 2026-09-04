package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// Mode switches and stacking order, driven through the real binary. Each test
// here failed on the tree before its fix; NEGATIVE_CONTROLS.md records the
// controls.

// fillPaneWith types a command into the focused pane that paints every row of
// it with one letter, so a later frame can say which pane is on top of a cell.
func fillPaneWith(t *testing.T, term *tuitest.Terminal, letter string) {
	t.Helper()
	line := strings.Repeat(letter, 100)
	enterTerminalMode(t, term)
	if err := term.SendKeys("yes "+line+" | head -60", tuitest.Enter); err != nil {
		t.Fatalf("type fill: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		_, rows := s.Size()
		n := 0
		for r := range rows {
			if strings.Contains(s.Line(r), strings.Repeat(letter, 20)) {
				n++
			}
		}
		return n >= 3
	}, shellTimeout); err != nil {
		t.Fatalf("pane never filled with %s: %v\n%s", letter, err, term.Snapshot())
	}
	leaveTerminalMode(t, term)
}

func cellAt(s tuitest.Screen, col, row int) rune {
	line := []rune(s.Line(row))
	if col >= 0 && col < len(line) {
		return line[col]
	}
	return ' '
}

// insideContent reports whether a cell is inside a pane's content, away from
// its border and from the last rows where the shell prompt sits.
func insideContent(r winRect, x, y int) bool {
	return x > r.X && x < r.X+r.Width-1 && y > r.Y && y < r.Y+r.Height-3
}

func covers(r winRect, x, y int) bool {
	return x >= r.X && x < r.X+r.Width && y >= r.Y && y < r.Y+r.Height
}

// runPaletteRow opens the command palette, filters to one row, runs it and
// waits for the text it announces.
func runPaletteRow(t *testing.T, term *tuitest.Terminal, query, row, announces string) {
	t.Helper()
	if err := term.SendKeys(tuitest.Ctrl('p')); err != nil {
		t.Fatal(err)
	}
	waitPaletteOpen(t, term, "for "+row)
	if err := term.SendKeys(query); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText(row, uiTimeout); err != nil {
		t.Fatalf("palette never filtered to %q: %v\n%s", row, err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText(announces, uiTimeout); err != nil {
		t.Fatalf("%q never produced %q: %v\n%s", row, announces, err, term.Snapshot())
	}
}

// TestClickingAPaneKeepsTheOthersInOrder is the report about z-order: three
// floating panes, and clicking the one at the back reshuffled the other two.
//
// A, B and C are painted with their own letter and dragged so that B and C
// overlap somewhere A does not reach. B is raised over C, then A is clicked.
// The cell where only B and C meet must still show B: the click raised A and
// nothing else.
func TestClickingAPaneKeepsTheOthersInOrder(t *testing.T) {
	term, base := start(t, startOpts{cols: 120, rows: 40, args: []string{"new", "zorder"}})
	waitBoot(t, term)
	for _, letter := range []string{"A", "B", "C"} {
		newWindow(t, term)
		fillPaneWith(t, term, letter)
	}
	rects := waitForSettledGeometry(t, base, 3)
	// The three panes open on one centred box. C, on top, goes down and to the
	// right; B, now on top of the box, goes left and down. Both drags raise the
	// pane they grab, so the stack ends A, C, B from the bottom.
	c := rects[2]
	mouseDrag(t, term, c.X+8, c.Y, c.X+18, c.Y+10, tuitest.MouseLeft, 0)
	mouseDrag(t, term, c.X+8, c.Y, c.X-22, c.Y+3, tuitest.MouseLeft, 0)
	rects = waitForSettledGeometry(t, base, 3)
	a, b, c := rects[0], rects[1], rects[2]

	bcX, bcY, aX, aY := -1, -1, -1, -1
	for y := range 40 {
		for x := range 120 {
			if bcX < 0 && insideContent(b, x, y) && insideContent(c, x, y) && !covers(a, x, y) {
				bcX, bcY = x, y
			}
			if aX < 0 && insideContent(a, x, y) && !covers(b, x, y) && !covers(c, x, y) {
				aX, aY = x, y
			}
		}
	}
	if bcX < 0 || aX < 0 {
		t.Fatalf("the drags did not produce the overlap the test needs: %v", rects)
	}
	if got := cellAt(term.Screen(), bcX, bcY); got != 'B' {
		t.Fatalf("B was raised last, so it should cover C at (%d,%d), got %q\n%s", bcX, bcY, got, term.Snapshot())
	}

	mouseClick(t, term, aX, aY, tuitest.MouseLeft, 0)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return cellAt(s, aX, aY) == 'A' && cellAt(s, bcX, bcY) != ' '
	}, uiTimeout); err != nil {
		t.Fatalf("the click on A did not settle: %v\n%s", err, term.Snapshot())
	}
	if got := cellAt(term.Screen(), bcX, bcY); got != 'B' {
		t.Errorf("clicking A put C over B: cell (%d,%d) shows %q, want B\n%s", bcX, bcY, got, term.Snapshot())
	}
}

// scrollingStrip starts a session under the scrolling layout with enough
// columns that the strip is longer than the screen and the focus is on its
// last column, so the first columns sit off the left edge.
func scrollingStrip(t *testing.T, panes int) (*tuitest.Terminal, string) {
	t.Helper()
	base := t.TempDir()
	writeConfig(t, base, "[startup]\nopen_default_window = true\ntiled = true\nlayout = \"scrolling\"\n")
	term := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"new", "strip"}})
	waitWindowCount(t, term, 1, "first column")
	for n := 2; n <= panes; n++ {
		if out, err := tuiosCLI(t, base, "run-command", "NewWindow"); err != nil {
			t.Fatalf("NewWindow: %v\n%s", err, out)
		}
		waitWindowCount(t, term, n, "another column")
	}
	rects := waitForSettledGeometry(t, base, panes)
	if len(offScreenRects(rects)) == 0 {
		t.Fatalf("no column is off screen, so there is nothing to bring back: %v", rects)
	}
	return term, base
}

func offScreenRects(rects []winRect) []winRect {
	var out []winRect
	for _, r := range rects {
		if r.X < 0 || r.X+r.Width > 120 {
			out = append(out, r)
		}
	}
	return out
}

// TestTilingOffBringsTheStripOnScreen is the niri report: turn tiling off
// with the strip scrolled to its last column, and the columns past the left
// edge stayed there, unreachable, with nothing left to scroll them back.
// Every door to the switch is driven: the key, the palette row, and the two
// tape commands the CLI and the set-layout verb use.
func TestTilingOffBringsTheStripOnScreen(t *testing.T) {
	for _, door := range []string{"key", "palette", "tape DisableTiling", "tape ToggleTiling"} {
		t.Run(door, func(t *testing.T) {
			term, base := scrollingStrip(t, 4)
			switch door {
			case "key":
				if err := term.SendKeys("t"); err != nil {
					t.Fatal(err)
				}
				if err := term.WaitForText("Tiling off", uiTimeout); err != nil {
					t.Fatalf("tiling never turned off: %v\n%s", err, term.Snapshot())
				}
			case "palette":
				runPaletteRow(t, term, "disable tiling", "Layout: disable tiling", "Tiling off")
			default:
				cmd := strings.TrimPrefix(door, "tape ")
				if out, err := tuiosCLI(t, base, "run-command", cmd); err != nil {
					t.Fatalf("%s: %v\n%s", cmd, err, out)
				}
			}
			after := waitForSettledGeometry(t, base, 4)
			if off := offScreenRects(after); len(off) > 0 {
				t.Errorf("%d pane(s) are still off screen after tiling was turned off: %v\n%s", len(off), off, term.Snapshot())
			}
		})
	}
}

// TestAFloatingPaneStaysUnderTheWhichKeyOverlay: with four tiled panes and a
// fifth floated, the float used to be drawn at a depth past the which-key
// overlay's, so pressing the prefix painted the bindings under the pane.
func TestAFloatingPaneStaysUnderTheWhichKeyOverlay(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[startup]\nopen_default_window = true\ntiled = true\nlayout = \"bsp\"\n")
	term := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"new", "whichkey"}})
	waitWindowCount(t, term, 1, "first pane")
	for range 4 {
		newWindow(t, term)
	}
	fillPaneWith(t, term, "F")
	runPaletteRow(t, term, "toggle floating", "Toggle floating", "Window: floating")
	rects := waitForSettledGeometry(t, base, 5)
	p := rects[4]
	// Into the bottom-right corner, where the which-key overlay opens.
	mouseDrag(t, term, p.X+8, p.Y, 119-p.Width+8, 37-p.Height, tuitest.MouseLeft, 0)
	rects = waitForSettledGeometry(t, base, 5)
	p = rects[4]
	if err := term.SendKeys(tuitest.Ctrl('b')); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("Toggle tiling", uiTimeout); err != nil {
		t.Fatalf("the which-key overlay never opened: %v\n%s", err, term.Snapshot())
	}
	// The overlay is taller than the float, so its rows run through the
	// float's box. None of those rows may still show the float's paint.
	s := term.Screen()
	for y := p.Y + 1; y < p.Y+p.Height-1; y++ {
		if strings.Contains(s.Line(y), "FFFF") {
			t.Fatalf("row %d still shows the floating pane through the which-key overlay\n%s", y, term.Snapshot())
		}
	}
}

// sharedBorderSession is two panes tiled with shared borders on, so each pane
// gives up its own border and the frame shows one divider between them. That
// is the state a tiling switch has to undo, and the one the other doors forgot.
func sharedBorderSession(t *testing.T, name string) (*tuitest.Terminal, string) {
	t.Helper()
	base := t.TempDir()
	writeConfig(t, base, "[startup]\nopen_default_window = true\ntiled = true\nlayout = \"bsp\"\n[appearance]\nshared_borders = true\n")
	term := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"new", name}})
	waitWindowCount(t, term, 1, "first pane")
	newWindow(t, term)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return len(paneStarts(s)) == 0
	}, uiTimeout); err != nil {
		t.Fatalf("the panes never gave up their borders: %v\n%s", err, term.Snapshot())
	}
	return term, base
}

// waitPaneCorners waits for the frame to show n panes drawing their own box.
func waitPaneCorners(t *testing.T, term *tuitest.Terminal, n int, what string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return len(paneStarts(s)) == n
	}, uiTimeout); err != nil {
		t.Errorf("%s: %d pane corners on screen, want %d: %v\n%s", what, len(paneStarts(term.Screen())), n, err, term.Snapshot())
	}
}

// TestTapeTilingCommandsSettleTheBorders: DisableTiling and ToggleTiling from
// a tape or the CLI used to flip the flag and nothing else, so the panes kept
// drawing no border of their own while the dividers between them went away.
func TestTapeTilingCommandsSettleTheBorders(t *testing.T) {
	term, base := sharedBorderSession(t, "tape")
	for _, step := range []struct {
		cmd     string
		corners int
	}{
		{"DisableTiling", 2},
		{"EnableTiling", 0},
		{"ToggleTiling", 2},
		{"ToggleTiling", 0},
	} {
		if out, err := tuiosCLI(t, base, "run-command", step.cmd); err != nil {
			t.Fatalf("%s: %v\n%s", step.cmd, err, out)
		}
		waitPaneCorners(t, term, step.corners, "after "+step.cmd)
	}
}

// TestAPeerSeesTilingTurnOff: tiling turned off on one client reaches the
// other as state. The second client used to keep its panes borderless, with
// no dividers left between them, until something else retiled.
//
// Turning tiling on is not held to the same standard here. A client push does
// not advance the daemon's version, so the peer never adopts the BSP tree and
// cannot draw dividers; it keeps its own borders around the shared
// rectangles. That is a separate gap, filed on its own.
func TestAPeerSeesTilingTurnOff(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[startup]\nopen_default_window = true\ntiled = true\nlayout = \"bsp\"\n[appearance]\nshared_borders = true\n")
	term := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"new", "peer"}})
	waitWindowCount(t, term, 1, "first pane")
	newWindow(t, term)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return len(paneStarts(s)) == 0
	}, uiTimeout); err != nil {
		t.Fatalf("the panes never gave up their borders: %v\n%s", err, term.Snapshot())
	}
	peer := attachIn(t, base, "peer", startOpts{cols: 120, rows: 40})
	if err := peer.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == 2 && len(paneStarts(s)) == 0
	}, uiTimeout); err != nil {
		t.Fatalf("the peer never showed the shared-border layout: %v\n%s", err, peer.Snapshot())
	}
	if err := term.SendKeys("t"); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("Tiling off", uiTimeout); err != nil {
		t.Fatalf("tiling never turned off: %v\n%s", err, term.Snapshot())
	}
	waitPaneCorners(t, term, 2, "the client that turned tiling off")
	waitPaneCorners(t, peer, 2, "the peer")
	// And it stays that way: a later push must not put the flags back.
	time.Sleep(time.Second)
	if n := len(paneStarts(peer.Screen())); n != 2 {
		t.Errorf("the peer went back to %d pane corners\n%s", n, peer.Snapshot())
	}
}
