package app

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
)

// A border style is a set of glyphs drawn to go together, and a divider is drawn
// in the style the frame is drawn in. Whatever it puts in a cell has to come out
// of that set, at every junction the layout can put it in: the chrome's rule on
// any of the four sides, another divider crossing it, and the screen edge with
// no rule to meet. These read the composed frame, because the glyph in the cell
// is chosen from the style's table, the focused perimeter and the chrome at once.

// styleGlyphs is every glyph the style draws its own frame with. A divider cell
// outside this set is borrowed from another style, which is the failure being
// guarded: a box-drawing tee welded onto a bar of blocks.
func styleGlyphs(b lipgloss.Border) string {
	return b.Top + b.Bottom + b.Left + b.Right +
		b.TopLeft + b.TopRight + b.BottomLeft + b.BottomRight +
		b.Middle + b.MiddleTop + b.MiddleBottom + b.MiddleLeft + b.MiddleRight
}

// junctionGlyphs is what the style draws a meeting of two dividers with: its own
// junction set where it has one, and its plain divider glyphs where it is drawn
// with fills, which have no arms to draw a meeting with.
func junctionGlyphs(b lipgloss.Border) string {
	j := b.Middle + b.MiddleTop + b.MiddleBottom + b.MiddleLeft + b.MiddleRight
	if j == "" {
		return b.Left + b.Top
	}
	return j
}

// dividerCells lists every cell the dividers of this layout own, clipped to the
// content region: the whole of each division, both ends included.
func dividerCells(m *OS) []layout.Rect {
	b := m.GetBSPBounds()
	var cells []layout.Rect
	for _, s := range m.separatorSplits() {
		if s.Vertical {
			for y := max(s.From, b.Y); y <= min(s.To, b.Y+b.H-1); y++ {
				cells = append(cells, layout.Rect{X: s.Pos, Y: y})
			}
			continue
		}
		for x := max(s.From, b.X); x <= min(s.To, b.X+b.W-1); x++ {
			cells = append(cells, layout.Rect{X: x, Y: s.Pos})
		}
	}
	return cells
}

func sidebarSides() []string { return []string{"left", "right", ""} }

// TestDividerCellsStayInTheStylesOwnGlyphs walks every style the settings page
// offers against every shape the content region takes, so a style added to the
// list is asked the same question without anyone rewriting this.
func TestDividerCellsStayInTheStylesOwnGlyphs(t *testing.T) {
	for _, style := range config.BorderStyles {
		for _, dock := range []string{"top", "bottom", "hidden"} {
			for _, side := range sidebarSides() {
				t.Run(fmt.Sprintf("%s/%s-dock/%s", style, dock, sidebarName(side)), func(t *testing.T) {
					m := extentOSStyled(t, 4, dock, side, style)
					own := styleGlyphs(config.GetBorderForStyle())
					g := frameCells(t, m)
					for _, c := range dividerCells(m) {
						if got := cellAt(g, c.X, c.Y); !strings.ContainsRune(own, got) {
							t.Errorf("the divider cell (%d,%d) is %q, which is not one of this style's own glyphs %q",
								c.X, c.Y, string(got), own)
						}
					}
				})
			}
		}
	}
}

// TestDividerMeetsTheChromeRuleInItsOwnTerms is the question the junction cell
// answers: a style drawn with strokes carries one cell onto the rule and joins
// it, and a style drawn with fills leaves the rule alone, having already inked
// its last cell up to the boundary. Either way nothing foreign lands on the rule.
func TestDividerMeetsTheChromeRuleInItsOwnTerms(t *testing.T) {
	for _, style := range config.BorderStyles {
		for _, dock := range []string{"top", "bottom"} {
			t.Run(style+"/"+dock+"-dock", func(t *testing.T) {
				m := extentOSStyled(t, 2, dock, "", style)
				border := config.GetBorderForStyle()
				own := styleGlyphs(border)
				rule := firstRune(config.GetWindowSeparatorChar(), '─')
				s := firstSplit(t, m, true)
				got := cellAt(frameCells(t, m), s.Pos, dockRuleRow(m))

				if config.BorderFillsCells() {
					if got != rule {
						t.Errorf("the dock's rule under the divider is %q, want the rule's own %q: a fill has no stroke to join, so it stops at the boundary",
							string(got), string(rule))
					}
					return
				}
				if !strings.ContainsRune(own, got) {
					t.Errorf("the divider meets the dock's rule with %q, which is not one of this style's own glyphs %q",
						string(got), own)
				}
			})
		}
	}
}

// TestDividerCellsInASCII: with the frame held to ASCII every divider cell has to
// be drawable in it, whatever style is configured.
func TestDividerCellsInASCII(t *testing.T) {
	for _, style := range config.BorderStyles {
		t.Run(style, func(t *testing.T) {
			ascii := config.UseASCIIOnly
			t.Cleanup(func() { config.UseASCIIOnly = ascii })
			m := extentOSStyled(t, 4, "bottom", "right", style)
			config.UseASCIIOnly = true
			g := frameCells(t, m)
			for _, c := range dividerCells(m) {
				if got := cellAt(g, c.X, c.Y); got > 127 {
					t.Errorf("the divider cell (%d,%d) is %q, which ASCII cannot draw", c.X, c.Y, string(got))
				}
			}
		})
	}
}
