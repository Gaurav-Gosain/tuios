package input

import (
	"fmt"
	"image/color"
	"sort"
	"strconv"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// rgb is a truecolor foreground, or ok false for any other foreground.
type rgb struct {
	r, g, b uint8
	ok      bool
}

func rgbOf(c color.Color) rgb {
	r, g, b, _ := c.RGBA()
	return rgb{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), true}
}

// frameFG walks one rendered frame and returns the truecolor foreground in
// effect at every cell, indexed by row then column. Only foreground state is
// tracked, which is all the separator check needs.
func frameFG(frame string) []map[int]rgb {
	var rows []map[int]rgb
	for _, line := range strings.Split(frame, "\n") {
		cells := map[int]rgb{}
		var fg rgb
		col := 0
		runes := []rune(line)
		for i := 0; i < len(runes); {
			if runes[i] == 0x1b && i+1 < len(runes) && runes[i+1] == '[' {
				j := i + 2
				for j < len(runes) && runes[j] != 'm' && (runes[j] == ';' || (runes[j] >= '0' && runes[j] <= '9')) {
					j++
				}
				if j < len(runes) && runes[j] == 'm' {
					fg = applySGR(fg, string(runes[i+2:j]))
					i = j + 1
					continue
				}
				i = j + 1
				continue
			}
			if fg.ok {
				cells[col] = fg
			}
			col++
			i++
		}
		rows = append(rows, cells)
	}
	return rows
}

// applySGR folds one SGR parameter string into the running foreground state.
func applySGR(fg rgb, params string) rgb {
	if params == "" {
		return rgb{}
	}
	fields := strings.Split(params, ";")
	for i := 0; i < len(fields); i++ {
		n, err := strconv.Atoi(fields[i])
		if err != nil {
			continue
		}
		switch {
		case n == 0 || n == 39:
			fg = rgb{}
		case n == 38 && i+4 < len(fields) && fields[i+1] == "2":
			r, _ := strconv.Atoi(fields[i+2])
			g, _ := strconv.Atoi(fields[i+3])
			b, _ := strconv.Atoi(fields[i+4])
			fg = rgb{uint8(r), uint8(g), uint8(b), true}
			i += 4
		case n == 38 && i+2 < len(fields) && fields[i+1] == "5":
			fg = rgb{}
			i += 2
		case n == 38:
			fg = rgb{}
		}
	}
	return fg
}

// unfocusedColumns lists the columns of one row that carry the unfocused border
// color. It only feeds the failure message, where the point is to show which
// column the divider actually landed on when it was not the focused pane's edge.
func unfocusedColumns(rows []map[int]rgb, y int, unfocused rgb) []int {
	if y < 0 || y >= len(rows) {
		return nil
	}
	var cols []int
	for x, c := range rows[y] {
		if c == unfocused {
			cols = append(cols, x)
		}
	}
	sort.Ints(cols)
	return cols
}

// TestSharedBorderDragKeepsFocusedSeparatorHighlighted is the regression guard
// for a visual artifact: during a shared-borders drag the focused pane's moving
// divider briefly rendered in the unfocused border color, so a red separator
// showed up beside the highlighted one and read as an afterimage.
//
// The overlay takes its two inputs from different places. Separator positions
// are collected from the BSP tree, while the highlight is the focused window's
// perimeter, taken from live window geometry. A frame composed while those two
// disagree draws the divider at the tree's stale position, which is no longer on
// the perimeter, so it loses the focus color.
//
// The drag is interleaved with PTY output because that is what makes the
// disagreement observable: terminal output composes a frame on a path that knows
// nothing about the drag, so it lands between a motion event and the ratio sync
// that motion deferred.
func TestSharedBorderDragKeepsFocusedSeparatorHighlighted(t *testing.T) {
	// The composed frame is only colored when the writer has a color profile,
	// and this check reads colors out of it.
	prevProfile := lipgloss.Writer.Profile
	lipgloss.Writer.Profile = colorprofile.TrueColor
	t.Cleanup(func() { lipgloss.Writer.Profile = prevProfile })
	app.SetInputHandler(HandleInput)

	prev := config.SharedBorders
	config.SharedBorders = true
	t.Cleanup(func() { config.SharedBorders = prev })

	m := benchResizeOS(t, 4)
	startX, startY := m.ResizeStartX, m.ResizeStartY

	unfocused := rgbOf(theme.BorderUnfocused())
	focused := rgbOf(theme.BorderFocusedWindow())
	if unfocused == focused {
		t.Fatal("theme border colors are identical; the check cannot tell them apart")
	}

	_ = m.View()

	var bad []string
	const steps = 24
	for i := 1; i <= steps; i++ {
		_, _ = m.Update(motionAt(startX-i, startY))
		// Terminal output during the drag: a frame the drag did not ask for.
		_, _ = m.Update(app.PTYDataMsg{})

		frame := m.View().Content
		win := m.GetFocusedWindow()
		if win == nil {
			t.Fatal("no focused window mid-drag")
		}
		edge := win.X + win.Width
		rows := frameFG(frame)

		reds, cyans := 0, 0
		for y := win.Y; y < win.Y+win.Height && y < len(rows); y++ {
			switch rows[y][edge] {
			case unfocused:
				reds++
			case focused:
				cyans++
			}
		}
		if reds > 0 || cyans == 0 {
			bad = append(bad, fmt.Sprintf("frame %d (edge x=%d): %d unfocused cells, %d focused cells; unfocused columns on row %d: %v",
				i, edge, reds, cyans, win.Y+1, unfocusedColumns(rows, win.Y+1, unfocused)))
		}
	}

	if len(bad) > 0 {
		t.Fatalf("the focused pane's divider was not drawn in the focus color on %d of %d frames:\n  %s",
			len(bad), steps, strings.Join(bad, "\n  "))
	}
}
