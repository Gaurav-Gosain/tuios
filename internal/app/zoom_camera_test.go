package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/layout"
)

// Zooming a pane to part of the screen is a camera move over the layout, not a
// resize of the one pane.
//
// The layout is laid out on a canvas larger than the screen and the screen is
// panned to the pane you zoomed. Every pane keeps its place and its proportions
// relative to every other; the zoomed one is the one the camera is on, and its
// neighbours run off the edges because the canvas is bigger than the window you
// are looking through.

// TestTheCameraLiftsTheZoomedPaneToItsShare pins the scale: whatever the pane
// started as, it ends up taking about the configured percentage of the screen.
//
// The setting says how much of the screen the pane you asked for should take,
// and that is the same promise whether it started as a third of the layout or
// as half of it.
func TestTheCameraLiftsTheZoomedPaneToItsShare(t *testing.T) {
	m, wins := zoomPeekOS(t)
	m.Settings.ZoomSize = 90
	regionW, regionH := m.GetContentWidth(), m.GetUsableHeight()

	for _, tile := range []layout.Rect{
		{X: 0, Y: 0, W: regionW / 2, H: regionH / 2},
		{X: regionW / 2, Y: 0, W: regionW / 2, H: regionH / 3},
		{X: 0, Y: 0, W: regionW / 3, H: regionH},
	} {
		c := m.zoomCanvasFor(wins[0], tile)
		if !c.on {
			t.Fatalf("tile %v got no camera, so this checks nothing", tile)
		}
		got := c.apply(tile)
		// One axis reaches the share and the other is no bigger, because the
		// scale is uniform and takes the tighter of the two.
		wantW, wantH := regionW*90/100, regionH*90/100
		if got.W > regionW || got.H > regionH {
			t.Errorf("tile %v was lifted to %dx%d, past the %dx%d screen", tile, got.W, got.H, regionW, regionH)
		}
		if got.W < wantW && got.H < wantH {
			t.Errorf("tile %v was lifted to %dx%d, short of %dx%d on both axes",
				tile, got.W, got.H, wantW, wantH)
		}
	}
}

// TestTheCameraKeepsTheLayoutTogether pins that every pane is moved by the same
// transform, so two panes that shared an edge still share it and none of them
// overlaps another.
//
// The layout is the one from the report: a full height pane down the left and
// two stacked on the right, with the top right one zoomed.
func TestTheCameraKeepsTheLayoutTogether(t *testing.T) {
	m, wins := zoomPeekOS(t)
	m.Settings.ZoomSize = 90
	regionW, regionH := m.GetContentWidth(), m.GetUsableHeight()

	left := layout.Rect{X: 0, Y: 0, W: regionW / 2, H: regionH}
	topRight := layout.Rect{X: regionW / 2, Y: 0, W: regionW - regionW/2, H: regionH / 2}
	bottomRight := layout.Rect{X: regionW / 2, Y: regionH / 2, W: regionW - regionW/2, H: regionH - regionH/2}

	c := m.zoomCanvasFor(wins[0], topRight)
	if !c.on {
		t.Fatal("no camera, so this checks nothing")
	}
	l, tr, br := c.apply(left), c.apply(topRight), c.apply(bottomRight)

	if l.X+l.W != tr.X {
		t.Errorf("the left pane ends at %d and the right one starts at %d: the seam opened",
			l.X+l.W, tr.X)
	}
	if tr.X != br.X || tr.W != br.W {
		t.Errorf("the two right panes no longer share a column: %v and %v", tr, br)
	}
	if tr.Y+tr.H != br.Y {
		t.Errorf("the top right pane ends at %d and the bottom one starts at %d: the seam opened",
			tr.Y+tr.H, br.Y)
	}
}

