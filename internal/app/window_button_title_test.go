package app

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/charmbracelet/x/ansi"
)

// When the title shares the top bar with the controls, the two take opposite
// ends: putting the badge next to the pill would make a press aimed at one land
// near the other, and it would leave the bar's whole run of border on the far
// side doing nothing.
//
// And when they do not both fit, the badge is the one that goes, at either end.
// A name the bar cannot show is still on the dock and in the palette; a close
// button nobody can press is gone.

// withTopTitle runs fn with window titles on the top border and restores the
// global.
func withTopTitle(t *testing.T, fn func()) {
	t.Helper()
	prev := config.WindowTitlePosition
	config.WindowTitlePosition = "top"
	t.Cleanup(func() { config.WindowTitlePosition = prev })
	fn()
}

func TestTitleBadgeTakesTheEndTheControlsDidNot(t *testing.T) {
	withTopTitle(t, func() {
		for _, style := range config.WindowButtonStyles {
			for _, position := range config.WindowButtonPositions {
				withButtonPosition(t, position, func() {
					withButtonStyle(t, style, func() {
						win := &terminal.Window{
							ID: "w", X: 0, Y: 0, Width: 60, Height: 10,
							Workspace: 1, CustomName: "editor",
						}
						m := &OS{Windows: []*terminal.Window{win}}
						cols, rects := drawTopBorder(t, m, win, false)
						row := string(cols)

						name := strings.Index(row, "editor")
						if name < 0 {
							t.Fatalf("%s/%s: the bar drew no title: %q", style, position, row)
						}
						lo := rects[0].X
						for _, r := range rects[1:] {
							lo = min(lo, r.X)
						}

						// The badge is measured against the pill's nearest
						// control, which is enough to say which end each took.
						if position == config.WindowButtonPositionLeft && name <= lo {
							t.Errorf("%s/left: the title is at column %d, left of the controls at %d",
								style, name, lo)
						}
						if position == config.WindowButtonPositionRight && name >= lo {
							t.Errorf("%s/right: the title is at column %d, right of the controls at %d",
								style, name, lo)
						}
					})
				})
			}
		}
	})
}

func TestANarrowBarKeepsItsControlsAtTheNamedEnd(t *testing.T) {
	withTopTitle(t, func() {
		for _, style := range config.WindowButtonStyles {
			for _, position := range config.WindowButtonPositions {
				withButtonPosition(t, position, func() {
					withButtonStyle(t, style, func() {
						// Too narrow for a name of any useful length, so the
						// bar shows the controls alone. They still go to the
						// end the setting names, not back to the right.
						win := &terminal.Window{
							ID: "w", X: 5, Y: 2, Width: 18, Height: 8,
							Workspace: 1, CustomName: strings.Repeat("long", 30),
						}
						m := &OS{Windows: []*terminal.Window{win}}
						pill, pillHits := m.buildWindowButtons(lipgloss.Color("#7dd3fc"), win, false)
						cols, rects := drawTopBorder(t, m, win, false)

						if len(rects) != len(pillHits) {
							t.Fatalf("%s/%s: a squeezed bar recorded %d controls, want the %d it drew",
								style, position, len(rects), len(pillHits))
						}

						want := win.X + 1
						if position == config.WindowButtonPositionRight {
							want = win.X + len(cols) - 1 - lipgloss.Width(pill)
						}
						for i, h := range pillHits {
							if got := rects[i]; got.X != want+h.X {
								t.Errorf("%s/%s: with no room for a title, %v is at column %d, want %d",
									style, position, got.Action, got.X, want+h.X)
							}
						}
						if strings.Contains(string(cols), "long") {
							t.Errorf("%s/%s: the bar kept part of a title it had no room for: %q",
								style, position, string(cols))
						}
					})
				})
			}
		}
	})
}

// getWindowTitle reserves the pill's width before it truncates, so today no
// window reaches layoutBorderRow with a badge that overruns the bar. This pins
// the fallback anyway, at the layout, because it is the branch that decides
// which of the two the user keeps when a caller stops reserving.
func TestABadgeThatDoesNotFitGivesWayToTheControls(t *testing.T) {
	col := lipgloss.Color("#7dd3fc")
	pill := "[-o x]"
	badge := "(a name far too long for this bar)"

	for _, position := range config.WindowButtonPositions {
		withButtonPosition(t, position, func() {
			const width = 20
			row := layoutBorderRow(badge, pill, width, col, true)
			text := ansi.Strip(row.text)

			if strings.Contains(text, "name") {
				t.Errorf("%s: the badge survived a bar with no room for it: %q", position, text)
			}
			if !strings.Contains(text, pill) {
				t.Errorf("%s: the controls were dropped instead of the badge: %q", position, text)
			}
			if got := lipgloss.Width(row.text); got != width+2 {
				t.Errorf("%s: the row came out %d cells, want %d", position, got, width+2)
			}

			want := 1
			if position == config.WindowButtonPositionRight {
				want = width + 1 - lipgloss.Width(pill)
			}
			if row.pillStart != want {
				t.Errorf("%s: the controls were reported at column %d, want %d",
					position, row.pillStart, want)
			}
			// Indexed by column, not by byte: the border fill is a multi-byte
			// rune, so a byte offset into the row is not the column it names.
			if got := string([]rune(text)[row.pillStart:][:lipgloss.Width(pill)]); got != pill {
				t.Errorf("%s: the row holds %q at the reported column %d, want the controls %q",
					position, got, row.pillStart, pill)
			}
		})
	}
}

// The bottom border draws the title on its own bottom corners. It carries no
// controls, so the button position leaves it alone, and it never borrowed the
// top border's corner glyphs.
func TestBottomTitleBarIsUnaffectedByTheButtonPosition(t *testing.T) {
	prev := config.WindowTitlePosition
	config.WindowTitlePosition = "bottom"
	t.Cleanup(func() { config.WindowTitlePosition = prev })

	var rows []string
	for _, position := range config.WindowButtonPositions {
		withButtonPosition(t, position, func() {
			win := &terminal.Window{
				ID: "w", X: 0, Y: 0, Width: 60, Height: 10,
				Workspace: 1, CustomName: "editor",
			}
			m := &OS{Windows: []*terminal.Window{win}}
			out := m.addToBorder(strings.Repeat(" ", win.Width), lipgloss.Color("#7dd3fc"), win, 1, false)
			lines := strings.Split(ansi.Strip(out), "\n")
			rows = append(rows, lines[len(lines)-1])
		})
	}

	if rows[0] != rows[1] {
		t.Errorf("the button position changed the bottom bar:\n right: %q\n  left: %q", rows[0], rows[1])
	}
	if !strings.HasPrefix(rows[0], config.GetWindowBorderBottomLeft()) ||
		!strings.HasSuffix(rows[0], config.GetWindowBorderBottomRight()) {
		t.Errorf("the bottom bar is not drawn on its own corners: %q", rows[0])
	}
}
