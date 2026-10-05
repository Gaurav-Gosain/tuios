package tuie2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// Resizing panes under shared borders. With shared borders on, the tilers keep
// one cell between two neighbours and draw the divider in it. Every resize has
// to keep that cell: a resize that puts two panes edge to edge leaves the
// divider nowhere to go, and it disappears from the screen.
//
// The report was that resizing in the master-stack layout with shared borders
// was broken, and that shared borders did not work in the scrolling layout.
// In master-stack every key resize closed the cell, so the divider vanished;
// with appearance.gap at 2 the panes overlapped. A mouse drag closed it too,
// for as long as the button was held. A press on the cell where the divider
// meets the stack's own divider grabbed nothing when the master was on the
// right or at the bottom. The scrolling layout drew a box round every column
// whatever the setting said.
//
// Each test reads the rectangles from list-windows and the divider cells from
// the frame, and saves its frames under artifactDir.

// isBoxRune reports whether r is a box-drawing character, which is what every
// divider glyph is.
func isBoxRune(r rune) bool { return r >= 0x2500 && r <= 0x257f }

// dividerCell is one cell a divider has to be drawn in.
type dividerCell struct {
	x, y     int
	vertical bool
}

// neighbourFaults checks the space between every two panes that face each
// other: it has to be gap cells, and no two panes may overlap. It returns the
// cell in the middle of each division, for the frame check, and fails when
// there is no division at all, so a layout with one pane cannot pass.
func neighbourFaults(rects []winRect, gap int) ([]dividerCell, error) {
	var cells []dividerCell
	for i, a := range rects {
		for j, b := range rects {
			if i == j {
				continue
			}
			if i < j && geomOverlap(a, b) {
				return nil, fmt.Errorf("pane %d and pane %d overlap", i, j)
			}
			if lo, hi := max(a.Y, b.Y), min(a.Y+a.Height, b.Y+b.Height); hi > lo && b.X > a.X {
				if d := b.X - (a.X + a.Width); d >= -1 && d <= gap+3 {
					if d != gap {
						return nil, fmt.Errorf("pane %d starts %d columns after pane %d ends, want %d", j, d, i, gap)
					}
					cells = append(cells, dividerCell{x: a.X + a.Width, y: (lo + hi) / 2, vertical: true})
				}
			}
			if lo, hi := max(a.X, b.X), min(a.X+a.Width, b.X+b.Width); hi > lo && b.Y > a.Y {
				if d := b.Y - (a.Y + a.Height); d >= -1 && d <= gap+3 {
					if d != gap {
						return nil, fmt.Errorf("pane %d starts %d rows after pane %d ends, want %d", j, d, i, gap)
					}
					cells = append(cells, dividerCell{x: (lo + hi) / 2, y: a.Y + a.Height})
				}
			}
		}
	}
	if len(cells) == 0 {
		return nil, fmt.Errorf("no two panes face each other")
	}
	return cells, nil
}

// dividersDrawn reports the first division cell on screen that holds no
// divider.
func dividersDrawn(s tuitest.Screen, cells []dividerCell) error {
	cols, rows := s.Size()
	for _, c := range cells {
		if c.x < 0 || c.x >= cols || c.y < 0 || c.y >= rows {
			continue
		}
		if r := s.Cell(c.x, c.y).Rune; !isBoxRune(r) {
			kind := "horizontal"
			if c.vertical {
				kind = "vertical"
			}
			return fmt.Errorf("the %s divider cell (%d,%d) holds %q, not a divider", kind, c.x, c.y, r)
		}
	}
	return nil
}

// waitForDividers waits for the session's panes to keep gap cells between
// every two neighbours, and for the frame to draw a divider in each of those
// gaps. It returns the rectangles.
func waitForDividers(t *testing.T, term *tuitest.Terminal, base, session string, n, gap int, what string) []winRect {
	t.Helper()
	var cells []dividerCell
	rects := waitForShape(t, base, session, n, what, func(rects []winRect) error {
		var err error
		cells, err = neighbourFaults(rects, gap)
		return err
	})
	var last error
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		last = dividersDrawn(s, cells)
		return last == nil
	}, 5*time.Second); err != nil {
		t.Fatalf("%s: %v\n%s\n%s", what, last, describeRects(rects), term.Snapshot())
	}
	return rects
}

