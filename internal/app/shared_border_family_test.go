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
					own := styleGlyphs(config.Global.GetBorderForStyle())
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
// it, and every other style leaves the rule to the chrome that drew it. Either
// way nothing foreign lands on the rule.
func TestDividerMeetsTheChromeRuleInItsOwnTerms(t *testing.T) {
	for _, style := range config.BorderStyles {
		for _, dock := range []string{"top", "bottom"} {
			t.Run(style+"/"+dock+"-dock", func(t *testing.T) {
				m := extentOSStyled(t, 2, dock, "", style)
				border := config.Global.GetBorderForStyle()
				own := styleGlyphs(border)
				rule := firstRune(config.Global.GetWindowSeparatorChar(), '─')
				s := firstSplit(t, m, true)
				got := cellAt(frameCells(t, m), s.Pos, dockRuleRow(m))

				if !config.Global.BorderJoinsChromeRules() {
					if got != rule {
						t.Errorf("the dock's rule under the divider is %q, want the rule's own %q: this style has no stroke to join it with, so it stops at the boundary",
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

// stackedOS lays two panes one above the other across the whole content region,
// so the divider between them runs the region's full width and ends on whatever
// closes the region on the left and on the right. The tilers both put a three
// pane stack in one half, which leaves the far rail untouched.
func stackedOS(t *testing.T, sidebar, style string) *OS {
	t.Helper()
	m := extentOSStyled(t, 2, "bottom", sidebar, style)
	m.UseBSPLayout = false
	b := m.GetBSPBounds()
	h := (b.H - 1) / 2
	top, bottom := m.Windows[0], m.Windows[1]
	top.X, top.Y, top.Width, top.Height = b.X, b.Y, b.W, h
	bottom.X, bottom.Y, bottom.Width, bottom.Height = b.X, b.Y+h+1, b.W, b.H-h-1
	return m
}

// TestDividerMeetsTheRailEdgeOnEitherSide: the rail closes the content region on
// whichever side it sits, and a division that runs the region's whole width ends
// on that rule from either direction.
func TestDividerMeetsTheRailEdgeOnEitherSide(t *testing.T) {
	for _, style := range config.BorderStyles {
		for _, side := range []string{"left", "right"} {
			t.Run(style+"/"+side+"-sidebar", func(t *testing.T) {
				m := stackedOS(t, side, style)
				b := m.GetBSPBounds()
				border := config.Global.GetBorderForStyle()
				s := firstSplit(t, m, false)
				if s.From > b.X || s.To < b.X+b.W-1 {
					t.Fatalf("this divider spans columns %d-%d, not the region's %d-%d; it cannot answer for either edge",
						s.From, s.To, b.X, b.X+b.W-1)
				}
				g := frameCells(t, m)

				edge, want, caps := b.X+b.W, firstRune(border.MiddleRight, '┤'), border.TopRight+border.BottomRight
				if side == "left" {
					edge, want, caps = b.X-1, firstRune(border.MiddleLeft, '├'), border.TopLeft+border.BottomLeft
				}
				if !config.Global.BorderJoinsChromeRules() {
					want = firstRune(config.Global.GetWindowBorderLeft(), '│')
					caps = ""
				}
				if got := cellAt(g, edge, s.Pos); got != want && !strings.ContainsRune(caps, got) {
					t.Errorf("the divider meets the rail's edge rule at column %d with %q, want %q or one of %q",
						edge, string(got), string(want), caps)
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
			ascii := config.Global.UseASCIIOnly
			t.Cleanup(func() { config.Global.UseASCIIOnly = ascii })
			m := extentOSStyled(t, 4, "bottom", "right", style)
			config.Global.UseASCIIOnly = true
			m.Settings.UseASCIIOnly = true
			g := frameCells(t, m)
			for _, c := range dividerCells(m) {
				if got := cellAt(g, c.X, c.Y); got > 127 {
					t.Errorf("the divider cell (%d,%d) is %q, which ASCII cannot draw", c.X, c.Y, string(got))
				}
			}
		})
	}
}
