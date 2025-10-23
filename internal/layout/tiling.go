package layout

import (
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TileLayout represents the position and size for a tiled window
type TileLayout struct {
	X, Y, Width, Height int
}

// CalculateTilingLayout returns optimal positions for n windows
func CalculateTilingLayout(n int, screenWidth int, usableHeight int) []TileLayout {
	if n == 0 {
		return nil
	}

	layouts := make([]TileLayout, 0, n)

	// Reserve 1 line at top for status bar to prevent clipping
	const topMargin = 1
	adjustedHeight := usableHeight - topMargin

	switch n {
	case 1:
		// Single window - full screen
		layouts = append(layouts, TileLayout{
			X:      0,
			Y:      topMargin,
			Width:  screenWidth,
			Height: adjustedHeight,
		})

	case 2:
		// Two windows - side by side
		halfWidth := screenWidth / 2
		layouts = append(layouts,
			TileLayout{
				X:      0,
				Y:      topMargin,
				Width:  halfWidth,
				Height: adjustedHeight,
			},
			TileLayout{
				X:      halfWidth,
				Y:      topMargin,
				Width:  screenWidth - halfWidth,
				Height: adjustedHeight,
			},
		)

	case 3:
		// Three windows - one left, two right stacked
		halfWidth := screenWidth / 2
		halfHeight := adjustedHeight / 2
		layouts = append(layouts,
			TileLayout{
				X:      0,
				Y:      topMargin,
				Width:  halfWidth,
				Height: adjustedHeight,
			},
			TileLayout{
				X:      halfWidth,
				Y:      topMargin,
				Width:  screenWidth - halfWidth,
				Height: halfHeight,
			},
			TileLayout{
				X:      halfWidth,
				Y:      topMargin + halfHeight,
				Width:  screenWidth - halfWidth,
				Height: adjustedHeight - halfHeight,
			},
		)

	case 4:
		// Four windows - 2x2 grid
		halfWidth := screenWidth / 2
		halfHeight := adjustedHeight / 2
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
				Height: adjustedHeight - halfHeight,
			},
			TileLayout{
				X:      halfWidth,
				Y:      topMargin + halfHeight,
				Width:  screenWidth - halfWidth,
				Height: adjustedHeight - halfHeight,
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
		cellHeight := adjustedHeight / rows

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
				layout.Height = adjustedHeight + topMargin - layout.Y
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