// TestSharedBorderKeyResizeKeepsTheDivider resizes master-stack panes with the
// keys in every arrangement, and checks the divider survives each press.
//
// NEGATIVE CONTROL: fails on main (486ce455) at "after the width keys" in
// every case. The split mover put the panes on both sides of a division on
// the same column, so the cell for the divider was gone: "pane 1 starts 0
// columns after pane 0 ends, want 1". With gap = 2 the panes overlapped.
func TestSharedBorderKeyResizeKeepsTheDivider(t *testing.T) {
	for _, tc := range []struct {
		name, appearance string
		panes, gap       int
		percent          bool
	}{
		{"master-left", "", 3, 1, true},
		{"master-right", "master_position = \"right\"\n", 3, 1, false},
		{"master-top", "master_position = \"top\"\n", 3, 1, false},
		{"master-bottom", "master_position = \"bottom\"\n", 3, 1, false},
		{"grid", "", 4, 1, false},
		{"two-masters", "master_count = 2\nmaster_grid = false\n", 4, 1, false},
		{"gap-2", "gap = 2\n", 3, 2, false},
		{"dock-top", "dockbar_position = \"top\"\n", 3, 1, false},
		{"compact-dock", "dock_compact = true\n", 3, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			const name = "sbkeys"
			term := masterSession(t, base, name, "shared_borders = true\n"+tc.appearance, tc.panes)
			dir := artifactDir(t)
			before := waitForDividers(t, term, base, name, tc.panes, tc.gap, "the opening layout")
			saveArtifact(t, term, dir, "before")

			// The focus is on the last pane. Its width keys move a vertical
			// division and its height keys a horizontal one, in every
			// arrangement here.
			sendKeys(t, term, ">", ">")
			after := waitForDividers(t, term, base, name, tc.panes, tc.gap, "after the width keys")
			if sameGeometry(before, after) {
				t.Fatalf("the width keys resized nothing\n%s", describeRects(after))
			}
			saveArtifact(t, term, dir, "after-width-keys")

			sendKeys(t, term, "}", "}")
			taller := waitForDividers(t, term, base, name, tc.panes, tc.gap, "after the height keys")
			if sameGeometry(after, taller) {
				t.Fatalf("the height keys resized nothing\n%s", describeRects(taller))
			}
			saveArtifact(t, term, dir, "after-height-keys")

			if tc.percent {
				// The layout prefix, then 7: the focused pane to 70% of the width.
				sendKeys(t, term, tuitest.Ctrl('b'), "L", "7")
				rects := waitForDividers(t, term, base, name, tc.panes, tc.gap, "after the width percentage")
				if w := rects[len(rects)-1].Width; w != 84 {
					t.Fatalf("the focused pane is %d columns wide after the 70%% key, want 84\n%s", w, describeRects(rects))
				}
				saveArtifact(t, term, dir, "after-width-percent")
			}
		})
	}
}

