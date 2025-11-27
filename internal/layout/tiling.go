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
func CalculateTilingLayout(n int, screenWidth int, usableHeight int, topMargin int, masterRatio float64) []TileLayout {
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
		// Two windows - side by side with configurable ratio
		masterWidth := int(float64(screenWidth) * masterRatio)
		slaveWidth := screenWidth - masterWidth
		layouts = append(layouts,
			TileLayout{
				X:      0,
				Y:      topMargin,
				Width:  masterWidth,
				Height: usableHeight,
			},
			TileLayout{
				X:      masterWidth,
				Y:      topMargin,
				Width:  slaveWidth,
				Height: usableHeight,
			},
		)

	case 3:
		// Three windows - one left (master), two right stacked
		masterWidth := int(float64(screenWidth) * masterRatio)
		slaveWidth := screenWidth - masterWidth
		halfHeight := usableHeight / 2
		layouts = append(layouts,
			TileLayout{
				X:      0,
				Y:      topMargin,
				Width:  masterWidth,
				Height: usableHeight,
			},
			TileLayout{
				X:      masterWidth,
				Y:      topMargin,
				Width:  slaveWidth,
				Height: halfHeight,
			},
			TileLayout{
				X:      masterWidth,
				Y:      topMargin + halfHeight,
				Width:  slaveWidth,
				Height: usableHeight - halfHeight,
			},
		)

	case 4:
		// Four windows - 2x2 grid
		halfWidth := screenWidth / 2
		halfHeight := usableHeight / 2
		layouts = append(layouts,
			TileLayout{
				X:      0,
				Y:      topMargin,
				Width:  halfWidth,
				Height: halfHeight,
			},
			TileLayout{
				X:      halfWidth,
				Y:      topMargin,
				Width:  screenWidth - halfWidth,
				Height: halfHeight,
			},
			TileLayout{
				X:      0,
				Y:      topMargin + halfHeight,
				Width:  halfWidth,
				Height: usableHeight - halfHeight,
			},
			TileLayout{
				X:      halfWidth,
				Y:      topMargin + halfHeight,
				Width:  screenWidth - halfWidth,
				Height: usableHeight - halfHeight,
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

		cellWidth := screenWidth / cols
		cellHeight := usableHeight / rows

		for i := range n {
			row := i / cols
			col := i % cols

			// Last row might have fewer windows, so expand them
			actualCols := cols
			if row == rows-1 {
				remainingWindows := n - row*cols
				if remainingWindows < cols {
					actualCols = remainingWindows
					cellWidth = screenWidth / actualCols
				}
			}

			layout := TileLayout{
				X:      col * cellWidth,
				Y:      topMargin + row*cellHeight,
				Width:  cellWidth,
				Height: cellHeight,
			}

			// Adjust last column width to fill screen
			if col == actualCols-1 {
				layout.Width = screenWidth - layout.X
			}
			// Adjust last row height to fill screen
			if row == rows-1 {
				layout.Height = usableHeight - (row * cellHeight)
			}

			layouts = append(layouts, layout)
		}
	}

	// Ensure minimum window size
	for i := range layouts {
		if layouts[i].Width < config.DefaultWindowWidth {
			layouts[i].Width = config.DefaultWindowWidth
		}
		if layouts[i].Height < config.DefaultWindowHeight {
			layouts[i].Height = config.DefaultWindowHeight
		}
	}

	return layouts
}
