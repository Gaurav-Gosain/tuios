package tuie2e

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// appearance.outer_gap on the real screen: the pane borders sit that many
// cells in from the screen edge, the dock's rule and the rail, and every mouse
// target on a pane (a divider, a window control, a title bar) moves with them.
//
// dockChromeRows is config.DockHeight: the dock row and the rule under it.
const dockChromeRows = 2

// gapCase is one layout of the chrome around the panes.
type gapCase struct {
	gap        int
	dock, rail string // dock "top" or "bottom", rail "left", "right" or "hidden"
}

func (c gapCase) name() string {
	return fmt.Sprintf("gap%d-dock-%s-rail-%s", c.gap, c.dock, c.rail)
}

func (c gapCase) config() string {
	var b strings.Builder
	fmt.Fprintf(&b, "[appearance]\nouter_gap = %d\ndockbar_position = %q\nclick_to_type = 'double'\n", c.gap, c.dock)
	if c.rail == "hidden" {
		b.WriteString("[appearance.sidebar]\nenabled = false\n")
	} else {
		fmt.Fprintf(&b, "[appearance.sidebar]\nenabled = true\nposition = %q\n", c.rail)
	}
	return b.String()
}

// region is the pane region the case asks for, worked out from the chrome:
// the first and last column and row a pane border may sit on.
func (c gapCase) region(cols, rows int) (x0, y0, x1, y1 int) {
	top, bottom, left, right := 0, 0, 0, 0
	if c.dock == "top" {
		top = dockChromeRows
	} else {
		bottom = dockChromeRows
	}
	switch c.rail {
	case "left":
		left = shippedRail(cols)
	case "right":
		right = shippedRail(cols)
	}
	return left + c.gap, top + c.gap, cols - 1 - right - c.gap, rows - 1 - bottom - c.gap
}

// startGapCase starts a standalone tuios with the case's config and n windows.
func startGapCase(t *testing.T, c gapCase, cols, rows, n int, tiled bool) *tuitest.Terminal {
	t.Helper()
	base := t.TempDir()
	useShippedLooks(base)
	writeConfig(t, base, c.config())
	term := startIn(t, base, startOpts{cols: cols, rows: rows, shippedLooks: true})
	waitBoot(t, term)
	for range n {
		newWindow(t, term)
	}
	if tiled {
		enableTiling(t, term)
	} else {
		disableTiling(t, term)
	}
	return term
}

var gapRoundCorners = map[string]bool{"╭": true, "╮": true, "╰": true, "╯": true}

// gapCornerBox is the smallest box holding every rounded corner outside the
// rail's columns: the outline of the panes.
func gapCornerBox(s tuitest.Screen, railX0, railX1 int) (x0, y0, x1, y1 int, ok bool) {
	cols, rows := s.Size()
	x0, y0, x1, y1 = cols, rows, -1, -1
	for y := range rows {
		for x := range cols {
			if x >= railX0 && x < railX1 {
				continue
			}
			if gapRoundCorners[s.Cell(x, y).Content] {
				x0, y0, x1, y1 = min(x0, x), min(y0, y), max(x1, x), max(y1, y)
			}
		}
	}
	return x0, y0, x1, y1, x1 >= 0
}

// railBand is the columns the rail takes, or an empty band.
func (c gapCase) railBand(cols int) (int, int) {
	switch c.rail {
	case "left":
		return 0, shippedRail(cols)
	case "right":
		return cols - shippedRail(cols), cols
	}
	return 0, 0
}

// waitGapBox waits until the panes' outline is the box asked for.
func waitGapBox(t *testing.T, term *tuitest.Terminal, c gapCase, want [4]int, what string) {
	t.Helper()
	cols, _ := term.Screen().Size()
	rx0, rx1 := c.railBand(cols)
	var got [4]int
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		x0, y0, x1, y1, ok := gapCornerBox(s, rx0, rx1)
		got = [4]int{x0, y0, x1, y1}
		return ok && got == want
	}, uiTimeout); err != nil {
		t.Fatalf("%s: the panes' outline is %v (x0 y0 x1 y1), want %v\n%s", what, got, want, term.Snapshot())
	}
}

