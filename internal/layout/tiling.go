// Package layout provides window tiling and layout management for the terminal.
package layout

// TileLayout represents the position and size for a tiled window
type TileLayout struct {
	X, Y, Width, Height int
}

// span is one neighbour's place along an axis: where it starts and how far it
// runs.
type span struct{ Pos, Size int }

// spans divides an extent between n neighbours, keeping gap cells free between
// each adjacent pair.
//
// The gap comes out of the extent before it is divided, so the shares are even.
// Taking it out of one side afterwards - which is what this tiler used to do -
// made the far pane narrower than the near one by the whole gap, on every split
// and at every pane count.
//
// The remainder is spread a cell at a time from the first share rather than
// handed to the last, so no two neighbours differ by more than one cell. A
// last pane carrying the whole remainder is visibly the odd one out on a wide
// screen, and it is the pane a rounding bug hides in.
//
// Shares are floored at one cell and never at a fixed minimum. A minimum wider
// than the share is what pushed a pane past its own rectangle and into its
// neighbour's, so a workspace holding more panes than fit at a comfortable size
// drew them overlapping instead of simply smaller. This is the rule the BSP
// tiler already follows (bsp.go applyLayoutRecursive): tiling never overlaps;
// when space runs short the panes shrink.
func spans(origin, total, n, gap int) []span {
	if n <= 0 {
		return nil
	}
	if n == 1 {
		return []span{{Pos: origin, Size: max(total, 1)}}
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

// splitByRatio is spans for two neighbours whose shares are not equal: the
// first takes ratio of what is left once the gap is reserved. Both sides keep
// at least one cell, so a ratio at either end of its range cannot produce a
// pane with nothing in it.
func splitByRatio(origin, total int, ratio float64, gap int) (span, span) {
	avail := max(total-gap, 2)
	near := min(max(int(float64(avail)*ratio), 1), avail-1)
	return span{Pos: origin, Size: near}, span{Pos: origin + near + gap, Size: avail - near}
}

// gridColumns is how many columns the grid uses for n panes. Two up to six,
// three beyond, which keeps a pane roughly as wide as it is tall at the sizes a
// terminal actually is.
func gridColumns(n int) int {
	if n <= 6 {
		return 2
	}
	return 3
}

// CalculateTilingLayout returns optimal positions for n windows
// masterRatio controls the width ratio of the master (left) pane (0.3-0.7)
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
func CalculateTilingLayout(n int, screenWidth int, usableHeight int, topMargin int, masterRatio float64, gap int) []TileLayout {
	if n == 0 {
		return nil
	}

	layouts := make([]TileLayout, 0, n)

	// Clamp master ratio to reasonable bounds (30%-70%)
	if masterRatio < 0.3 {
		masterRatio = 0.3
	} else if masterRatio > 0.7 {
		masterRatio = 0.7
	}

	// Status bar is an overlay, windows use full usable height starting at Y=0
	switch n {
	case 1:
		// Single window - full screen
		layouts = append(layouts, TileLayout{
			X:      0,
			Y:      topMargin,
			Width:  screenWidth,
			Height: usableHeight,
		})

	case 2:
		// Two windows, split along whichever axis the screen is longer on as it
		// is drawn. A cell is about twice as tall as it is wide, so a tall
		// 51x37 terminal reads as landscape by the numbers while being
		// obviously upright to the eye; splitting it side by side hands out two
		// 25 column panes. Compare against the scaled height so the split
		// follows the shape on screen, and stack when it is taller.
		if screenWidth >= usableHeight*cellAspect {
			near, far := splitByRatio(0, screenWidth, masterRatio, gap)
			layouts = append(layouts,
				TileLayout{X: near.Pos, Y: topMargin, Width: near.Size, Height: usableHeight},
				TileLayout{X: far.Pos, Y: topMargin, Width: far.Size, Height: usableHeight},
			)
			break
		}
		near, far := splitByRatio(topMargin, usableHeight, masterRatio, gap)
		layouts = append(layouts,
			TileLayout{X: 0, Y: near.Pos, Width: screenWidth, Height: near.Size},
			TileLayout{X: 0, Y: far.Pos, Width: screenWidth, Height: far.Size},
		)

	case 3:
		// Three windows - one left (master), two right stacked
		master, stack := splitByRatio(0, screenWidth, masterRatio, gap)
		rows := spans(topMargin, usableHeight, 2, gap)
		layouts = append(layouts,
			TileLayout{X: master.Pos, Y: topMargin, Width: master.Size, Height: usableHeight},
			TileLayout{X: stack.Pos, Y: rows[0].Pos, Width: stack.Size, Height: rows[0].Size},
			TileLayout{X: stack.Pos, Y: rows[1].Pos, Width: stack.Size, Height: rows[1].Size},
		)

	default:
		// A grid. Four windows is the 2x2 case of it, which is why it has no
		// branch of its own any more: it was the same arithmetic written twice,
		// and the copy did not get the fixes the general path got.
		cols := gridColumns(n)
		if n == 4 {
			cols = 2
		}
		rowCount := (n + cols - 1) / cols
		rows := spans(topMargin, usableHeight, rowCount, gap)

		for row := range rowCount {
			// The last row carries whatever is left over, which can be fewer
			// panes than a full row. They share the width between them rather
			// than leaving a hole where the missing ones would have been.
			inRow := min(cols, n-row*cols)
			cells := spans(0, screenWidth, inRow, gap)
			for col := range inRow {
				layouts = append(layouts, TileLayout{
					X:      cells[col].Pos,
					Y:      rows[row].Pos,
					Width:  cells[col].Size,
					Height: rows[row].Size,
				})
			}
		}
	}

	return layouts
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
	// Reach into the cells past each end while they are gap as well, so lines
	// meet at a T junction instead of leaving a hole where three panes touch.
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
			// One line per boundary, on the gap's leading column, whatever the
			// gap is. A line on every column of the gap is what this did, which
			// is correct at a gap of one and turns a wider gap into a thick
			// rule: appearance.gap asks for ground between the panes, not for a
			// wider divider, and the BSP tiler already answers it that way.
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