// TestSharedBorderDragKeepsTheDivider drags the master-stack divider with the
// mouse. The divider has to follow the pointer while the button is held, and
// end on the column the pointer let go of.
//
// NEGATIVE CONTROL: fails on main (486ce455) at "the divider during the
// drag": while the button was held the master and the stack stood edge to
// edge and no divider was drawn between them.
func TestSharedBorderDragKeepsTheDivider(t *testing.T) {
	base := t.TempDir()
	const name = "sbdrag"
	term := masterSession(t, base, name, "shared_borders = true\n", 3)
	dir := artifactDir(t)
	rects := waitForDividers(t, term, base, name, 3, 1, "the opening layout")
	saveArtifact(t, term, dir, "before")
	master := rects[0]
	from := master.X + master.Width
	to := from - 12
	row := master.Y + 5

	mousePress(t, term, from, row, tuitest.MouseLeft, 0)
	for col := from - 1; col >= to; col-- {
		mouseMotion(t, term, col, row, tuitest.MouseLeft, 0)
	}
	// Every row of the master's height on the new divider column holds a
	// divider glyph, the junction with the stack's own division included.
	var last string
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		for y := master.Y; y < master.Y+master.Height; y++ {
			if r := s.Cell(to, y).Rune; !isBoxRune(r) {
				last = fmt.Sprintf("cell (%d,%d) holds %q", to, y, r)
				return false
			}
		}
		return true
	}, 5*time.Second); err != nil {
		saveArtifact(t, term, dir, "during-drag")
		t.Fatalf("the divider during the drag is not on column %d: %s\n%s", to, last, term.Snapshot())
	}
	saveArtifact(t, term, dir, "during-drag")
	mouseRelease(t, term, to, row, tuitest.MouseLeft, 0)

	after := waitForDividers(t, term, base, name, 3, 1, "after the drag")
	if got := after[0].X + after[0].Width; got != to {
		t.Fatalf("the divider is on column %d after the drag, want %d where the pointer let go\n%s", got, to, describeRects(after))
	}
	saveArtifact(t, term, dir, "after-drag")
}

// TestSharedBorderDragFromAJunction presses on the cell where the master's
// divider meets the stack's own, with the master on the right and at the
// bottom. No pane's right or bottom edge reaches that cell, only the master's
// left or top edge, and the press has to grab the divider all the same.
//
// NEGATIVE CONTROL: fails on main (486ce455) at "after the drag" in both
// cases: the press grabbed nothing and the master did not move.
func TestSharedBorderDragFromAJunction(t *testing.T) {
	for _, tc := range []struct {
		name, position string
		vertical       bool
	}{
		{"master-right", "right", true},
		{"master-bottom", "bottom", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			const name = "sbjunction"
			term := masterSession(t, base, name, "shared_borders = true\nmaster_position = \""+tc.position+"\"\n", 3)
			dir := artifactDir(t)
			rects := waitForDividers(t, term, base, name, 3, 1, "the opening layout")
			saveArtifact(t, term, dir, "before")
			master, stack := rects[0], rects[1]
			var x, y, toX, toY int
			if tc.vertical {
				x, y = master.X-1, stack.Y+stack.Height
				toX, toY = x+8, y
			} else {
				x, y = stack.X+stack.Width, master.Y-1
				toX, toY = x, y+4
			}
			mouseDrag(t, term, x, y, toX, toY, tuitest.MouseLeft, 0)

			waitForShape(t, base, name, 3, "after the drag", func(rects []winRect) error {
				m := rects[0]
				if tc.vertical && m.X != toX+1 {
					return fmt.Errorf("the master starts on column %d, want %d after a drag from the junction (%d,%d) to column %d", m.X, toX+1, x, y, toX)
				}
				if !tc.vertical && m.Y != toY+1 {
					return fmt.Errorf("the master starts on row %d, want %d after a drag from the junction (%d,%d) to row %d", m.Y, toY+1, x, y, toY)
				}
				return nil
			})
			waitForDividers(t, term, base, name, 3, 1, "the dividers after the drag")
			saveArtifact(t, term, dir, "after-drag")
		})
	}
}

// scrollingSession is masterSession for the scrolling layout.
func scrollingSession(t *testing.T, base, name, appearance string, n int) *tuitest.Terminal {
	t.Helper()
	return layoutSession(t, base, name, "scrolling", appearance, n)
}

// layoutSession is masterSession for any layout.
func layoutSession(t *testing.T, base, name, layout, appearance string, n int) *tuitest.Terminal {
	t.Helper()
	writeConfig(t, base, "[startup]\nopen_default_window = true\ntiled = true\nlayout = \""+layout+"\"\n"+
		"[appearance]\n"+appearance)
	if out, err := tuiosCLI(t, base, "new", "-d", name); err != nil {
		t.Fatalf("create the detached session: %v\n%s", err, out)
	}
	term := attachIn(t, base, name, startOpts{cols: 120, rows: 40})
	waitForSettledGeometryIn(t, base, name, 1)
	for i := 2; i <= n; i++ {
		if out, err := tuiosCLI(t, base, "run-command", "-s", name, "NewWindow"); err != nil {
			t.Fatalf("open window %d: %v\n%s", i, err, out)
		}
		waitForSettledGeometryIn(t, base, name, i)
	}
	return term
}

