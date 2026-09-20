package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// Zoom used to be two things at once: the pane takes the whole region, and it
// takes it in one frame. appearance.zoom_size and appearance.zoom_animation
// separate them.

// zoomPeekOS is four panes in a two by two split, which is the layout that makes
// the anchoring visible: each pane has a neighbour on exactly two sides.
func zoomPeekOS(t *testing.T) (*OS, []*terminal.Window) {
	t.Helper()
	prev := config.Global
	t.Cleanup(func() { config.Global = prev })

	var wins []*terminal.Window
	for i := range 4 {
		w := newTestWindow(t, string(rune('a'+i))+"0000000000000000000000000000000", 40, 20)
		w.Workspace = 1
		wins = append(wins, w)
	}
	// Top left, top right, bottom left, bottom right of a 120x40 region.
	wins[0].X, wins[0].Y, wins[0].Width, wins[0].Height = 0, 0, 60, 20
	wins[1].X, wins[1].Y, wins[1].Width, wins[1].Height = 60, 0, 60, 20
	wins[2].X, wins[2].Y, wins[2].Width, wins[2].Height = 0, 20, 60, 20
	wins[3].X, wins[3].Y, wins[3].Width, wins[3].Height = 60, 20, 60, 20

	m := &OS{
		Settings:         config.Global,
		Windows:          wins,
		FocusedWindow:    0,
		WorkspaceFocus:   map[int]int{},
		NumWorkspaces:    9,
		CurrentWorkspace: 1,
		Width:            120,
		Height:           40,
		PendingResizes:   map[string][2]int{},
	}
	return m, wins
}

// TestAFullZoomIsUnchanged pins that the default takes the whole region, so
// nothing moved for anyone who sets none of this.
func TestAFullZoomIsUnchanged(t *testing.T) {
	m, _ := zoomPeekOS(t)
	m.Settings.ZoomSize = config.ZoomSizeDefault

	x, y, w, h := m.zoomRectFor(m.Windows[3])

	if x != m.GetLeftMargin() || y != m.GetTopMargin() {
		t.Errorf("a full zoom starts at (%d,%d), want the region's own corner (%d,%d)",
			x, y, m.GetLeftMargin(), m.GetTopMargin())
	}
	if w != m.GetContentWidth() || h != m.GetUsableHeight() {
		t.Errorf("a full zoom is %dx%d, want the whole region %dx%d",
			w, h, m.GetContentWidth(), m.GetUsableHeight())
	}
}

// TestAPeekZoomLeavesTheLayoutShowing pins the size, and that the box is pulled
// toward the pane's own corner so the neighbours that show are the ones it has.
func TestAPeekZoomLeavesTheLayoutShowing(t *testing.T) {
	m, wins := zoomPeekOS(t)
	m.Settings.ZoomSize = 90

	region := struct{ x, y, w, h int }{m.GetLeftMargin(), m.GetTopMargin(), m.GetContentWidth(), m.GetUsableHeight()}

	for _, c := range []struct {
		name          string
		win           *terminal.Window
		wantLeftGap   bool
		wantTopGap    bool
		wantRightGap  bool
		wantBottomGap bool
	}{
		{"top left", wins[0], false, false, true, true},
		{"top right", wins[1], true, false, false, true},
		{"bottom left", wins[2], false, true, true, false},
		{"bottom right", wins[3], true, true, false, false},
	} {
		x, y, w, h := m.zoomRectFor(c.win)

		if w >= region.w || h >= region.h {
			t.Fatalf("%s: the box is %dx%d in a %dx%d region, so nothing could peek",
				c.name, w, h, region.w, region.h)
		}
		if got := x > region.x; got != c.wantLeftGap {
			t.Errorf("%s: gap on the left = %v, want %v (box at x=%d, region from %d)",
				c.name, got, c.wantLeftGap, x, region.x)
		}
		if got := y > region.y; got != c.wantTopGap {
			t.Errorf("%s: gap above = %v, want %v (box at y=%d, region from %d)",
				c.name, got, c.wantTopGap, y, region.y)
		}
		if got := x+w < region.x+region.w; got != c.wantRightGap {
			t.Errorf("%s: gap on the right = %v, want %v", c.name, got, c.wantRightGap)
		}
		if got := y+h < region.y+region.h; got != c.wantBottomGap {
			t.Errorf("%s: gap below = %v, want %v", c.name, got, c.wantBottomGap)
		}
		// And it never leaves the region.
		if x < region.x || y < region.y || x+w > region.x+region.w || y+h > region.y+region.h {
			t.Errorf("%s: the box (%d,%d %dx%d) is outside the region (%d,%d %dx%d)",
				c.name, x, y, w, h, region.x, region.y, region.w, region.h)
		}
	}
}