// TestOuterGapTiledFrames tiles two panes under each placement of the dock and
// the rail and checks the outline of the panes against the region the gap asks
// for. At gap 0 the borders touch the screen edge, the dock's rule and the
// rail. The frames are saved under TUIOS_E2E_FRAMES.
func TestOuterGapTiledFrames(t *testing.T) {
	const cols, rows = 100, 24
	cases := []gapCase{
		{0, "top", "right"}, {3, "top", "right"},
		{0, "bottom", "hidden"}, {3, "bottom", "hidden"},
		{1, "top", "left"}, {3, "bottom", "left"},
	}
	for _, c := range cases {
		t.Run(c.name(), func(t *testing.T) {
			term := startGapCase(t, c, cols, rows, 2, true)
			x0, y0, x1, y1 := c.region(cols, rows)
			waitGapBox(t, term, c, [4]int{x0, y0, x1, y1}, "tiled")
			if err := term.WaitStable(time.Second); err != nil {
				t.Fatalf("the screen never settled: %v", err)
			}
			saveArtifact(t, term, artifactDir(t), "tiled")
			t.Logf("tiled, %s:\n%s", c.name(), term.Snapshot())
		})
	}
}

// TestOuterGapSharedBordersTouchTheEdge: with shared borders the panes give up
// their outer borders, and at gap 0 their text starts in the first column and
// the row under the dock's rule, as it always has. A gap moves the text in by
// the gap.
func TestOuterGapSharedBordersTouchTheEdge(t *testing.T) {
	const cols, rows = 100, 24
	for _, gap := range []int{0, 2} {
		t.Run(fmt.Sprintf("gap%d", gap), func(t *testing.T) {
			base := t.TempDir()
			useShippedLooks(base)
			writeConfig(t, base, fmt.Sprintf("[appearance]\nouter_gap = %d\nshared_borders = true\ndockbar_position = 'top'\n[appearance.sidebar]\nenabled = false\n", gap))
			term := startIn(t, base, startOpts{cols: cols, rows: rows, shippedLooks: true})
			waitBoot(t, term)
			newWindow(t, term)
			newWindow(t, term)
			enableTiling(t, term)
			// The prompt of the left pane is the first thing in its grid.
			wantRow, wantCol := dockChromeRows+gap, gap
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				return strings.HasPrefix(s.Cell(wantCol, wantRow).Content, "$")
			}, uiTimeout); err != nil {
				t.Fatalf("the left pane's prompt is not at column %d row %d\n%s", wantCol, wantRow, term.Snapshot())
			}
			if gap > 0 {
				s := term.Screen()
				for x := range gap {
					if c := s.Cell(x, wantRow).Content; c != "" && c != " " {
						t.Fatalf("column %d of the gap holds %q\n%s", x, c, term.Snapshot())
					}
				}
			}
			saveArtifact(t, term, artifactDir(t), "shared")
			t.Logf("shared borders, gap %d:\n%s", gap, term.Snapshot())
		})
	}
}

// gapTopCorners returns the columns of the top-left corners on a row, left to
// right, outside the rail.
func gapTopCorners(s tuitest.Screen, row, railX0, railX1 int) []int {
	cols, _ := s.Size()
	var out []int
	for x := range cols {
		if x >= railX0 && x < railX1 {
			continue
		}
		if s.Cell(x, row).Content == "╭" {
			out = append(out, x)
		}
	}
	return out
}

