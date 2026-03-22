package app

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// renderSharedBorderGrid draws a single unified border grid over the entire
// tiling viewport. The grid consists of an outer frame and internal split
// lines with proper junction characters where borders intersect.
func (m *OS) renderSharedBorderGrid() *lipgloss.Layer {
	topMargin := m.GetTopMargin()
	viewW := m.GetRenderWidth()
	viewH := m.GetUsableHeight()

	if viewW < 3 || viewH < 3 {
		return nil
	}

	// The outer frame occupies the full viewport area.
	// Content bounds (passed to BSP) are inset by 1 on each side.
	frameX := 0
	frameY := topMargin
	frameW := viewW
	frameH := viewH

	// Retrieve border characters from the current style.
	border := config.GetBorderForStyle()
	hChar := border.Top
	vChar := border.Left
	tlChar := border.TopLeft
	trChar := border.TopRight
	blChar := border.BottomLeft
	brChar := border.BottomRight

	// Junction characters for rounded style (the default).
	// These connect inner split lines with the outer frame / other splits.
	tDown := "┬"  // top border + vertical split going down
	tUp := "┴"    // bottom border + vertical split going up
	tRight := "├" // left border + horizontal split going right
	tLeft := "┤"  // right border + horizontal split going left
	cross := "┼"  // two splits crossing

	// Allocate a 2D grid of runes initialised to spaces.
	grid := make([][]string, frameH)
	for y := range grid {
		row := make([]string, frameW)
		for x := range row {
			row[x] = " "
		}
		grid[y] = row
	}

	// Draw outer frame ---------------------------------------------------

	// Top and bottom rows
	for x := 1; x < frameW-1; x++ {
		grid[0][x] = hChar
		grid[frameH-1][x] = hChar
	}
	// Left and right columns
	for y := 1; y < frameH-1; y++ {
		grid[y][0] = vChar
		grid[y][frameW-1] = vChar
	}
	// Corners
	grid[0][0] = tlChar
	grid[0][frameW-1] = trChar
	grid[frameH-1][0] = blChar
	grid[frameH-1][frameW-1] = brChar

	// Collect splits from the BSP tree -----------------------------------
	tree := m.WorkspaceTrees[m.CurrentWorkspace]
	if tree == nil || tree.IsEmpty() {
		// No splits – just draw the outer frame.
		return m.buildBorderLayer(grid, frameX, frameY, frameW, frameH)
	}

	contentBounds := m.GetBSPBounds()
	splits := tree.CollectSplits(contentBounds, true)

	// Mark cells occupied by split lines in the grid.
	// Coordinates in splits are in absolute screen space; convert to grid-local.
	// gridX = absX - frameX, gridY = absY - frameY.

	// First pass: draw the lines.
	for _, s := range splits {
		if s.Type == layout.SplitVertical {
			gx := s.Pos - frameX
			if gx < 0 || gx >= frameW {
				continue
			}
			fromGY := s.FromPos - frameY
			toGY := s.ToPos - frameY
			for gy := fromGY; gy <= toGY; gy++ {
				if gy >= 0 && gy < frameH {
					grid[gy][gx] = vChar
				}
			}
			// Junctions with outer frame
			if fromGY-1 >= 0 && fromGY-1 < frameH && grid[fromGY-1][gx] == hChar {
				grid[fromGY-1][gx] = tDown
			}
			if toGY+1 >= 0 && toGY+1 < frameH && grid[toGY+1][gx] == hChar {
				grid[toGY+1][gx] = tUp
			}
		} else {
			gy := s.Pos - frameY
			if gy < 0 || gy >= frameH {
				continue
			}
			fromGX := s.FromPos - frameX
			toGX := s.ToPos - frameX
			for gx := fromGX; gx <= toGX; gx++ {
				if gx >= 0 && gx < frameW {
					grid[gy][gx] = hChar
				}
			}
			// Junctions with outer frame
			if fromGX-1 >= 0 && fromGX-1 < frameW && grid[gy][fromGX-1] == vChar {
				grid[gy][fromGX-1] = tRight
			}
			if toGX+1 >= 0 && toGX+1 < frameW && grid[gy][toGX+1] == vChar {
				grid[gy][toGX+1] = tLeft
			}
		}
	}

	// Second pass: fix cross junctions where vertical and horizontal meet.
	for _, vs := range splits {
		if vs.Type != layout.SplitVertical {
			continue
		}
		vgx := vs.Pos - frameX
		for _, hs := range splits {
			if hs.Type != layout.SplitHorizontal {
				continue
			}
			hgy := hs.Pos - frameY
			// Check if they actually overlap
			if vgx >= hs.FromPos-frameX && vgx <= hs.ToPos-frameX &&
				hgy >= vs.FromPos-frameY && hgy <= vs.ToPos-frameY {
				if vgx >= 0 && vgx < frameW && hgy >= 0 && hgy < frameH {
					grid[hgy][vgx] = cross
				}
			}
		}
	}

	return m.buildBorderLayer(grid, frameX, frameY, frameW, frameH)
}

// buildBorderLayer converts a character grid into a styled lipgloss Layer.
func (m *OS) buildBorderLayer(grid [][]string, x, y, w, h int) *lipgloss.Layer {
	borderColor := theme.BorderUnfocused()

	var sb strings.Builder
	// Pre-compute styled renderer once.
	colorStyle := lipgloss.NewStyle().Foreground(borderColor)

	for row := range grid {
		if row > 0 {
			sb.WriteRune('\n')
		}
		// Build each row as a single styled string.
		var rowSB strings.Builder
		for _, ch := range grid[row] {
			rowSB.WriteString(ch)
		}
		sb.WriteString(colorStyle.Render(rowSB.String()))
	}

	layer := lipgloss.NewLayer(sb.String()).
		X(x).Y(y).
		Z(config.ZIndexAnimating - 1). // Just below animating windows, above normal windows
		ID("shared-border-grid")
	return layer
}
