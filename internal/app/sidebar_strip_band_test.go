package app

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// bandCells is the set of cells the strip paints in one ground, as
// "column,row" keys against the rail's own origin. Reading the ground back out
// of the frame is the point: the claim under test is about what the user can
// see, and the layout arithmetic that produced it is exactly what would agree
// with a wrong answer.
func bandCells(lines []string, ground string) map[string]bool {
	out := map[string]bool{}
	for y, line := range lines {
		for x, cell := range stripCells(line) {
			if bgOf(cell) == ground {
				out[fmt.Sprintf("%d,%d", x, y)] = true
			}
		}
	}
	return out
}

// rectCells is the same set for a recorded hit rectangle.
func rectCells(h sidebarRowHit, railX0, topMargin int) map[string]bool {
	out := map[string]bool{}
	for y := h.Y0; y < h.Y1; y++ {
		for x := h.X0; x < h.X1; x++ {
			out[fmt.Sprintf("%d,%d", x-railX0, y-topMargin)] = true
		}
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestStripHoverBandIsTheHitRectangle is the acceptance criterion for the
// collapsed rail's pointer feedback: what you see is what you can hit. The
// drawn band and the recorded rectangle are asserted as one set of cells rather
// than measured apart, because they were each individually correct while
// disagreeing with each other. The rectangle was widened to the whole band and
// the fill was not, so the target was a column bigger than it looked, on the
// pane-facing side the pointer arrives from.
func TestStripHoverBandIsTheHitRectangle(t *testing.T) {
	for _, pos := range []string{"left", "right"} {
		t.Run(pos, func(t *testing.T) {
			m, tree := stripOS(t, 120, 24)
			withSidebar(t, true, pos, config.SidebarDefaultWidth)
			m.SidebarCollapsed = true

			m.sidebarPanelLinesForTree(tree)
			targets := append([]sidebarRowHit(nil), m.SidebarHits...)
			if len(targets) < 4 {
				t.Fatalf("the fixture drew %d targets, too few to be a test", len(targets))
			}
			seen := map[sidebarRowKind]bool{}

			w, top := m.GetSidebarWidth(), m.GetTopMargin()
			railX0 := 0
			if pos == "right" {
				railX0 = m.GetRenderWidth() - w
			}
			panel := panelSGR(t)

			for _, h := range targets {
				seen[h.Kind] = true
				m.SidebarHoverActive = true
				m.SidebarHoverX, m.SidebarHoverY = railX0, h.Y0
				lines, _ := m.sidebarPanelLinesForTree(tree)

				// The ground the target is painted in, read off the first cell of the
				// rectangle. Which colour it is belongs to the row (the alarm badge
				// keeps its own ink); that it is not the strip's resting ground, and
				// that it stops exactly where the rectangle does, is the invariant.
				ground := bgOf(stripCells(lines[h.Y0-top])[h.X0-railX0])
				if ground == panel || ground == "" {
					t.Errorf("%v target at row %d draws no band at all", h.Kind, h.Y0-top)
					continue
				}
				painted, want := bandCells(lines, ground), rectCells(h, railX0, top)
				if len(painted) != len(want) {
					t.Errorf("%v target at row %d: painted %v, recorded %v",
						h.Kind, h.Y0-top, sortedKeys(painted), sortedKeys(want))
					continue
				}
				for cell := range want {
					if !painted[cell] {
						t.Errorf("%v target at row %d: cell %s is inside the hit rectangle and unpainted",
							h.Kind, h.Y0-top, cell)
					}
				}
			}

			// The fixture has to have exercised the kinds that differ in shape: a
			// two-row session slot, a one-row control, and the inked badge.
			for _, kind := range []sidebarRowKind{sidebarRowSession, sidebarRowCollapse, sidebarRowAgent} {
				if !seen[kind] {
					t.Errorf("no %v target on the strip, so its band went untested", kind)
				}
			}
		})
	}
}

// TestStripHoverPaintsNothingOffTarget: the rows carrying no target are the
// pads, the slack between the two lists and the group's rule. A band on one of
// them offers a hitbox that is not there, which is the same lie as a band
// narrower than its rectangle told the other way round.
func TestStripHoverPaintsNothingOffTarget(t *testing.T) {
	m, tree := stripOS(t, 120, 24)
	m.SidebarCollapsed = true
	m.sidebarPanelLinesForTree(tree)

	top := m.GetTopMargin()
	targeted := map[int]bool{}
	for _, h := range m.SidebarHits {
		for y := h.Y0; y < h.Y1; y++ {
			targeted[y] = true
		}
	}

	panel := panelSGR(t)
	for i := range m.GetUsableHeight() {
		y := top + i
		if targeted[y] {
			continue
		}
		m.SidebarHoverActive = true
		m.SidebarHoverX, m.SidebarHoverY = 0, y
		lines, _ := m.sidebarPanelLinesForTree(tree)
		for x, cell := range stripCells(lines[i]) {
			if bg := bgOf(cell); bg != panel {
				t.Errorf("row %d carries no target, but hovering it painted cell (%d,%d) %q", i, x, i, bg)
			}
		}
	}
}

// TestStripHoverBandSpansEveryColumnOnBothSides is the regression in its own
// words, on the column that was missing: the pane-facing one, which is the edge
// rule's, and which is mirrored to the other end of the band on a right-hand
// rail.
func TestStripHoverBandSpansEveryColumnOnBothSides(t *testing.T) {
	for _, pos := range []string{"left", "right"} {
		t.Run(pos, func(t *testing.T) {
			m, tree := quietStripOS(t, 120, 20)
			withSidebar(t, true, pos, config.SidebarDefaultWidth)
			m.SidebarCollapsed = true
			m.sidebarPanelLinesForTree(tree)

			var slot sidebarRowHit
			for _, h := range m.SidebarHits {
				if h.Kind == sidebarRowSession && h.SessionID == "api" {
					slot = h
				}
			}
			w, top := m.GetSidebarWidth(), m.GetTopMargin()
			railX0 := 0
			if pos == "right" {
				railX0 = m.GetRenderWidth() - w
			}

			m.SidebarHoverActive = true
			m.SidebarHoverX, m.SidebarHoverY = railX0, slot.Y0
			lines, _ := m.sidebarPanelLinesForTree(tree)

			panel := panelSGR(t)
			for y := slot.Y0; y < slot.Y1; y++ {
				cells := stripCells(lines[y-top])
				if len(cells) != w {
					t.Fatalf("row %d is %d cells, want the band's %d", y-top, len(cells), w)
				}
				for x, cell := range cells {
					if bgOf(cell) == panel {
						t.Errorf("cell (%d,%d) of the hovered slot kept the resting ground", x, y-top)
					}
				}
			}
		})
	}
}

// TestStripHoverBandSurvivesASeverityMark: a row carrying an alarm is drawn
// exactly like a quiet one under the pointer, because the band is the ground and
// the mark is the message. The pointer does not repaint an alarm and the alarm
// does not eat the band.
func TestStripHoverBandSurvivesASeverityMark(t *testing.T) {
	m, tree := stripOS(t, 120, 24)
	m.SidebarCollapsed = true
	m.sidebarPanelLinesForTree(tree)

	var loud, quiet sidebarRowHit
	for _, h := range m.SidebarHits {
		switch h.SessionID {
		case "api": // carries needs_input and errored panes
			loud = h
		case "docs":
			quiet = h
		}
	}
	if loud.Y1 == 0 || quiet.Y1 == 0 {
		t.Fatal("the fixture lost one of the two session rows")
	}

	top := m.GetTopMargin()
	grounds := map[string]string{}
	for name, h := range map[string]sidebarRowHit{"api": loud, "docs": quiet} {
		m.SidebarHoverActive = true
		m.SidebarHoverX, m.SidebarHoverY = 0, h.Y0
		lines, _ := m.sidebarPanelLinesForTree(tree)
		grounds[name] = bgOf(stripCells(lines[h.Y0-top])[0])
	}
	if grounds["api"] != grounds["docs"] {
		t.Errorf("the alarming row's band is %q and the quiet row's is %q; the band is the ground, not a message",
			grounds["api"], grounds["docs"])
	}

	// The mark itself keeps its severity colour under the pointer, so hovering
	// cannot quiet an alarm.
	m.SidebarHoverX, m.SidebarHoverY = 0, loud.Y0
	lines, _ := m.sidebarPanelLinesForTree(tree)
	if !strings.Contains(lines[loud.Y0-top], fgParams(sidebarSeverityColor("needs_input", theme.UI()))) {
		t.Errorf("hovering the alarming row repainted its mark: %q", lines[loud.Y0-top])
	}
}

// TestStripTooltipAnchorsOnTheRowItNames: the label and the band are drawn on
// the same ground, so they read as one object only if they start on the same
// line. Every list on the strip is worth a label, and each has to land level
// with its own row.
func TestStripTooltipAnchorsOnTheRowItNames(t *testing.T) {
	m, tree := stripOS(t, 120, 24)
	m.SidebarCollapsed = true
	m.sidebarPanelLinesForTree(tree)

	seen := map[sidebarStripRowKind]bool{}
	for _, r := range m.sidebarStripRows {
		if r.Label == "" {
			t.Errorf("the %v row at %d says nothing", r.Kind, r.Y0)
			continue
		}
		seen[r.Kind] = true
		m.Tooltip = tooltipState{Source: tooltipRailStrip, Key: r.Y0, Shown: true}
		layer := m.renderRailTooltip()
		if layer == nil {
			t.Fatalf("hovering the %v row at %d showed no label", r.Kind, r.Y0)
		}
		if got := layer.GetY(); got != r.Y0 {
			t.Errorf("the %v row's label sits on row %d, want its own row %d", r.Kind, got, r.Y0)
		}
	}
	for _, kind := range []sidebarStripRowKind{sidebarStripSession, sidebarStripTerminal, sidebarStripAgent} {
		if !seen[kind] {
			t.Errorf("no %v row was recorded, so nothing names it under the pointer", kind)
		}
	}
}