// TestOuterGapMouseFollows drags the divider between two tiled panes, presses
// a window's close control, and drags a floating window into the top-left
// snap zone, at gap 0 and gap 3. Every one of those targets is found on the
// screen, so if the hit tests stayed where the gap-less layout had them the
// press lands on the wrong cell and nothing happens.
func TestOuterGapMouseFollows(t *testing.T) {
	const cols, rows = 120, 30
	for _, c := range []gapCase{{0, "top", "right"}, {3, "top", "right"}, {3, "bottom", "hidden"}} {
		t.Run(c.name(), func(t *testing.T) {
			term := startGapCase(t, c, cols, rows, 2, true)
			x0, y0, x1, y1 := c.region(cols, rows)
			waitGapBox(t, term, c, [4]int{x0, y0, x1, y1}, "tiled")
			rx0, rx1 := c.railBand(cols)
			dir := artifactDir(t)

			// The divider: the right border of the left pane, one column
			// before the right pane's corner.
			starts := gapTopCorners(term.Screen(), y0, rx0, rx1)
			if len(starts) != 2 || starts[0] != x0 {
				t.Fatalf("want two panes side by side from column %d, found corners at %v\n%s", x0, starts, term.Snapshot())
			}
			divider := starts[1] - 1
			mid := (y0 + y1) / 2
			mouseDrag(t, term, divider, mid, divider-10, mid, tuitest.MouseLeft, 0)
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				now := gapTopCorners(s, y0, rx0, rx1)
				return len(now) == 2 && now[1] <= starts[1]-8
			}, uiTimeout); err != nil {
				t.Fatalf("dragging the divider at column %d did not move it\n%s", divider, term.Snapshot())
			}
			waitGapBox(t, term, c, [4]int{x0, y0, x1, y1}, "after the divider drag")
			saveArtifact(t, term, dir, "divider")

			// The close control of the right pane: the first dot after its corner.
			right := gapTopCorners(term.Screen(), y0, rx0, rx1)[1]
			dots := findDots(term.Screen())
			closeX := -1
			for _, d := range dots {
				if d.row == y0 && d.start > right && d.start < right+4 {
					closeX = d.at[ctlClose]
				}
			}
			if closeX < 0 {
				t.Fatalf("no window controls on the right pane's title bar at row %d\n%s", y0, term.Snapshot())
			}
			mouseClick(t, term, closeX, y0, tuitest.MouseLeft, 0)
			waitWindowCount(t, term, 1, "after pressing the close control")
			waitGapBox(t, term, c, [4]int{x0, y0, x1, y1}, "one pane left")

			// Floating: the remaining window, dragged by its title bar into
			// the top-left corner, snaps to the top-left quarter of the pane
			// region.
			disableTiling(t, term)
			wx, wy, _, _, ok := gapCornerBox(term.Screen(), rx0, rx1)
			if !ok {
				t.Fatalf("no window on screen after tiling off\n%s", term.Snapshot())
			}
			grabX := wx + 10
			if cell := term.Screen().Cell(grabX, wy).Content; cell != "─" {
				t.Fatalf("expected the title bar's rule at column %d row %d, found %q\n%s", grabX, wy, cell, term.Snapshot())
			}
			mouseDrag(t, term, grabX, wy, 0, 0, tuitest.MouseLeft, 0)
			w := (x1 - x0 + 1) / 2
			h := (y1 - y0 + 1) / 2
			waitGapBox(t, term, c, [4]int{x0, y0, x0 + w - 1, y0 + h - 1}, "after the snap to the top-left")
			saveArtifact(t, term, dir, "float-snap")
			t.Logf("after the drags, %s:\n%s", c.name(), term.Snapshot())
		})
	}
}

// TestOuterGapTwoClientsOfDifferentSizes attaches two clients of different
// sizes to one daemon session and sets the gap live from outside. Both lay the
// panes out in the session's size less the gap, so both outlines are the same.
func TestOuterGapTwoClientsOfDifferentSizes(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	writeConfig(t, base, "[appearance]\ndockbar_position = 'top'\n[appearance.sidebar]\nenabled = false\n")
	for _, args := range [][]string{
		{"new", "gap", "--detach"},
		{"new-window", "second", "-s", "gap", "--no-focus"},
	} {
		if o, err := tuiosCLI(t, base, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, o)
		}
	}
	big := attachIn(t, base, "gap", startOpts{shippedLooks: true, cols: 120, rows: 36})
	small := attachIn(t, base, "gap", startOpts{shippedLooks: true, cols: 90, rows: 28})
	enableTiling(t, small)
	c := gapCase{gap: 0, dock: "top", rail: "hidden"}
	x0, y0, x1, y1 := c.region(90, 28)
	for name, term := range map[string]*tuitest.Terminal{"big": big, "small": small} {
		waitGapBox(t, term, c, [4]int{x0, y0, x1, y1}, name+" at gap 0")
	}

	setLive(t, base, "appearance.outer_gap", "2")
	c.gap = 2
	x0, y0, x1, y1 = c.region(90, 28)
	for name, term := range map[string]*tuitest.Terminal{"big": big, "small": small} {
		waitGapBox(t, term, c, [4]int{x0, y0, x1, y1}, name+" at gap 2")
		saveArtifact(t, term, artifactDir(t), name)
		t.Logf("%s client at gap 2:\n%s", name, term.Snapshot())
	}
}