// TestTheCameraShowsTheNeighboursItHas pins where the peek falls: on the sides
// the zoomed pane actually has neighbours on, because the camera stops at the
// canvas edge rather than running off it.
//
// Zoom the top right pane and what comes in from the edges is the pane to its
// left and the pane below it. Nothing comes in from above or from the right,
// because there is no canvas there to come in.
func TestTheCameraShowsTheNeighboursItHas(t *testing.T) {
	m, wins := zoomPeekOS(t)
	m.Settings.ZoomSize = 90
	regionW, regionH := m.GetContentWidth(), m.GetUsableHeight()
	originX, originY := m.GetLeftMargin(), m.GetTopMargin()

	left := layout.Rect{X: 0, Y: 0, W: regionW / 2, H: regionH}
	topRight := layout.Rect{X: regionW / 2, Y: 0, W: regionW - regionW/2, H: regionH / 2}
	bottomRight := layout.Rect{X: regionW / 2, Y: regionH / 2, W: regionW - regionW/2, H: regionH - regionH/2}

	c := m.zoomCanvasFor(wins[0], topRight)
	l, tr, br := c.apply(left), c.apply(topRight), c.apply(bottomRight)

	// The zoomed pane is flush against the top and the right, which is the
	// corner of the canvas it sits in.
	if tr.Y != originY {
		t.Errorf("the zoomed pane starts at y=%d, want the screen's top %d: the camera ran off the canvas",
			tr.Y, originY)
	}
	if tr.X+tr.W < originX+regionW {
		t.Errorf("the zoomed pane ends at x=%d, short of the right edge %d",
			tr.X+tr.W, originX+regionW)
	}
	// And the two neighbours reach onto the screen from the sides they are on.
	if l.X+l.W <= originX {
		t.Error("the left neighbour is entirely off the screen, so nothing peeks from the left")
	}
	if br.Y >= originY+regionH {
		t.Error("the pane below is entirely off the screen, so nothing peeks from below")
	}
}

// TestAFullZoomTakesNoCamera pins the default: at 100 percent the pane takes the
// screen and the rest is not drawn, which is what zoom has always been.
func TestAFullZoomTakesNoCamera(t *testing.T) {
	m, wins := zoomPeekOS(t)
	m.Settings.ZoomSize = 100
	tile := layout.Rect{X: 0, Y: 0, W: m.GetContentWidth() / 2, H: m.GetUsableHeight()}

	if c := m.zoomCanvasFor(wins[0], tile); c.on {
		t.Error("a full zoom built a camera")
	}
}

// TestALonePaneTakesNoCamera pins that a pane which is the whole layout is left
// alone. Lifting the camera would frame it against empty canvas.
func TestALonePaneTakesNoCamera(t *testing.T) {
	m, wins := zoomPeekOS(t)
	m.Settings.ZoomSize = 80
	tile := layout.Rect{X: 0, Y: 0, W: m.GetContentWidth(), H: m.GetUsableHeight()}

	if c := m.zoomCanvasFor(wins[0], tile); c.on {
		t.Error("the only pane on the workspace built a camera, framing it against nothing")
	}
}

// TestAFullHeightPaneIsLiftedOnWidthAlone pins the per-axis scale on the layout
// from the report: a pane down the left, two stacked on the right.
//
// The left pane already spans the region top to bottom. Lifting it vertically
// as well would push its own top and bottom off the screen, which loses the
// pane you asked to see, so that axis is left alone and the whole lift goes
// into width.
func TestAFullHeightPaneIsLiftedOnWidthAlone(t *testing.T) {
	m, wins := zoomPeekOS(t)
	m.Settings.ZoomSize = 90
	regionW, regionH := m.GetContentWidth(), m.GetUsableHeight()

	left := layout.Rect{X: m.GetLeftMargin(), Y: m.GetTopMargin(), W: regionW / 2, H: regionH}
	c := m.zoomCanvasFor(wins[0], left)
	if !c.on {
		t.Fatal("a full height pane beside two others got no camera")
	}
	got := c.apply(left)

	if got.H != regionH {
		t.Errorf("the pane is %d rows, want the region's %d: it was lifted on an axis it already spanned",
			got.H, regionH)
	}
	if got.Y != m.GetTopMargin() {
		t.Errorf("the pane starts at y=%d, want the screen's top %d", got.Y, m.GetTopMargin())
	}
	if want := regionW * 90 / 100; got.W < want-1 || got.W > want+1 {
		t.Errorf("the pane is %d columns, want about %d", got.W, want)
	}
}

// TestACameraZoomDrawsTheLayoutUnderIt pins the render's side: the panes around
// the zoomed one are drawn, or the camera is pointed at a blank screen.
func TestACameraZoomDrawsTheLayoutUnderIt(t *testing.T) {
	m, wins := zoomPeekOS(t)
	m.Settings.ZoomAnimation = false
	m.AutoTiling = true
	m.UseBSPLayout = true
	m.FocusedWindow = 0
	for _, w := range wins {
		m.AddWindowToBSPTree(w)
	}

	m.Settings.ZoomSize = 100
	m.ToggleZoom()
	if !m.zoomCoversRegion(wins[0]) {
		t.Error("a full zoom does not cover the region, so the layout is drawn under it for nothing")
	}
	m.ToggleZoom()

	m.Settings.ZoomSize = 85
	m.ToggleZoom()
	if !m.zoomUsesLayout(wins[0]) {
		t.Fatal("the zoom was not left to the layout, so this checks nothing")
	}
	if m.zoomCoversRegion(wins[0]) {
		t.Error("a camera zoom reports that it covers the region, so the layout around it is not drawn")
	}
}
