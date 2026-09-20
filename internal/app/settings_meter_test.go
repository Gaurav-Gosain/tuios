package app

import (
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

// TestTheGaugeKeepsItsWidthAtEveryValue pins that both halves are always drawn,
// so the control beside the gauge sits in the same column on every row.
func TestTheGaugeKeepsItsWidthAtEveryValue(t *testing.T) {
	pal := overlay.Palette{}
	on, off := settingsMeterGlyphs()
	if on == "" || off == "" {
		t.Fatalf("a gauge glyph is empty: on=%q off=%q", on, off)
	}
	for _, f := range []float64{0, 0.1, 0.5, 0.9, 1} {
		out := ansi.Strip(settingsMeter(f, false, pal.Surface, pal))
		if want := settingsMeterCells + 1; lipgloss.Width(out) != want {
			t.Errorf("fraction %v drew %d cells, want %d: %q", f, lipgloss.Width(out), want, out)
		}
	}
}

// TestOnlyAProportionGetsAGauge pins that the bar is drawn beside a value whose
// range is real travel, and nowhere else.
//
// Every int option with a ceiling used to get one. Most of those ceilings are
// guards against a silly number rather than the top of a scale, so the gauge sat
// empty beside a notification timeout that allows an hour or a scrollback that
// allows a million lines, saying nothing about either.
func TestOnlyAProportionGetsAGauge(t *testing.T) {
	m := &OS{Settings: config.Global, Width: 120, Height: 40}
	for _, o := range config.Options() {
		if o.Type != config.OptionInt {
			continue
		}
		item := m.registryItem(o.Path)
		switch {
		case o.Percent && item.meter == nil:
			t.Errorf("%s is a proportion but has no gauge", o.Path)
		case !o.Percent && item.meter != nil:
			t.Errorf("%s is not a proportion but has a gauge", o.Path)
		}
	}
}

// TestEveryGaugedRowIsAProportion covers the hand-written rows too, which do not
// come from the registry and so are not reached by the check above.
func TestEveryGaugedRowIsAProportion(t *testing.T) {
	proportion := map[string]bool{}
	for _, o := range config.Options() {
		if o.Percent {
			proportion[settingLabel(o.Path)] = true
		}
	}
	if len(proportion) == 0 {
		t.Fatal("no option is marked as a proportion, so this test checks nothing")
	}

	m := &OS{Settings: config.Global, Width: 120, Height: 40}
	for _, cat := range m.settingsCategories() {
		for _, item := range cat.Items {
			if item.meter != nil && !proportion[item.Label] {
				t.Errorf("%s/%s has a gauge but is not a proportion", cat.Name, item.Label)
			}
		}
	}
}

// TestAProportionReadsAsAPercent pins the % after the number, so the gauge and
// the value agree about what the row is measuring.
func TestAProportionReadsAsAPercent(t *testing.T) {
	m := &OS{Settings: config.Global, Width: 120, Height: 40}
	for _, o := range config.Options() {
		if !o.Percent {
			continue
		}
		item := m.registryItem(o.Path)
		if item.value == nil {
			t.Errorf("%s has no value to show", o.Path)
			continue
		}
		if got := item.value(m); !strings.HasSuffix(got, "%") {
			t.Errorf("%s reads as %q, want a trailing %%", o.Path, got)
		}
	}
}
