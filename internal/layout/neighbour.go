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

// Neighbour picks the rectangle focus moves to when it steps from from toward
// side, and returns its index in cands, or -1 when nothing lies that way.
//
// A candidate lies that way when both of its edges on that axis are further
// along than from's: for right, it starts right of from's left edge and ends
// right of from's right edge. That holds for a tiled neighbour, for one that
// shares a border cell, and for a floating window that half covers from.
//
// Among those, the ones that face from (share at least one cell across the
// axis) win. The nearest wins among them. At the same distance, one sharing two
// or more cells beats one sharing a single cell: with shared borders, panes
// that only meet at a corner share exactly one cell, so a step down from a
// short pane must not land on the tall pane beside the one below it. A single
// cell still counts when nothing else touches that side, since a pane can meet
// its only neighbour along one row. After that, the one whose centre is closest
// to from's across the axis wins, then the one sharing the most, then the
// earlier index. In a tiled layout that is always a pane touching from on that
// side, whatever the nesting of the splits.
//
// When nothing faces from and fallback is true, the nearest candidate lying
// that way is taken instead, counting distance across the axis double. That is
// for floating windows, which need not line up at all. A tiled layout passes
// false: its panes cover the screen, so a pane with nothing facing it on a side
// is at the edge, and a diagonal jump there would not be the direction pressed.
func Neighbour(from Rect, cands []Rect, side Side, fallback bool) int {
	f := towardRight(from, side)
	best, bestFacing := -1, false
	var bestKey [5]int
	for i, raw := range cands {
		c := towardRight(raw, side)
		if c.X <= f.X || c.X+c.W <= f.X+f.W {
			continue
		}
		gap := max(c.X-(f.X+f.W), 0)
		overlap := min(c.Y+c.H, f.Y+f.H) - max(c.Y, f.Y)
		facing := overlap >= 1
		if !facing && !fallback {
			continue
		}
		// Twice the centres' offset, to stay in whole cells.
		offset := abs((2*c.Y + c.H) - (2*f.Y + f.H))
		thin := 0
		if overlap == 1 {
			thin = 1
		}
		key := [5]int{gap, thin, offset, -overlap, i}
		if !facing {
			key = [5]int{gap + 2*max(-overlap, 0), 0, offset, 0, i}
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

// Wrap picks the rectangle focus wraps to when Neighbour finds nothing toward
// side in a tiled layout, and returns its index in cands, or -1. Focus leaves
// the layout at one edge and comes back in at the other: of the candidates that
// lie the other way and face from, the one farthest that way wins. Its far edge
// is the edge of the layout, so in a tiled layout it is a pane on that edge.
//
// Ties go as in Neighbour: one sharing two or more cells with from beats one
// sharing a single cell, then the one whose centre is closest to from's across
// the axis wins, then the one sharing the most, then the earlier index.
//
// A candidate must lie the other way by the rule Neighbour uses for its side,
// so a pane beside from that only shares a border cell with it never counts. A
// pane that spans the whole layout on the axis of travel has nothing to wrap to.
//
// The tmux shim wraps in internal/tmuxcompat by tmux's own rule (edges that
// meet, ties to the pane focused last) and does not call this.
func Wrap(from Rect, cands []Rect, side Side) int {
	back := opposite(side)
	f := towardRight(from, back)
	best := -1
	var bestKey [5]int
	for i, raw := range cands {
		c := towardRight(raw, back)
		if c.X <= f.X || c.X+c.W <= f.X+f.W {
			continue
		}
		overlap := min(c.Y+c.H, f.Y+f.H) - max(c.Y, f.Y)
		if overlap < 1 {
			continue
		}
		offset := abs((2*c.Y + c.H) - (2*f.Y + f.H))
		thin := 0
		if overlap == 1 {
			thin = 1
		}
		// The far edge first: the larger it is, the farther the candidate.
		key := [5]int{-(c.X + c.W), thin, offset, -overlap, i}
		if best < 0 || slices.Compare(key[:], bestKey[:]) < 0 {
			best, bestKey = i, key
		}
	}
	return best
}

func opposite(side Side) Side {
	switch side {
	case SideLeft:
		return SideRight
	case SideRight:
		return SideLeft
	case SideUp:
		return SideDown
	}
	return SideUp
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
