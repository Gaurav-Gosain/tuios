package app

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// renderSeparatorOverlay renders thin separator lines between tiled panes.
// Each separator line is its own lipgloss Layer to avoid occluding content.
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

	// Collect all separator characters with positions
	type cell struct{ vert, horiz bool }
	grid := make(map[[2]int]*cell)
	get := func(x, y int) *cell {
		k := [2]int{x, y}
		if c, ok := grid[k]; ok {
			return c
		}
		c := &cell{}
		grid[k] = c
		return c
	}

	for _, s := range splits {
		if s.Vertical {
			if s.Pos < 0 || s.Pos >= viewW {
				continue
			}
			for y := max(s.From, 0); y <= min(s.To, viewH-1); y++ {
				get(s.Pos, y).vert = true
			}
		} else {
			if s.Pos < 0 || s.Pos >= viewH {
				continue
			}
			for x := max(s.From, 0); x <= min(s.To, viewW-1); x++ {
				get(x, s.Pos).horiz = true
			}
		}
	}

	// Resolve each cell to a character
	type charPos struct {
		x, y int
		ch   rune
	}
	var chars []charPos

	for k, c := range grid {
		x, y := k[0], k[1]
		if c.vert && c.horiz {
			chars = append(chars, charPos{x, y, '┼'})
		} else if c.vert {
			// Check horizontal neighbors for T-junctions
			_, hasL := grid[[2]int{x - 1, y}]
			_, hasR := grid[[2]int{x + 1, y}]
			if hasL && hasR {
				chars = append(chars, charPos{x, y, '┼'})
			} else if hasR {
				chars = append(chars, charPos{x, y, '├'})
			} else if hasL {
				chars = append(chars, charPos{x, y, '┤'})
			} else {
				chars = append(chars, charPos{x, y, '│'})
			}
		} else if c.horiz {
			_, hasU := grid[[2]int{x, y - 1}]
			_, hasD := grid[[2]int{x, y + 1}]
			if hasU && hasD {
				chars = append(chars, charPos{x, y, '┼'})
			} else if hasD {
				chars = append(chars, charPos{x, y, '┬'})
			} else if hasU {
				chars = append(chars, charPos{x, y, '┴'})
			} else {
				chars = append(chars, charPos{x, y, '─'})
			}
		}
	}

	if len(chars) == 0 {
		return nil
	}

	// Build color string
	borderColor := theme.BorderUnfocused()
	r, g, b, _ := borderColor.RGBA()
	colorStr := fmt.Sprintf("\x1b[38;2;%d;%d;%dm", r>>8, g>>8, b>>8)
	reset := "\x1b[0m"

	// Group into contiguous horizontal runs to minimize layer count.
	// A "run" is a sequence of chars on the same row with consecutive X positions.
	type run struct {
		x, y int
		text string
	}

	// Sort chars by (y, x) for grouping
	// Simple insertion sort since count is small
	for i := 1; i < len(chars); i++ {
		for j := i; j > 0; j-- {
			if chars[j].y < chars[j-1].y || (chars[j].y == chars[j-1].y && chars[j].x < chars[j-1].x) {
				chars[j], chars[j-1] = chars[j-1], chars[j]
			} else {
				break
			}
		}
	}

	var runs []run
	i := 0
	for i < len(chars) {
		// Start a new run
		r := run{x: chars[i].x, y: chars[i].y}
		var sb strings.Builder
		sb.WriteString(colorStr)
		sb.WriteRune(chars[i].ch)
		j := i + 1
		for j < len(chars) && chars[j].y == r.y && chars[j].x == chars[j-1].x+1 {
			sb.WriteRune(chars[j].ch)
			j++
		}
		sb.WriteString(reset)
		r.text = sb.String()
		runs = append(runs, r)
		i = j
	}

	// Create one layer per run
	layers := make([]*lipgloss.Layer, len(runs))
	for idx, r := range runs {
		layers[idx] = lipgloss.NewLayer(r.text).
			X(r.x).Y(r.y).
			Z(config.ZIndexSeparators).
			ID(fmt.Sprintf("sep-%d-%d", r.y, r.x))
	}

	return layers
}
