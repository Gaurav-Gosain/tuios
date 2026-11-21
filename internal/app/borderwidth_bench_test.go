package app

import (
	"fmt"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

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
