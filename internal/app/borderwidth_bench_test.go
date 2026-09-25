package app

// What it costs to measure a box that has already been sized.
//
// A profile of the flood benchmark puts ansi.StringWidth at 43.75% cumulative,
// and one of the five passes over the content is tuios's own: addToBorder
// opens with
//
//	width := max(lipgloss.Width(content)-2, 0)
//
// where content is the whole rendered box. lipgloss.Width walks every row
// through a grapheme-cluster iterator to find the widest one, so a 40-row pane
// pays 40 full-width scans of styled text, per pane, per frame, to recover a
// number the caller already holds: the same call takes *terminal.Window, whose
// Width is the box's width by construction.
//
// These two benchmarks are the same question asked twice: what the scan costs,
// and what knowing the answer costs. The gap is the size of the saving, and
// once the call site reads the width off the window instead, the ratio here is
// what proves it stayed fixed.
//
// The call site now passes window.ContentWidth(), and
// TestBorderBoxInnerWidthIsKnown below is the differential proof that the
// cheap answer is the measured one.

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// benchBox builds a rendered pane box of a realistic size and style density:
// a border, and rows carrying colour changes the way a real frame does, so the
// width scan has escape sequences to step over rather than plain ASCII.
func benchBox(cols, rows int) string {
	var b strings.Builder
	b.Grow(cols * rows * 8)
	b.WriteString("╭" + strings.Repeat("─", cols-2) + "╮\n")
	for y := range rows - 2 {
		b.WriteString("│")
		// Three style runs per row is typical of a shell prompt plus output.
		seg := (cols - 2) / 3
		for i := range 3 {
			fmt.Fprintf(&b, "\x1b[38;5;%dm", 8+(y+i)%200)
			n := seg
			if i == 2 {
				n = (cols - 2) - 2*seg
			}
			b.WriteString(strings.Repeat("x", n))
		}
		b.WriteString("\x1b[m│\n")
	}
	b.WriteString("╰" + strings.Repeat("─", cols-2) + "╯")
	return b.String()
}

// TestBorderBoxInnerWidthIsKnown is what licenses addToBorder to be told its
// width instead of measuring one. It builds the border box exactly as
// renderWindowBox does and checks that the number the call site passes,
// window.ContentWidth(), is the number the scan would have returned.
//
// The matrix is the two things that could break the identity. Sizes go down to
// a one-column pane, where the content box clamps and the rendered box comes
// out wider than the pane itself; and every pre-shaped case is replayed,
// because a wide rune, a combining mark or a joined emoji is where a column
// count and a byte count part company. Both border-box paths run, since only
// one of them sets Width on the style and it is the other one whose width is
// implied by its content.
func TestBorderBoxInnerWidthIsKnown(t *testing.T) {
	check := func(t *testing.T, win *terminal.Window, m *OS, label string) {
		t.Helper()
		for _, noPreShape := range []bool{false, true} {
			preShapedDisabled = noPreShape
			win.InvalidateCache()
			win.MarkContentDirty()
			content := m.renderTerminal(win, true, true)
			preShaped := !preShapedDisabled &&
				win.RenderedCols == win.ContentWidth() &&
				win.RenderedRows == win.ContentHeight()
			box := sizeContentBox(lipgloss.NewStyle().
				Align(lipgloss.Left).
				AlignVertical(lipgloss.Top).
				Border(getBorder(&config.Global)).
				BorderTop(false), win, preShaped)
			rendered := box.BorderForeground(lipgloss.Color("62")).Render(content)
			measured := max(lipgloss.Width(rendered)-2, 0)
			if known := win.ContentWidth(); known != measured {
				t.Errorf("%s (preShaped=%v): the box measures %d columns inside, the call site passes %d",
					label, preShaped, measured, known)
			}
		}
		preShapedDisabled = false
	}

	for _, w := range []int{1, 2, 3, 4, 5, 10, 61, 62, 158} {
		for _, h := range []int{1, 2, 3, 4, 10, 42} {
			win := newTestWindow(t, fmt.Sprintf("size-%dx%d", w, h), w, h)
			m := newTestOS(win)
			m.Mode = TerminalMode
			check(t, win, m, fmt.Sprintf("%dx%d", w, h))
		}
	}

	for _, tc := range preShapedCases {
		win := preShapedWindow(t, "width-"+tc.name, tc.text)
		m := newTestOS(win)
		m.Mode = TerminalMode
		check(t, win, m, tc.name)
	}
}