// TestScrollingSharedBorders turns shared borders on in the scrolling layout.
// The columns have to stand one cell apart with one divider between them, as
// tiled panes do, and draw no border of their own. Dragging the divider sets
// the width of the column on its left.
//
// NEGATIVE CONTROL: fails on main (486ce455) at "the opening layout": the
// columns stood edge to edge, each in its own box, so the setting did nothing
// in this layout.
func TestScrollingSharedBorders(t *testing.T) {
	base := t.TempDir()
	const name = "sbniri"
	term := scrollingSession(t, base, name, "shared_borders = true\nscroll_column_width = 40\n", 3)
	dir := artifactDir(t)
	rects := waitForDividers(t, term, base, name, 3, 1, "the opening layout")
	saveArtifact(t, term, dir, "columns")
	// A pane that draws its own border opens its top row with a corner.
	if line := term.Screen().Line(rects[0].Y); strings.ContainsAny(line, "╭╮") {
		t.Fatalf("the columns still draw their own borders:\n%s", term.Snapshot())
	}

	first := rects[0]
	from := first.X + first.Width
	to := from - 10
	mouseDrag(t, term, from, first.Y+5, to, first.Y+5, tuitest.MouseLeft, 0)
	after := waitForDividers(t, term, base, name, 3, 1, "after the divider drag")
	if w := after[0].Width; w != first.Width-10 {
		t.Fatalf("the first column is %d columns wide after the drag, want %d\n%s", w, first.Width-10, describeRects(after))
	}
	saveArtifact(t, term, dir, "after-drag")

	// The width is the column's own now, kept through a retile.
	if err := term.Resize(130, 42); err != nil {
		t.Fatalf("resize the client: %v", err)
	}
	resized := waitForDividers(t, term, base, name, 3, 1, "after the client grew")
	if w := resized[0].Width; w != first.Width-10 {
		t.Fatalf("the first column is %d columns wide after the client grew, want %d\n%s", w, first.Width-10, describeRects(resized))
	}
	saveArtifact(t, term, dir, "after-resize")
}

