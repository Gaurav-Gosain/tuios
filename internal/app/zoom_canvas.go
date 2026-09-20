package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// Zooming a pane to less than the whole screen is a camera move, not a resize.
//
// The first cut of appearance.zoom_size grew the one pane toward the screen and
// left the others at their tiles, so what showed around it was whatever happened
// to lie under the edges: on a full-height pane beside a stack of two, a few
// rows of somebody else's title bar at each end. It read as litter rather than
// as a view of anything.
//
// The layout is laid out on a canvas larger than the screen instead, and the
// screen is panned to the pane you zoomed. Every pane keeps its place and its
// proportions relative to every other; the zoomed one is simply the one the
// camera is on, and its neighbours run off the edges because the canvas is
// bigger than the window you are looking through it with. It is the scrolling
// layout's strip in two directions, which is what it was asked to be.
//
// The scale is whatever makes the zoomed pane reach zoom_size percent of the
// screen, so a small pane in a busy layout is lifted further than a big one:
// the setting says how much of the screen the pane you asked for should take,
// and that is the same promise whatever it started as.
//
// The tiler is run again in the larger box rather than its finished rectangles
// being stretched. Stretching them scaled the gaps between panes along with the
// panes, and a gap is not content: it is the one column a shared border is
// drawn in. At any zoom past about 1.5x that column became two, which drew fat
// dividers under BSP and, under master-stack, none at all, because the divider
// finder looks for neighbours exactly one gap apart. Laying out in the larger
// box keeps every gap the width the user asked for, so the dividers come out
// right without anything downstream knowing a camera is involved.

// zoomCanvas is the transform from the box a zoomed layout is computed in to
// the screen. The zero value is the identity, which is what every layout that
// is not in this state uses.
type zoomCanvas struct {
	// on is false for a layout drawn at its own size, which is a zoom of the
	// whole screen, no zoom at all, or a zoom the layout cannot do this to.
	on bool
	// bounds is the box the layout is computed in: the content region grown
	// until the zoomed pane's share of it is zoom_size percent of the screen.
	//
	// One factor per axis rather than one for both. A pane that already spans
	// the region on an axis cannot be lifted on it: growing the box vertically
	// for the left half of a split would push that pane's own top and bottom
	// off the screen, which loses the pane you asked to see. Each axis is
	// floored at the region's own size, so an axis with nothing to gain is left
	// alone and the lift happens on the axis that has room for it.
	//
	// The box is stretched differently in the two directions when that happens.
	// A pane is a grid of cells rather than a picture, so there is no aspect to
	// preserve: what the setting promises is that the pane you zoomed takes
	// that share of each axis, and that is what this gives it.
	bounds layout.Rect
	// panX and panY are how far the screen has been moved over that box.
	panX, panY int
}

// apply maps one rectangle from the enlarged box onto the screen.
//
// A pure translation, because the layout was computed at the box's own size.
// A rectangle that lands entirely off the screen is returned as it falls out:
// the render clips panes to the content region, so a pane the camera has left
// behind costs a layer nobody sees rather than a special case here.
func (c zoomCanvas) apply(r layout.Rect) layout.Rect {
	if !c.on {
		return r
	}
	r.X -= c.panX
	r.Y -= c.panY
	return r
}

// applySplit maps a divider from the enlarged box onto the screen, so the lines
// the tiler reserved between panes land between the same panes.
func (c zoomCanvas) applySplit(s layout.SplitLine) layout.SplitLine {
	if !c.on {
		return s
	}
	if s.Vertical {
		s.Pos -= c.panX
		s.From -= c.panY
		s.To -= c.panY
		return s
	}
	s.Pos -= c.panY
	s.From -= c.panX
	s.To -= c.panX
	return s
}

// zoomCanvasBounds is the box a zoomed workspace's layout should be computed
// in, given the rectangle the tiler gave the zoomed pane at the screen's own
// size. ok is false when there is nothing to do.
//
// tile is the rectangle the tiler chose before any of this, which is the only
// one that says how big a share of the layout the pane holds.
func (m *OS) zoomCanvasBounds(zoomed *terminal.Window, tile layout.Rect) (layout.Rect, bool) {
	pct := m.Settings.GetZoomSize()
	region := layout.Rect{
		X: m.GetLeftMargin(), Y: m.GetTopMargin(),
		W: m.GetContentWidth(), H: m.GetUsableHeight(),
	}
	if zoomed == nil || pct >= 100 || tile.W <= 0 || tile.H <= 0 || region.W <= 0 || region.H <= 0 {
		return region, false
	}
	// A pane that is the whole layout has nothing around it to show, so
	// growing the box would frame it against empty canvas.
	if tile.W >= region.W && tile.H >= region.H {
		return region, false
	}

	// How big the box has to be for this pane to reach its share of each axis.
	// Never smaller than the screen: an axis the pane already spans has nothing
	// to gain, and shrinking it there would push the pane's own ends off.
	wantW := float64(region.W) * float64(pct) / 100
	wantH := float64(region.H) * float64(pct) / 100
	boundsW := max(int(float64(region.W)*wantW/float64(tile.W)+0.5), region.W)
	boundsH := max(int(float64(region.H)*wantH/float64(tile.H)+0.5), region.H)
	if boundsW <= region.W && boundsH <= region.H {
		// Already at least that much of both axes. Nothing to lift.
		return region, false
	}
	return layout.Rect{X: region.X, Y: region.Y, W: boundsW, H: boundsH}, true
}

// zoomCanvasAt finishes the camera once the layout has been recomputed in the
// enlarged box and the zoomed pane's rectangle there is known.
//
// The screen is centred on that pane and then held inside the box. The clamp is
// what puts a corner pane in its corner: there is no box past the edge to show,
// so the camera stops and the whole of the peek falls on the sides the pane
// actually has neighbours on.
func (m *OS) zoomCanvasAt(bounds, zoomedRect layout.Rect) zoomCanvas {
	region := layout.Rect{
		X: m.GetLeftMargin(), Y: m.GetTopMargin(),
		W: m.GetContentWidth(), H: m.GetUsableHeight(),
	}
	c := zoomCanvas{on: true, bounds: bounds}
	c.panX = clampInt(zoomedRect.X+zoomedRect.W/2-region.W/2-region.X, 0, max(bounds.W-region.W, 0))
	c.panY = clampInt(zoomedRect.Y+zoomedRect.H/2-region.H/2-region.Y, 0, max(bounds.H-region.H, 0))
	return c
}
