package layout

import "slices"

// Side is a direction to step focus in.
type Side int

const (
	SideLeft Side = iota
	SideRight
	SideUp
	SideDown
)

// ParseSide reads "left", "right", "up" or "down".
func ParseSide(s string) (Side, bool) {
	switch s {
	case "left":
		return SideLeft, true
	case "right":
		return SideRight, true
	case "up":
		return SideUp, true
	case "down":
		return SideDown, true
	}
	return 0, false
}

// minNeighbourOverlap is how many cells two rectangles must share across the
// direction of travel to count as facing each other. One cell is not enough:
// with shared borders, panes that only meet at a corner share exactly one
// column or row, and a step down from a short pane must not land on the tall
// pane beside the one below it.
// A pane one cell across needs only the one cell it has.
const minNeighbourOverlap = 2

// Neighbour picks the rectangle focus moves to when it steps from from toward
// side, and returns its index in cands, or -1 when nothing lies that way.
//
// A candidate lies that way when both of its edges on that axis are further
// along than from's: for right, it starts right of from's left edge and ends
// right of from's right edge. That holds for a tiled neighbour, for one that
// shares a border cell, and for a floating window that half covers from.
//
// Among those, the ones that face from (share at least minNeighbourOverlap
// cells across the axis) win. The nearest wins among them, then the one whose
// centre is closest to from's across the axis, then the one sharing the most,
// then the earlier index. In a tiled layout that is
// always a pane touching from on that side, whatever the nesting of the splits.
//
// When nothing faces from and fallback is true, the nearest candidate lying
// that way is taken instead, counting distance across the axis double. That is
// for floating windows, which need not line up at all. A tiled layout passes
// false: its panes cover the screen, so a pane with nothing facing it on a side
// is at the edge, and a diagonal jump there would not be the direction pressed.
func Neighbour(from Rect, cands []Rect, side Side, fallback bool) int {
	f := towardRight(from, side)
	best, bestFacing := -1, false
	var bestKey [4]int
	for i, raw := range cands {
		c := towardRight(raw, side)
		if c.X <= f.X || c.X+c.W <= f.X+f.W {
			continue
		}
		gap := max(c.X-(f.X+f.W), 0)
		overlap := min(c.Y+c.H, f.Y+f.H) - max(c.Y, f.Y)
		facing := overlap >= min(minNeighbourOverlap, f.H, c.H)
		if !facing && !fallback {
			continue
		}
		// Twice the centres' offset, to stay in whole cells.
		offset := abs((2*c.Y + c.H) - (2*f.Y + f.H))
		key := [4]int{gap, offset, -overlap, i}
		if !facing {
			key = [4]int{gap + 2*max(-overlap, 0), offset, 0, i}
		}
		switch {
		case best < 0,
			facing && !bestFacing,
			facing == bestFacing && slices.Compare(key[:], bestKey[:]) < 0:
			best, bestFacing, bestKey = i, facing, key
		}
	}
	return best
}

// towardRight turns a rectangle so that stepping toward side becomes stepping
// right: X is the axis of travel and grows in that direction, Y is across it.
func towardRight(r Rect, side Side) Rect {
	switch side {
	case SideLeft:
		return Rect{X: -(r.X + r.W), Y: r.Y, W: r.W, H: r.H}
	case SideDown:
		return Rect{X: r.Y, Y: r.X, W: r.H, H: r.W}
	case SideUp:
		return Rect{X: -(r.Y + r.H), Y: r.X, W: r.H, H: r.W}
	}
	return r
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