// TestThePeekBoxIsAnchoredOnTheTileNotTheBox pins which rectangle the anchor is
// read from.
//
// The box is recomputed on every sync and every resize, and by then the pane's
// own rectangle is the box. Reading the anchor from that says the pane came
// from wherever the box currently is, so a box computed at one region size and
// recomputed at another walks away from the corner the pane actually lives in.
// The pre-zoom rectangle is the one that still says where the pane belongs.
//
// The pane below is put somewhere its current rectangle and its pre-zoom
// rectangle disagree, which is the only arrangement that can tell the two
// apart.
func TestThePeekBoxIsAnchoredOnTheTileNotTheBox(t *testing.T) {
	m, wins := zoomPeekOS(t)
	m.Settings.ZoomSize = 80

	target := wins[3]
	// Its tile is the bottom right, and that is where it should be anchored.
	target.PreZoomX, target.PreZoomY = 60, 20
	target.PreZoomWidth, target.PreZoomHeight = 60, 20
	target.Zoomed = true
	// Its current rectangle says the top left, which is what the anchor must
	// not read.
	target.X, target.Y, target.Width, target.Height = 0, 0, 20, 10

	x, y, w, h := m.zoomRectFor(target)

	if x+w < m.GetLeftMargin()+m.GetContentWidth() {
		t.Errorf("the box ends at x=%d, short of the right edge %d: it was anchored on the pane's current rectangle",
			x+w, m.GetLeftMargin()+m.GetContentWidth())
	}
	if y+h < m.GetTopMargin()+m.GetUsableHeight() {
		t.Errorf("the box ends at y=%d, short of the bottom edge %d: it was anchored on the pane's current rectangle",
			y+h, m.GetTopMargin()+m.GetUsableHeight())
	}
}

// TestAPeekZoomDoesNotHideTheLayout pins the render's side of it: the panes
// underneath are drawn, or the zoomed pane grows over a blank screen and the
// smaller box shows nothing worth seeing.
func TestAPeekZoomDoesNotHideTheLayout(t *testing.T) {
	m, wins := zoomPeekOS(t)
	m.Settings.ZoomAnimation = false

	m.Settings.ZoomSize = config.ZoomSizeDefault
	m.FocusedWindow = 0
	m.ToggleZoom()
	if !m.zoomCoversRegion(wins[0]) {
		t.Error("a full zoom does not cover the region, so the layout is drawn under it for nothing")
	}
	m.ToggleZoom()

	m.Settings.ZoomSize = 80
	m.ToggleZoom()
	if m.zoomCoversRegion(wins[0]) {
		t.Error("a peek zoom reports that it covers the region, so the layout it leaves room for is not drawn")
	}
}

// TestAFullHeightPaneKeepsItsHeight is the layout from the report: one pane down
// the left, two stacked on the right.
//
// The left pane spans the region top to bottom, so it has no neighbour above or
// below it. Shrinking it vertically anyway opened a band at each end onto the
// only thing past its ends, which is a few rows of the right column's title
// bars: the least useful rows in the frame, and they read as litter scattered
// around the zoom rather than as a peek at anything.
func TestAFullHeightPaneKeepsItsHeight(t *testing.T) {
	m, wins := zoomPeekOS(t)
	m.Settings.ZoomSize = 90

	// Left half full height, and two stacked on the right.
	left := wins[0]
	left.X, left.Y, left.Width, left.Height = 0, 0, 60, m.GetUsableHeight()
	wins[1].X, wins[1].Y, wins[1].Width, wins[1].Height = 60, 0, 60, 19
	wins[2].X, wins[2].Y, wins[2].Width, wins[2].Height = 60, 19, 60, 19
	m.Windows = wins[:3]

	x, y, w, h := m.zoomRectFor(left)

	if h != m.GetUsableHeight() {
		t.Errorf("the box is %d rows in a %d row region: it gave up height it had no neighbour for",
			h, m.GetUsableHeight())
	}
	if y != m.GetTopMargin() {
		t.Errorf("the box starts at y=%d, want the region's top %d", y, m.GetTopMargin())
	}
	if w >= m.GetContentWidth() {
		t.Errorf("the box is %d columns in a %d column region: it gave up no width, so nothing peeks",
			w, m.GetContentWidth())
	}
	// And the width it gave up is on the right, which is the side the two
	// neighbours are on.
	if x != m.GetLeftMargin() {
		t.Errorf("the box starts at x=%d, want the region's left %d: the band is on the wrong side",
			x, m.GetLeftMargin())
	}
}

// TestALonePaneZoomsWhole pins that a pane with no neighbours at all takes the
// region. A box floating in the middle of nothing is not a peek at anything.
func TestALonePaneZoomsWhole(t *testing.T) {
	m, wins := zoomPeekOS(t)
	m.Settings.ZoomSize = 80
	only := wins[0]
	only.X, only.Y = 0, 0
	only.Width, only.Height = m.GetContentWidth(), m.GetUsableHeight()
	m.Windows = wins[:1]

	x, y, w, h := m.zoomRectFor(only)

	if w != m.GetContentWidth() || h != m.GetUsableHeight() {
		t.Errorf("the only pane zoomed to %dx%d, want the whole region %dx%d",
			w, h, m.GetContentWidth(), m.GetUsableHeight())
	}
	if x != m.GetLeftMargin() || y != m.GetTopMargin() {
		t.Errorf("the only pane zoomed to (%d,%d), want the region's corner (%d,%d)",
			x, y, m.GetLeftMargin(), m.GetTopMargin())
	}
}
