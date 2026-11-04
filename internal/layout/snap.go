package layout

// Snap is where a floating pane goes when it is dropped at an edge of the
// region the panes are laid out in: a half, a quarter, or the whole region.
//
// The zones and the rectangles live together so the region a drop is read
// against and the region a snap fills cannot disagree. Every client that
// floats panes (the terminal client and the desktop app) reads them here.
type Snap int

const (
	// NoSnap is a drop away from every edge.
	NoSnap Snap = iota
	// SnapLeft is the left half.
	SnapLeft
	// SnapRight is the right half.
	SnapRight
	// SnapTopLeft is the top-left quarter.
	SnapTopLeft
	// SnapTopRight is the top-right quarter.
	SnapTopRight
	// SnapBottomLeft is the bottom-left quarter.
	SnapBottomLeft
	// SnapBottomRight is the bottom-right quarter.
	SnapBottomRight
	// SnapFullScreen is the whole region.
	SnapFullScreen
	// Unsnap is the centred box a snapped pane returns to.
	Unsnap
)

// SnapEdge is how near an edge of the region, in cells, a drop has to land
// to snap.
const SnapEdge = 5

// SnapZoneAt names the snap a pointer released at (x, y) asks for, or NoSnap.
// region is the box the panes are laid out in, in cells. A corner is a
// quarter, the top edge the whole region, and a side edge a half.
func SnapZoneAt(region Rect, x, y int) Snap {
	atLeft := x <= region.X+SnapEdge
	atRight := x >= region.X+region.W-SnapEdge
	atTop := y <= region.Y+SnapEdge
	atBottom := y >= region.Y+region.H-SnapEdge

	switch {
	case atTop && atLeft:
		return SnapTopLeft
	case atTop && atRight:
		return SnapTopRight
	case atBottom && atLeft:
		return SnapBottomLeft
	case atBottom && atRight:
		return SnapBottomRight
	case atTop:
		return SnapFullScreen
	case atLeft:
		return SnapLeft
	case atRight:
		return SnapRight
	}
	return NoSnap
}

// SnapBounds is the rectangle snap q fills in region. The halves split the
// width with the odd cell on the right, and the quarters split the height
// with the odd row at the bottom. NoSnap and Unsnap are the centred box half
// the region's size.
func SnapBounds(region Rect, q Snap) Rect {
	halfWidth := region.W / 2
	midX := region.X + halfWidth
	halfHeight := region.H / 2

	switch q {
	case SnapLeft:
		return Rect{X: region.X, Y: region.Y, W: halfWidth, H: region.H}
	case SnapRight:
		return Rect{X: midX, Y: region.Y, W: region.W - halfWidth, H: region.H}
	case SnapTopLeft:
		return Rect{X: region.X, Y: region.Y, W: halfWidth, H: halfHeight}
	case SnapTopRight:
		return Rect{X: midX, Y: region.Y, W: halfWidth, H: halfHeight}
	case SnapBottomLeft:
		return Rect{X: region.X, Y: halfHeight + region.Y, W: halfWidth, H: region.H - halfHeight}
	case SnapBottomRight:
		return Rect{X: midX, Y: halfHeight + region.Y, W: halfWidth, H: region.H - halfHeight}
	case SnapFullScreen:
		return region
	default:
		return Rect{X: region.X + region.W/4, Y: region.H/4 + region.Y, W: halfWidth, H: halfHeight}
	}
}
