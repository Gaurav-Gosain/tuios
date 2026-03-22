package app

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// renderSeparatorOverlay builds a lipgloss Layer containing the shared border
// separator lines between tiled panes. It uses the BSP tree's CollectSplits
// to find all separator positions and draws the appropriate box-drawing
// characters at each position.
func (m *OS) renderSeparatorOverlay() *lipgloss.Layer {
	tree := m.WorkspaceTrees[m.CurrentWorkspace]
	if tree == nil || tree.IsEmpty() {
		return nil
	}

	bounds := m.GetBSPBounds()
	splits := tree.CollectSplits(bounds)
	if len(splits) == 0 {
		return nil
	}

	viewW := m.GetRenderWidth()
	viewH := m.GetRenderHeight()

	// Build a 2D grid of characters for separator positions.
	// We only need to cover the viewport area.
	grid := make([][]rune, viewH)
	for y := range grid {
		grid[y] = make([]rune, viewW)
		for x := range grid[y] {
			grid[y][x] = ' '
		}
	}

	// Mark all separator cells in the grid
	for _, s := range splits {
		if s.Vertical {
			// Vertical separator: draw '│' in a column at X=s.Pos
			x := s.Pos
			if x < 0 || x >= viewW {
				continue
			}
			for y := s.From; y <= s.To; y++ {
				if y >= 0 && y < viewH {
					grid[y][x] = '│'
				}
			}
		} else {
			// Horizontal separator: draw '─' in a row at Y=s.Pos
			y := s.Pos
			if y < 0 || y >= viewH {
				continue
			}
			for x := s.From; x <= s.To; x++ {
				if x >= 0 && x < viewW {
					grid[y][x] = '─'
				}
			}
		}
	}

	// Fix intersections: where a vertical and horizontal separator meet, use the
	// appropriate intersection character.
	for y := range viewH {
		for x := range viewW {
			if grid[y][x] == ' ' {
				continue
			}
			// Check if this cell is at an intersection
			hasUp := y > 0 && (grid[y-1][x] == '│' || grid[y-1][x] == '┼' || grid[y-1][x] == '┬' || grid[y-1][x] == '├' || grid[y-1][x] == '┤')
			hasDown := y < viewH-1 && (grid[y+1][x] == '│' || grid[y+1][x] == '┼' || grid[y+1][x] == '┴' || grid[y+1][x] == '├' || grid[y+1][x] == '┤')
			hasLeft := x > 0 && (grid[y][x-1] == '─' || grid[y][x-1] == '┼' || grid[y][x-1] == '├' || grid[y][x-1] == '┬' || grid[y][x-1] == '┴')
			hasRight := x < viewW-1 && (grid[y][x+1] == '─' || grid[y][x+1] == '┼' || grid[y][x+1] == '┤' || grid[y][x+1] == '┬' || grid[y][x+1] == '┴')

			vert := hasUp || hasDown
			horiz := hasLeft || hasRight

			if vert && horiz {
				// Intersection
				if hasUp && hasDown && hasLeft && hasRight {
					grid[y][x] = '┼'
				} else if !hasUp && hasDown && hasLeft && hasRight {
					grid[y][x] = '┬'
				} else if hasUp && !hasDown && hasLeft && hasRight {
					grid[y][x] = '┴'
				} else if hasUp && hasDown && !hasLeft && hasRight {
					grid[y][x] = '├'
				} else if hasUp && hasDown && hasLeft && !hasRight {
					grid[y][x] = '┤'
				} else {
					grid[y][x] = '┼'
				}
			}
		}
	}

	// Build the output string from the grid.
	// Use the unfocused border color for separator lines.
	borderColor := theme.BorderUnfocused()
	style := lipgloss.NewStyle().Foreground(borderColor)

	var sb strings.Builder
	for y := range viewH {
		if y > 0 {
			sb.WriteByte('\n')
		}
		line := string(grid[y])
		// Only style non-empty lines (lines that have separator characters)
		hasContent := false
		for _, r := range grid[y] {
			if r != ' ' {
				hasContent = true
				break
			}
		}
		if hasContent {
			// Style each character individually to preserve spacing
			var lineBuf strings.Builder
			for _, r := range grid[y] {
				if r != ' ' {
					lineBuf.WriteString(style.Render(string(r)))
				} else {
					lineBuf.WriteRune(' ')
				}
			}
			sb.WriteString(lineBuf.String())
		} else {
			sb.WriteString(line)
		}
	}

	return lipgloss.NewLayer(sb.String()).
		X(0).Y(0).
		Z(config.ZIndexSeparators).
		ID("shared-borders")
}
