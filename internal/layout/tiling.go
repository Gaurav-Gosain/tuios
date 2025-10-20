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

	switch n {
	case 1:
		// Single window - full screen
		layouts = append(layouts, TileLayout{
			X:      0,
			Y:      0,
			Width:  screenWidth,
			Height: usableHeight,
		})

	case 2:
		// Two windows - side by side
		halfWidth := screenWidth / 2
		layouts = append(layouts,
			TileLayout{
				X:      0,
				Y:      0,
				Width:  halfWidth,
				Height: usableHeight,
			},
			TileLayout{
				X:      halfWidth,
				Y:      0,
				Width:  screenWidth - halfWidth,
				Height: usableHeight,
			},
		)

	case 3:
		// Three windows - one left, two right stacked
		halfWidth := screenWidth / 2
		halfHeight := usableHeight / 2
		layouts = append(layouts,
			TileLayout{
				X:      0,
				Y:      0,
				Width:  halfWidth,
				Height: usableHeight,
			},
			TileLayout{
				X:      halfWidth,
				Y:      0,
				Width:  screenWidth - halfWidth,
				Height: halfHeight,
			},
			TileLayout{
				X:      halfWidth,
				Y:      halfHeight,
				Width:  screenWidth - halfWidth,
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
				Y:      0,
				Width:  halfWidth,
				Height: halfHeight,
			},
			TileLayout{
				X:      halfWidth,
				Y:      0,
				Width:  screenWidth - halfWidth,
				Height: halfHeight,
			},
			TileLayout{
				X:      0,
				Y:      halfHeight,
				Width:  halfWidth,
				Height: usableHeight - halfHeight,
			},
			TileLayout{
				X:      halfWidth,
				Y:      halfHeight,
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
				Y:      row * cellHeight,
				Width:  cellWidth,
				Height: cellHeight,
			}

			// Adjust last column width to fill screen
			if col == actualCols-1 {
				layout.Width = screenWidth - layout.X
			}
			// Adjust last row height to fill screen
			if row == rows-1 {
				layout.Height = usableHeight - layout.Y
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
