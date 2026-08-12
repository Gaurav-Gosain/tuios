// Package layout provides window tiling and layout management for the terminal.
package layout

import (
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TileLayout represents the position and size for a tiled window
type TileLayout struct {
	X, Y, Width, Height int
}

// CalculateTilingLayout returns optimal positions for n windows
// masterRatio controls the width ratio of the master (left) pane (0.3-0.7)
//
// Under config.SharedBorders the tiler reserves one cell between neighbours for
// the drawn divider, exactly as the BSP splitter does (bsp.go childBounds).
// Both layouts hand the separator its own column that way, so neither pane's
// first column is painted over by the line between them.
func CalculateTilingLayout(n int, screenWidth int, usableHeight int, topMargin int, masterRatio float64) []TileLayout {
	if n == 0 {
		return nil
	}

	gap := 0
	if config.SharedBorders {
		gap = 1
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
			masterWidth := int(float64(screenWidth) * masterRatio)
			layouts = append(layouts,
				TileLayout{
					X:      0,
					Y:      topMargin,
					Width:  masterWidth,
					Height: usableHeight,
				},
				TileLayout{
					X:      masterWidth + gap,
					Y:      topMargin,
					Width:  screenWidth - masterWidth - gap,
					Height: usableHeight,
				},
			)
			break
		}
		masterHeight := int(float64(usableHeight) * masterRatio)
		layouts = append(layouts,
			TileLayout{
				X:      0,
				Y:      topMargin,
				Width:  screenWidth,
				Height: masterHeight,
			},
			TileLayout{
				X:      0,
				Y:      topMargin + masterHeight + gap,
				Width:  screenWidth,
				Height: usableHeight - masterHeight - gap,
			},
		)

	case 3:
		// Three windows - one left (master), two right stacked
		masterWidth := int(float64(screenWidth) * masterRatio)
		slaveX := masterWidth + gap
		slaveWidth := screenWidth - slaveX
		halfHeight := usableHeight / 2
		layouts = append(layouts,
			TileLayout{
				X:      0,
				Y:      topMargin,
				Width:  masterWidth,
				Height: usableHeight,
			},
			TileLayout{
				X:      slaveX,
				Y:      topMargin,
				Width:  slaveWidth,
				Height: halfHeight,
			},
			TileLayout{
				X:      slaveX,
				Y:      topMargin + halfHeight + gap,
				Width:  slaveWidth,
				Height: usableHeight - halfHeight - gap,
			},
		)

	case 4:
		// Four windows - 2x2 grid
		halfWidth := screenWidth / 2
		rightX := halfWidth + gap
		halfHeight := usableHeight / 2
		bottomY := topMargin + halfHeight + gap
		layouts = append(layouts,
			TileLayout{
				X:      0,
				Y:      topMargin,
				Width:  halfWidth,
				Height: halfHeight,
			},
			TileLayout{
				X:      rightX,
				Y:      topMargin,
				Width:  screenWidth - rightX,
				Height: halfHeight,
			},
			TileLayout{
				X:      0,
				Y:      bottomY,
				Width:  halfWidth,
				Height: usableHeight - halfHeight - gap,
			},
			TileLayout{
				X:      rightX,
				Y:      bottomY,
				Width:  screenWidth - rightX,
				Height: usableHeight - halfHeight - gap,
			},
		)

	default:
		// More than 4 windows - create a grid
		// Calculate optimal grid dimensions
		cols := 3
		if n <= 6 {
			cols = 2
		}
		rows := (n + cols - 1) / cols // Ceiling division

		// The gaps come out of the grid before the cells are sized, so the
		// tiles still add up to the region.
		cellWidth := (screenWidth - gap*(cols-1)) / cols
		cellHeight := (usableHeight - gap*(rows-1)) / rows

		for i := range n {
			row := i / cols
			col := i % cols

			// Last row might have fewer windows, so expand them
			actualCols := cols
			if row == rows-1 {
				remainingWindows := n - row*cols
				if remainingWindows < cols {
					actualCols = remainingWindows
					cellWidth = (screenWidth - gap*(actualCols-1)) / actualCols
				}
			}

			layout := TileLayout{
				X:      col * (cellWidth + gap),
				Y:      topMargin + row*(cellHeight+gap),
				Width:  cellWidth,
				Height: cellHeight,
			}

			// Adjust last column width to fill screen
			if col == actualCols-1 {
				layout.Width = screenWidth - layout.X
			}
			// Adjust last row height to fill screen
			if row == rows-1 {
				layout.Height = usableHeight - row*(cellHeight+gap)
			}

			layouts = append(layouts, layout)
		}
	}

	// Ensure minimum window size, then keep the widened tile on-screen. Without
	// the position clamp a tile that was grown to the minimum on a small terminal
	// would overflow screenWidth/usableHeight and overlap its neighbours.
	for i := range layouts {
		if layouts[i].Width < config.DefaultWindowWidth {
			layouts[i].Width = config.DefaultWindowWidth
		}
		if layouts[i].Height < config.DefaultWindowHeight {
			layouts[i].Height = config.DefaultWindowHeight
		}
		layouts[i].X = max(0, min(layouts[i].X, screenWidth-layouts[i].Width))
		layouts[i].Y = max(topMargin, min(layouts[i].Y, topMargin+usableHeight-layouts[i].Height))
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
			if b.X == a.X+a.W+gap {
				if from, to := max(a.Y, b.Y), min(a.Y+a.H, b.Y+b.H)-1; from <= to {
					for x := a.X + a.W; x < b.X; x++ {
						f, t := grow(x, from, to, true)
						splits = append(splits, SplitLine{Vertical: true, Pos: x, From: f, To: t})
					}
				}
			}
			if b.Y == a.Y+a.H+gap {
				if from, to := max(a.X, b.X), min(a.X+a.W, b.X+b.W)-1; from <= to {
					for y := a.Y + a.H; y < b.Y; y++ {
						f, t := grow(y, from, to, false)
						splits = append(splits, SplitLine{Vertical: false, Pos: y, From: f, To: t})
					}
				}
			}
		}
	}
	return splits
}