// shellSize asks the shell in one window for the size of its terminal, and
// returns what stty reports. It is the size the program in the pane draws
// into, which is what a missed resize leaves wrong.
func shellSize(t *testing.T, base, session, window string) (rows, cols int) {
	t.Helper()
	file := filepath.Join(base, "stty-"+strings.ReplaceAll(window, "/", "_"))
	_ = os.Remove(file)
	if out, err := tuiosCLI(t, base, "send-keys", "-s", session, "-w", window, "--literal", "stty size > "+file+"\r"); err != nil {
		t.Fatalf("ask window %s for its size: %v\n%s", window, err, out)
	}
	deadline := time.Now().Add(shellTimeout)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(file); err == nil {
			if _, err := fmt.Sscanf(string(b), "%d %d", &rows, &cols); err == nil {
				return rows, cols
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("window %s never reported its size", window)
	return 0, 0
}

// TestScrollingDividerDragResizesEveryStackedWindow drags the divider of a
// column that holds two stacked windows. Both windows take the new width, so
// both shells have to be told it, not only the one level with the pointer.
//
// NEGATIVE CONTROL: with ScrollingResizeColumnVisual putting only the dragged
// window in PendingResizes, the other stacked window's shell keeps the old
// width: "the lower window's shell is 48 columns wide, want 38".
func TestScrollingDividerDragResizesEveryStackedWindow(t *testing.T) {
	base := t.TempDir()
	const name = "sbstack"
	term := scrollingSession(t, base, name, "shared_borders = true\nscroll_column_width = 40\n"+
		"[keybindings.window_management]\nscroll_consume = [\"Y\"]\n", 3)
	dir := artifactDir(t)
	// Focus the first column and pull the second column's window into it.
	sendKeys(t, term, "h", "h")
	time.Sleep(500 * time.Millisecond)
	sendKeys(t, term, "Y")
	rects := waitForShape(t, base, name, 3, "the stacked column", func(rects []winRect) error {
		n := 0
		for _, r := range rects {
			if r.X == rects[0].X {
				n++
			}
		}
		if n != 2 {
			return fmt.Errorf("%d windows share the first column, want 2", n)
		}
		return nil
	})
	waitForDividers(t, term, base, name, 3, 1, "the stacked column")
	saveArtifact(t, term, dir, "stacked")
	var upper, lower winRect
	for _, r := range rects {
		if r.X != rects[0].X {
			continue
		}
		if upper.ID == "" || r.Y < upper.Y {
			upper, lower = r, upper
		} else {
			lower = r
		}
	}

	from := upper.X + upper.Width
	to := from - 10
	mouseDrag(t, term, from, upper.Y+3, to, upper.Y+3, tuitest.MouseLeft, 0)
	waitForShape(t, base, name, 3, "after the drag", func(rects []winRect) error {
		for _, r := range rects {
			if (r.ID == upper.ID || r.ID == lower.ID) && r.Width != upper.Width-10 {
				return fmt.Errorf("window %s is %d columns wide, want %d", r.ID, r.Width, upper.Width-10)
			}
		}
		return nil
	})
	saveArtifact(t, term, dir, "after-drag")
	for _, w := range []struct {
		which string
		r     winRect
	}{{"upper", upper}, {"lower", lower}} {
		if _, cols := shellSize(t, base, name, w.r.ID); cols != upper.Width-10 {
			t.Fatalf("the %s window's shell is %d columns wide, want %d\n%s", w.which, cols, upper.Width-10, term.Snapshot())
		}
	}
}

// TestScrollingDividerDragAtTheStripEnd drags a divider one cell at a time
// with the strip scrolled to its right end. The column has to end exactly
// where the pointer let go.
//
// NEGATIVE CONTROL: with ScrollingResizeColumnVisual clamping the viewport on
// every motion and the width measured from the column's current edge, the
// strip slides left under the pointer and the width runs away: "the column is
// 45 columns wide after the drag, want 60".
func TestScrollingDividerDragAtTheStripEnd(t *testing.T) {
	base := t.TempDir()
	const name = "sbstripend"
	term := scrollingSession(t, base, name, "shared_borders = true\n", 3)
	dir := artifactDir(t)
	rects := waitForDividers(t, term, base, name, 3, 1, "the opening layout")
	saveArtifact(t, term, dir, "before")
	// The last column is focused, so the strip is at its right end and the
	// column before it ends at the divider on screen.
	var col winRect
	for _, r := range rects {
		if r.X+r.Width > 0 && r.X+r.Width < 119 && (col.ID == "" || r.X > col.X) {
			col = r
		}
	}
	if col.ID == "" {
		t.Fatalf("no divider on screen\n%s", describeRects(rects))
	}
	from := col.X + col.Width
	to := from - 6
	mousePress(t, term, from, 10, tuitest.MouseLeft, 0)
	for x := from - 1; x >= to; x-- {
		mouseMotion(t, term, x, 10, tuitest.MouseLeft, 0)
	}
	var last string
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		r := s.Cell(to, 10).Rune
		last = string(r)
		return isBoxRune(r)
	}, 5*time.Second); err != nil {
		saveArtifact(t, term, dir, "during-drag")
		t.Fatalf("the divider is not under the pointer at column %d during the drag: the cell holds %q\n%s", to, last, term.Snapshot())
	}
	saveArtifact(t, term, dir, "during-drag")
	mouseRelease(t, term, to, 10, tuitest.MouseLeft, 0)
	after := waitForDividers(t, term, base, name, 3, 1, "after the drag")
	for _, r := range after {
		if r.ID == col.ID && r.Width != col.Width-6 {
			t.Fatalf("the column is %d columns wide after the drag, want %d\n%s", r.Width, col.Width-6, describeRects(after))
		}
	}
	saveArtifact(t, term, dir, "after-drag")
}

