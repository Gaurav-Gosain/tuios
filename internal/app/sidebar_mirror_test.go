package app

import (
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// The rule the whole mirroring pass generalises to: arrows point where the rail
// will go, controls hug the pane-facing corner, and text never mirrors.

// TestMirrorArrowsPointWhereTheRailWillGo walks the table: two positions by two
// states, in both glyph sets. An arrow pointing the wrong way is a control that
// lies about what it does.
func TestMirrorArrowsPointWhereTheRailWillGo(t *testing.T) {
	for _, ascii := range []bool{false, true} {
		prev := config.Global.UseASCIIOnly
		config.Global.UseASCIIOnly = ascii
		overlay.SetASCII(ascii)
		t.Cleanup(func() {
			config.Global.UseASCIIOnly = prev
			overlay.SetASCII(prev)
		})

		left, right := "«", "»"
		if ascii {
			left, right = "<<", ">>"
		}
		for _, tc := range []struct {
			pos     string
			variant int
			want    string
		}{
			{"left", sidebarVariantFull, left},   // collapses leftward
			{"left", sidebarVariantGlyph, right}, // reopens rightward
			{"right", sidebarVariantFull, right}, // collapses rightward
			{"right", sidebarVariantGlyph, left}, // reopens leftward
		} {
			m := sidebarTestOS(t, 120, 30, tc.pos)
			withSidebar(t, true, tc.pos, config.SidebarDefaultWidth)
			m.Settings = config.Global
			got, ok := m.sidebarCollapseGlyph(tc.variant)
			if !ok {
				t.Errorf("%s/%d: the control is not offered at all", tc.pos, tc.variant)
				continue
			}
			if got != tc.want {
				t.Errorf("%s rail, variant %d, ascii=%v: arrow %q, want %q", tc.pos, tc.variant, ascii, got, tc.want)
			}
		}
	}
}

// TestMirrorStripToggleHugsThePaneFacingColumn: the strip has one control and
// two columns, so which of them its glyph lands on is the whole decision. The
// zone behind it takes both columns either way, because a one-cell target is
// not a control.
func TestMirrorStripToggleHugsThePaneFacingColumn(t *testing.T) {
	for _, pos := range []string{"left", "right"} {
		m, tree := stripOS(t, 120, 20)
		withSidebar(t, true, pos, config.SidebarDefaultWidth)
		m.Settings = config.Global
		m.SidebarCollapsed = true
		lines, w := m.sidebarPanelLinesForTree(tree)

		var toggle sidebarRowHit
		for _, h := range m.SidebarHits {
			if h.Kind == sidebarRowCollapse {
				toggle = h
			}
		}
		if toggle.X1 == 0 {
			t.Fatalf("%s: the strip drew no toggle", pos)
		}
		railX0 := 0
		if pos == "right" {
			railX0 = m.GetRenderWidth() - w
		}
		// The zone is the whole band, edge rule included: the glyph sits against
		// the pane-facing edge, but a three-column rail cannot afford to spend a
		// third of its width on a column that only resizes.
		if toggle.X0 != railX0 || toggle.X1 != railX0+w {
			t.Errorf("%s: the strip toggle zone is %d..%d, want %d..%d", pos, toggle.X0, toggle.X1, railX0, railX0+w)
		}

		// The glyph itself still sits against the pane-facing edge.
		line := []rune(stripANSIForTrace(lines[toggle.Y0-m.GetTopMargin()]))
		glyph, _ := m.sidebarCollapseGlyph(sidebarVariantGlyph)
		at := w - 1 - len([]rune(glyph))
		if pos == "right" {
			at = 1
		}
		if got := string(line[at : at+len([]rune(glyph))]); got != glyph {
			t.Errorf("%s: the toggle glyph is %q at column %d, want %q", pos, got, at, glyph)
		}
	}
}

// TestMirrorEdgeRuleStaysOnThePaneFacingColumn: the rail's own frame is the one
// thing that was already mirrored, and the pass must not have moved it.
func TestMirrorEdgeRuleStaysOnThePaneFacingColumn(t *testing.T) {
	rule := config.Global.GetWindowBorderLeft()
	for _, pos := range []string{"left", "right"} {
		m, tree := sectionsTestOS(t, 120, 30)
		withSidebar(t, true, pos, config.SidebarDefaultWidth)
		m.Settings = config.Global
		lines := railPlain(t, m, tree)
		w := m.GetSidebarWidth()

		for i, l := range lines {
			if lipgloss.Width(l) != w {
				t.Fatalf("%s line %d is %d cells, want %d", pos, i, lipgloss.Width(l), w)
			}
			runes := []rune(l)
			if pos == "left" && string(runes[len(runes)-1]) != rule {
				t.Fatalf("left rail line %d does not end on the edge rule: %q", i, l)
			}
			if pos == "right" && string(runes[0]) != rule {
				t.Fatalf("right rail line %d does not start on the edge rule: %q", i, l)
			}
		}
	}
}
