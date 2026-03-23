package app

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// renderSeparatorOverlay renders separator lines between tiled panes.
// Instead of a full-viewport grid (which would paint spaces over window content),
// this renders each separator line as its own positioned lipgloss Layer.
func (m *OS) renderSeparatorOverlay() []*lipgloss.Layer {
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

	// Build a sparse grid to detect intersections
	type cellInfo struct {
		hasVert, hasHoriz bool
	}
	cells := make(map[[2]int]*cellInfo)

	getCell := func(x, y int) *cellInfo {
		key := [2]int{x, y}
		if c, ok := cells[key]; ok {
			return c
		}
		c := &cellInfo{}
		cells[key] = c
		return c
	}

	// Mark cells
	for _, s := range splits {
		if s.Vertical {
			x := s.Pos
			if x < 0 || x >= viewW {
				continue
			}
			for y := s.From; y <= s.To && y < viewH; y++ {
				if y >= 0 {
					getCell(x, y).hasVert = true
				}
			}
		} else {
			y := s.Pos
			if y < 0 || y >= viewH {
				continue
			}
			for x := s.From; x <= s.To && x < viewW; x++ {
				if x >= 0 {
					getCell(x, y).hasHoriz = true
				}
			}
		}
	}

	// Resolve characters
	type charPos struct {
		x, y int
		ch   rune
	}
	var chars []charPos

	for key, c := range cells {
		x, y := key[0], key[1]
		var ch rune
		if c.hasVert && c.hasHoriz {
			ch = '┼'
		} else if c.hasVert {
			ch = '│'
		} else if c.hasHoriz {
			ch = '─'
		}
		if ch != 0 {
			// Check adjacency for T-junctions at edges
			if c.hasVert && c.hasHoriz {
				// Already ┼
			} else if c.hasHoriz {
				// Check if vertical neighbors exist for T-junctions
				_, hasUp := cells[[2]int{x, y - 1}]
				_, hasDown := cells[[2]int{x, y + 1}]
				if hasUp && hasDown {
					ch = '┼'
				} else if hasDown {
					ch = '┬'
				} else if hasUp {
					ch = '┴'
				}
			} else if c.hasVert {
				_, hasLeft := cells[[2]int{x - 1, y}]
				_, hasRight := cells[[2]int{x + 1, y}]
				if hasLeft && hasRight {
					ch = '┼'
				} else if hasRight {
					ch = '├'
				} else if hasLeft {
					ch = '┤'
				}
			}
			chars = append(chars, charPos{x, y, ch})
		}
	}

	if len(chars) == 0 {
		return nil
	}

	// Group characters by row for efficient rendering
	rowChars := make(map[int][]charPos)
	for _, cp := range chars {
		rowChars[cp.y] = append(rowChars[cp.y], cp)
	}

	borderColor := theme.BorderUnfocused()
	r, g, b, _ := borderColor.RGBA()
	colorStr := fmt.Sprintf("\x1b[38;2;%d;%d;%dm", r>>8, g>>8, b>>8)
	reset := "\x1b[0m"

	// Build per-row layers
	var layers []*lipgloss.Layer
	for y, cps := range rowChars {
		// Build a sparse line: only separator chars, rest is empty
		var sb strings.Builder
		// Sort by X
		maxX := 0
		for _, cp := range cps {
			if cp.x > maxX {
				maxX = cp.x
			}
		}

		// Build character map for this row
		lineChars := make(map[int]rune)
		for _, cp := range cps {
			lineChars[cp.x] = cp.ch
		}

		// Find the leftmost character
		minX := viewW
		for _, cp := range cps {
			if cp.x < minX {
				minX = cp.x
			}
		}

		// Build the line from minX to maxX
		for x := minX; x <= maxX; x++ {
			if ch, ok := lineChars[x]; ok {
				sb.WriteString(colorStr)
				sb.WriteRune(ch)
				sb.WriteString(reset)
			} else {
				sb.WriteByte(' ')
			}
		}

		layer := lipgloss.NewLayer(sb.String()).
			X(minX).Y(y).
			Z(config.ZIndexSeparators).
			ID(fmt.Sprintf("sep-%d", y))
		layers = append(layers, layer)
	}

	return layers
}