// TestSharedBorderPressInsideAZoomedPane drags across the cell where the
// first divider is, while a zoomed pane covers it. The divider is hidden, so
// the press belongs to the zoomed pane and must resize nothing.
//
// It runs in BSP, where a resize is written into the tree at once. A
// master-stack resize under a zoom is never recorded as a ratio, so the
// retile at the end of the zoom would hide a wrong grab.
//
// The positive half is TestSharedBorderDragKeepsTheDivider: the same drag
// with no zoom moves the divider.
//
// NEGATIVE CONTROL: with the paneOver check cut from armTiledBorderResize,
// the press grabs the hidden divider and the first pane is narrower after
// the zoom ends: "the panes moved under the zoomed pane".
func TestSharedBorderPressInsideAZoomedPane(t *testing.T) {
	base := t.TempDir()
	const name = "sbzoom"
	term := layoutSession(t, base, name, "bsp", "shared_borders = true\nzoom_size = 100\n", 3)
	dir := artifactDir(t)
	before := waitForDividers(t, term, base, name, 3, 1, "the opening layout")
	if out, err := tuiosCLI(t, base, "run-command", "-s", name, "ToggleZoom"); err != nil {
		t.Fatalf("zoom: %v\n%s", err, out)
	}
	waitForShape(t, base, name, 3, "the zoomed pane", func(rects []winRect) error {
		for _, r := range rects {
			if r.Width == 120 {
				return nil
			}
		}
		return fmt.Errorf("no pane is zoomed")
	})
	time.Sleep(500 * time.Millisecond)
	saveArtifact(t, term, dir, "zoomed")
	first := before[0]
	from := first.X + first.Width
	mouseDrag(t, term, from, first.Y+5, from-10, first.Y+5, tuitest.MouseLeft, 0)
	time.Sleep(time.Second)
	if out, err := tuiosCLI(t, base, "run-command", "-s", name, "ToggleZoom"); err != nil {
		t.Fatalf("unzoom: %v\n%s", err, out)
	}
	after := waitForDividers(t, term, base, name, 3, 1, "after the zoom ended")
	saveArtifact(t, term, dir, "unzoomed")
	if !sameGeometry(before, after) {
		t.Fatalf("the panes moved under the zoomed pane\nbefore:\n%safter:\n%s", describeRects(before), describeRects(after))
	}
}

// TestScrollingDividerClickKeepsAProportionalColumn clicks a strip divider
// without moving the pointer. A click resizes nothing, so the column keeps
// its width as a share of the screen and follows the client's width.
//
// The positive half is the end of TestScrollingSharedBorders: after a real
// drag the column keeps its width in cells through the same resize.
//
// NEGATIVE CONTROL: with the width check cut from the scrolling capture in
// handleMouseRelease, the click records the width as fixed: "the first
// column is 48 columns wide after the client grew, want 60".
func TestScrollingDividerClickKeepsAProportionalColumn(t *testing.T) {
	base := t.TempDir()
	const name = "sbclick"
	term := scrollingSession(t, base, name, "shared_borders = true\nscroll_column_width = 40\n", 3)
	dir := artifactDir(t)
	rects := waitForDividers(t, term, base, name, 3, 1, "the opening layout")
	first := rects[0]
	mouseClick(t, term, first.X+first.Width, first.Y+5, tuitest.MouseLeft, 0)
	time.Sleep(time.Second)
	saveArtifact(t, term, dir, "after-click")
	if err := term.Resize(150, 40); err != nil {
		t.Fatalf("resize the client: %v", err)
	}
	waitForShape(t, base, name, 3, "after the client grew", func(rects []winRect) error {
		for _, r := range rects {
			if r.ID == first.ID && r.Width != 60 {
				return fmt.Errorf("the first column is %d columns wide after the client grew, want 60", r.Width)
			}
		}
		return nil
	})
	saveArtifact(t, term, dir, "after-resize")
}
