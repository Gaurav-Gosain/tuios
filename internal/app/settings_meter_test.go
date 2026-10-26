package app

import (
	"math"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/charmbracelet/x/ansi"
)

// TestTheGaugeDoesNotFollowTheScrollbarStyle pins that the settings gauge draws
// the same glyphs whatever a pane's scrollbar is set to.
//
// The two used to share a pair. Setting appearance.scrollbar.style to track
// swapped every gauge on the page to the block thumb, and because that style's
// track glyph is deliberately the empty string, the unfilled half of each gauge
// vanished. A row's control then took as many cells as its own value, so no two
// rows lined up.
func TestTheGaugeDoesNotFollowTheScrollbarStyle(t *testing.T) {
	pal := overlay.Palette{}
	bg := pal.Surface

	var widths []int
	rendered := map[string]string{}
	for _, style := range config.ScrollbarStyles {
		s := config.DefaultSettings()
		s.ScrollbarStyle = style
		prev := config.Global
		config.Global = s
		var got []string
		for _, f := range []float64{0, 0.25, 0.5, 1} {
			out := settingsMeter(f, true, bg, pal)
			got = append(got, ansi.Strip(out))
			widths = append(widths, lipgloss.Width(out))
		}
		config.Global = prev
		rendered[style] = strings.Join(got, "|")
	}

	first := rendered[config.ScrollbarStyles[0]]
	for _, style := range config.ScrollbarStyles[1:] {
		if rendered[style] != first {
			t.Errorf("scrollbar style %q changed the settings gauge:\n %q\nwant %q",
				style, rendered[style], first)
		}
	}
	for _, w := range widths {
		if w != widths[0] {
			t.Errorf("the gauge is not a fixed width across values and styles: got %v", widths)
			break
		}
	}
}

func TestSettingsMeterBoundsAndNaN(t *testing.T) {
	pal := overlay.Palette{}
	bg := pal.Surface

	for _, f := range []float64{-1.0, 0, 0.5, 1.0, 2.0, math.NaN(), math.Inf(1), math.Inf(-1)} {
		out := settingsMeter(f, true, bg, pal)
		w := lipgloss.Width(out)
		// settingsMeterCells + 1 space = 7 cells
		if w != settingsMeterCells+1 {
			t.Errorf("settingsMeter(%v) width = %d, want %d", f, w, settingsMeterCells+1)
		}
	}
}

func TestPercentItemEqualBounds(t *testing.T) {
	item := percentItem("test.path", 20, 20, func(m *OS) int { return 20 }, func(m *OS, v int) {})
	if item.meter == nil {
		t.Fatal("expected item.meter to be non-nil")
	}
	m := &OS{}
	got := item.meter(m)
	if got != 0 {
		t.Errorf("item.meter with lo == hi = %v, want 0", got)
	}
}
