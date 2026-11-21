// Package layout provides window tiling and layout management for the terminal.
package layout

import (
	"math"
)

// TileLayout represents the position and size for a tiled window
type TileLayout struct {
	X, Y, Width, Height int
}

type span struct{ Pos, Size int }

func spans(origin, total, n, gap int) []span {
	if n <= 0 {
		return nil
	}
	if n == 1 {
		return []span{{Pos: origin, Size: max(total, 1)}}
	}
	if total-gap*(n-1) < n {
		gap = max((total-n)/(n-1), 0)
	}
	avail := total - gap*(n-1)
	base, rem := 1, 0
	if avail >= n {
		base, rem = avail/n, avail%n
	}
	out := make([]span, 0, n)
	pos := origin
	for i := range n {
		size := base
		if i < rem {
			size++
		}
		out = append(out, span{Pos: pos, Size: size})
		pos += size + gap
	}
	return out
}

func weightedSpans(origin, total, n int, weights []float64, gap int) []span {
	if n <= 1 || len(weights) != n {
		return spans(origin, total, n, gap)
	}
	sum := 0.0
	for _, w := range weights {
		if !(w > 0) || math.IsInf(w, 0) {
			return spans(origin, total, n, gap)
		}
		sum += w
	}
	if total-gap*(n-1) < n {
		gap = max((total-n)/(n-1), 0)
	}
	avail := total - gap*(n-1)
	if avail < n {
		return spans(origin, total, n, gap)
	}
	out := make([]span, 0, n)
	pos, prev, acc := origin, 0, 0.0
	for i, w := range weights {
		acc += w
		edge := int(math.Round(float64(avail) * acc / sum))
		if i == n-1 {
			edge = avail
		}
		edge = min(max(edge, prev+1), avail-(n-1-i))
		size := edge - prev
		out = append(out, span{Pos: pos, Size: size})
		pos += size + gap
		prev = edge
	}
	return out
}

func splitByRatio(origin, total int, ratio float64, gap int) (span, span) {
	avail := max(total-gap, 2)
	near := min(max(int(float64(avail)*ratio), 1), avail-1)
	return span{Pos: origin, Size: near}, span{Pos: origin + near + gap, Size: avail - near}
}

// MinSplitRatio and MaxSplitRatio bound the master ratio and the stack ratio
// the master-stack tiler accepts. They are the appearance.master_ratio range,
// so the settings row, a percentage resize and the tiler agree on one range.
var (
	MinSplitRatio = float64(MasterRatioMin) / 100
	MaxSplitRatio = float64(MasterRatioMax) / 100
)

func clampSplitRatio(ratio float64) float64 {
	return min(max(ratio, MinSplitRatio), MaxSplitRatio)
}

// SplitRatioFor is the inverse of splitByRatio: the ratio at which a split of
// total cells with gap cells between the two sides gives the near side exactly
// near cells. The ratio aims at the middle of the cell, so the truncation in
// splitByRatio lands on near rather than one short of it. The result is
// clamped to the tiler's range, so a near side outside that range comes back
// at the nearest size the tiler will produce.
func SplitRatioFor(near, total, gap int) float64 {
	avail := max(total-gap, 2)
	return clampSplitRatio((float64(near) + 0.5) / float64(avail))
}

func gridColumns(n int) int {
	if n <= 6 {
		return 2
	}
	return 3
}

// MasterStackSideBySide reports whether the master-stack tiler puts two panes
// side by side in a region of this size. When it does not, it stacks them and
// the master ratio is the top pane's share of the height.
func MasterStackSideBySide(screenWidth, usableHeight int) bool {
	return screenWidth >= usableHeight*cellAspect
}

// CalculateMasterStackLayout returns the master-stack positions for n windows
// with the defaults: one master, on the left, and a grid from four panes up.
//
// masterRatio is the master pane's share of the split: its width, or its height
// when two panes are stacked on a tall screen. stackRatio is the top stacked
// pane's share of the height in the three pane layout. Zero or less means
// unset and the two stacked panes share the height equally, which is what the
// layout did before the ratio existed. A masterRatio of zero or less is the
// default split. Both ratios are clamped to MinSplitRatio..MaxSplitRatio. A
// grid of four or more panes is equal-share and reads neither.
//
// gap is the cells reserved between neighbours for the drawn divider, on the
// same terms as the BSP splitter (bsp.go childBounds). Both layouts hand the
// separator its own column that way, so neither pane's first column is painted
// over by the line between them.
//
// Every rectangle it returns is inside the region and disjoint from every other
// one, at any size and any pane count. That is the tiler's whole contract, and
// it is what the fixed pane minimum this used to enforce broke: on a 51x37
// terminal seven panes were each grown to twenty columns inside a region that
// could give them seventeen, and the frame showed panes on top of each other.
func CalculateMasterStackLayout(n int, screenWidth int, usableHeight int, topMargin int, masterRatio, stackRatio float64, gap int) []TileLayout {
	return CalculateMasterLayout(n, screenWidth, usableHeight, topMargin, MasterParams{
		Position:   MasterPositionLeft,
		Count:      1,
		Ratio:      masterRatio,
		StackRatio: stackRatio,
		Grid:       true,
		Gap:        gap,
	})
}

// SplitsBetween returns the separator lines that belong in the gaps between
// adjacent rects.
//
// The master-stack tiler keeps no tree to walk, so its dividers are read back
// from where the panes actually ended up. A line is only ever emitted on a cell
// no pane occupies, which is the property that keeps it off the first column of
// the pane beside it.
func SplitsBetween(rects []Rect, gap int) []SplitLine {
	if gap <= 0 {
		return nil
	}

	free := func(x, y int) bool {
		for _, r := range rects {
			if x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H {
				return false
			}
		}
		return true
	}
	grow := func(pos, from, to int, vertical bool) (int, int) {
		at := func(along int) bool {
			if vertical {
				return free(pos, along)
			}
			return free(along, pos)
		}
		for range gap {
			if !at(from - 1) {
				break
			}
			from--
		}
		for range gap {
			if !at(to + 1) {
				break
			}
			to++
		}
		return from, to
	}

	var splits []SplitLine
	for _, a := range rects {
		for _, b := range rects {
			if b.X == a.X+a.W+gap {
				if from, to := max(a.Y, b.Y), min(a.Y+a.H, b.Y+b.H)-1; from <= to {
					x := a.X + a.W
					f, t := grow(x, from, to, true)
					splits = append(splits, SplitLine{Vertical: true, Pos: x, From: f, To: t})
				}
			}
			if b.Y == a.Y+a.H+gap {
				if from, to := max(a.X, b.X), min(a.X+a.W, b.X+b.W)-1; from <= to {
					y := a.Y + a.H
					f, t := grow(y, from, to, false)
					splits = append(splits, SplitLine{Vertical: false, Pos: y, From: f, To: t})
				}
			}
		}
	}
	return splits
}
