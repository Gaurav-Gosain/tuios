package overlay

// Side is the side of its anchor that a placed box opens on.
type Side uint8

// The sides a box can open on. Below and Above place on the vertical axis,
// Right and Left on the horizontal one.
const (
	SideBelow Side = iota
	SideAbove
	SideRight
	SideLeft
)

// Opposite is the side across the anchor from s.
func (s Side) Opposite() Side {
	return s ^ 1
}

// vertical reports whether s places on the vertical axis.
func (s Side) vertical() bool {
	return s <= SideAbove
}

// Align is where a box sits along the anchor's other axis: for Below and
// Above the columns, for Right and Left the rows.
type Align uint8

// The alignments. Start puts the box's first column (or row) on the anchor's
// first, End puts its last on the anchor's last, Center centres it on the
// anchor.
const (
	AlignStart Align = iota
	AlignCenter
	AlignEnd
)

// Placement is how Place puts a box against its anchor.
type Placement struct {
	// Side is the side the box opens on when it fits there.
	Side Side
	// Align is where the box sits along the other axis.
	Align Align
	// Gap is the number of cells between the anchor and the box. -1 puts the
	// box's first row (or column) on the anchor's last, so the box covers
	// that cell: a menu at the pointer has its corner on the pointer cell.
	Gap int
	// FlipAlign turns Start into End (and End into Start) when the box would
	// run past the screen along the other axis and the flipped alignment
	// fits, instead of shifting the box along that axis. It is for a menu at
	// the pointer, which keeps the pointer on one of its corners rather than
	// in the middle of a row the person did not aim at.
	FlipAlign bool
}

// PointRect is the one cell (x, y) as an anchor.
func PointRect(x, y int) Rect {
	return Rect{X0: x, Y0: y, X1: x + 1, Y1: y + 1}
}

// Place returns the top left cell of a box w by h placed against anchor
// inside screen, and the side it opened on. Every coordinate is in the same
// frame, the screen's or a canvas's.
//
// The rule:
//
//  1. Open on p.Side when the box fits between the anchor and the screen's
//     edge on that side.
//  2. Otherwise open on the opposite side when it fits there, or when that
//     side has more room. With neither fitting and equal room, keep p.Side.
//  3. Along the other axis, sit at p.Align. With p.FlipAlign, a box that runs
//     past the screen at Start or End takes the other one if that fits.
//  4. Shift the box onto the screen on both axes. A box larger than the
//     screen on an axis starts at the screen's first cell on it, so its title
//     and first rows are the part that shows.
//
// The side returned is the one from steps 1 and 2, before the shift: a
// submenu that flips, or a caller that draws a pointer toward the anchor,
// reads it.
//
// The edge cases:
//
//	case                              result
//	fits on p.Side                    p.Side, aligned, not shifted
//	fits only across                  the opposite side
//	fits on neither side              the roomier side, then shifted onto the screen
//	taller (or wider) than screen     starts at screen.Y0 (or X0); side is the roomier
//	anchor wider than the screen      aligned to the anchor, then shifted onto the screen
//	zero or negative w or h           treated as 0: placed at the anchor's edge
//	anchor off the screen             placed as if on it, then shifted to the nearest edge
//	screen narrowed by rail or dock   pass the usable rect as screen; the box stays inside it
//	empty screen                      the screen's top left cell
//
// Place never resizes the box. A caller that can shrink it (a list that
// shows fewer rows) measures the room on each side first, sizes the box to
// it, then calls Place.
func Place(anchor Rect, w, h int, screen Rect, p Placement) (x, y int, side Side) {
	w, h = max(w, 0), max(h, 0)
	side = p.Side
	if !fits(anchor, w, h, screen, side, p.Gap) {
		o := side.Opposite()
		if fits(anchor, w, h, screen, o, p.Gap) || room(anchor, screen, o, p.Gap) > room(anchor, screen, side, p.Gap) {
			side = o
		}
	}
	main := sidePos(anchor, w, h, side, p.Gap)
	if side.vertical() {
		x = alignPos(anchor.X0, anchor.X1, w, screen.X0, screen.X1, p.Align, p.FlipAlign)
		y = main
	} else {
		x = main
		y = alignPos(anchor.Y0, anchor.Y1, h, screen.Y0, screen.Y1, p.Align, p.FlipAlign)
	}
	return shiftOnto(x, w, screen.X0, screen.X1), shiftOnto(y, h, screen.Y0, screen.Y1), side
}

// Clamp shifts a box w by h at (x, y) onto screen by Place's step 4. It is
// for a box with no anchor, such as one centred on the screen.
func Clamp(x, y, w, h int, screen Rect) (int, int) {
	return shiftOnto(x, max(w, 0), screen.X0, screen.X1), shiftOnto(y, max(h, 0), screen.Y0, screen.Y1)
}

// sidePos is the box's first row (Below, Above) or column (Right, Left) when
// it opens on side.
func sidePos(a Rect, w, h int, side Side, gap int) int {
	switch side {
	case SideBelow:
		return a.Y1 + gap
	case SideAbove:
		return a.Y0 - gap - h
	case SideRight:
		return a.X1 + gap
	default:
		return a.X0 - gap - w
	}
}

// fits reports whether the box lies inside screen on the side's axis when it
// opens on side.
func fits(a Rect, w, h int, s Rect, side Side, gap int) bool {
	pos := sidePos(a, w, h, side, gap)
	if side.vertical() {
		return pos >= s.Y0 && pos+h <= s.Y1
	}
	return pos >= s.X0 && pos+w <= s.X1
}

// room is the number of cells between the anchor (and its gap) and the
// screen's edge on side.
func room(a Rect, s Rect, side Side, gap int) int {
	switch side {
	case SideBelow:
		return s.Y1 - (a.Y1 + gap)
	case SideAbove:
		return (a.Y0 - gap) - s.Y0
	case SideRight:
		return s.X1 - (a.X1 + gap)
	default:
		return (a.X0 - gap) - s.X0
	}
}

// alignPos is the box's first cell along the other axis: n cells long against
// an anchor from a0 to a1, on a screen from s0 to s1.
func alignPos(a0, a1, n, s0, s1 int, al Align, flip bool) int {
	start, end := a0, a1-n
	switch al {
	case AlignCenter:
		return a0 + (a1-a0-n)/2
	case AlignEnd:
		if flip && end < s0 && start+n <= s1 {
			return start
		}
		return end
	default:
		if flip && start+n > s1 && end >= s0 {
			return end
		}
		return start
	}
}

// shiftOnto moves a run n cells long that starts at pos inside [s0, s1). A
// run longer than the screen starts at s0.
func shiftOnto(pos, n, s0, s1 int) int {
	return max(min(pos, s1-n), s0)
}
