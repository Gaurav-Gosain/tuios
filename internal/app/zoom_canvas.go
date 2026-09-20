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

// zoomCanvas is the transform from tiled coordinates to screen coordinates
// while a pane is zoomed to part of the screen. The zero value is the identity,
// which is what every layout that is not in this state uses.
type zoomCanvas struct {
	// on is false for a layout drawn at its own size, which is a zoom of the
	// whole screen, no zoom at all, or a zoom the layout cannot do this to.
	on bool
	// scaleX and scaleY are how much bigger the canvas is than the screen on
	// each axis.
	//
	// One factor per axis rather than one for both. A pane that already spans
	// the region on an axis cannot be lifted on it: scaling the left half of a
	// split vertically would push its own top and bottom off the screen, which
	// loses the pane you asked to see. Each axis is floored at 1, so an axis
	// with nothing to gain is left alone and the lift happens on the axis that
	// has room for it.
	//
	// The canvas is stretched differently in the two directions when that
	// happens. A pane is a grid of cells rather than a picture, so there is no
	// aspect to preserve: what the setting promises is that the pane you zoomed
	// takes that share of each axis, and that is what this gives it.
	scaleX, scaleY float64
	// pan is the canvas cell the screen's top left corner is over.
	panX, panY int
	// origin is the screen cell the content region starts at, added back after
	// the pan so the result is in absolute screen coordinates.
	originX, originY int
}

// apply maps one pane's tiled rectangle onto the screen.
//
// A rectangle entirely off the screen is returned as it falls out, off the
// screen: the render clips panes to the content region, so a pane the camera
// has left behind costs a layer nobody sees rather than a special case here.
func (c zoomCanvas) apply(r layout.Rect) layout.Rect {
	if !c.on {
		return r
	}
	// Scaled about the region's origin, so a pane at the origin stays there and
	// the whole canvas grows down and to the right from it.
	x := c.scaleX_(r.X-c.originX) - c.panX + c.originX
	y := c.scaleY_(r.Y-c.originY) - c.panY + c.originY
	// The far edge is scaled rather than the width, so two panes that shared an
	// edge still share it: scaling each width on its own leaves a seam wherever
	// the rounding went different ways.
	w := c.scaleX_(r.X-c.originX+r.W) - c.scaleX_(r.X-c.originX)
	h := c.scaleY_(r.Y-c.originY+r.H) - c.scaleY_(r.Y-c.originY)
	return layout.Rect{X: x, Y: y, W: max(w, 1), H: max(h, 1)}
}

// scaleX_ and scaleY_ scale one coordinate measured from the region's origin.
func (c zoomCanvas) scaleX_(v int) int { return int(float64(v)*c.scaleX + 0.5) }
func (c zoomCanvas) scaleY_(v int) int { return int(float64(v)*c.scaleY + 0.5) }

// zoomCanvasFor builds the transform for a workspace whose zoomed pane occupies
// tile in tiled coordinates, or the identity when there is nothing to do.
//
// tile is the rectangle the tiler gave that pane before any of this, which is
// the only rectangle that says where the pane belongs. The pane's own is the
// scaled one by the time this runs again.
func (m *OS) zoomCanvasFor(zoomed *terminal.Window, tile layout.Rect) zoomCanvas {
	pct := m.Settings.GetZoomSize()
	if zoomed == nil || pct >= 100 || tile.W <= 0 || tile.H <= 0 {
		return zoomCanvas{}
	}
	regionW, regionH := m.GetContentWidth(), m.GetUsableHeight()
	if regionW <= 0 || regionH <= 0 {
		return zoomCanvas{}
	}
	// A pane that is the whole layout has nothing around it to show, so lifting
	// the camera would frame it against empty canvas.
	if tile.W >= regionW && tile.H >= regionH {
		return zoomCanvas{}
	}

	// How much bigger the canvas has to be for this pane to reach its share of
	// each axis. Floored at 1: an axis the pane already spans has nothing to
	// gain, and scaling it there would push the pane's own ends off the screen.
	wantW := float64(regionW) * float64(pct) / 100
	wantH := float64(regionH) * float64(pct) / 100
	scaleX := max(wantW/float64(tile.W), 1)
	scaleY := max(wantH/float64(tile.H), 1)
	if scaleX <= 1 && scaleY <= 1 {
		// The pane already fills that much of both axes. Nothing to lift.
		return zoomCanvas{}
	}

	originX, originY := m.GetLeftMargin(), m.GetTopMargin()
	c := zoomCanvas{on: true, scaleX: scaleX, scaleY: scaleY, originX: originX, originY: originY}

	// Centre the screen on the pane, then hold it inside the canvas. The clamp
	// is what puts a corner pane in its corner: there is no canvas past the
	// edge to show, so the camera stops and the whole of the peek falls on the
	// sides the pane actually has neighbours on.
	tx, ty := tile.X-originX, tile.Y-originY
	paneW := c.scaleX_(tx+tile.W) - c.scaleX_(tx)
	paneH := c.scaleY_(ty+tile.H) - c.scaleY_(ty)
	c.panX = clampInt(c.scaleX_(tx)+paneW/2-regionW/2, 0, max(c.scaleX_(regionW)-regionW, 0))
	c.panY = clampInt(c.scaleY_(ty)+paneH/2-regionH/2, 0, max(c.scaleY_(regionH)-regionH, 0))
	return c
}
