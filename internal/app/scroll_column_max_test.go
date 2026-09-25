package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// The scrolling layout caps a column at 90 percent of the screen, which is
// where the next column stops peeking in at the edge. That peek is the only
// thing that says the strip has another column, so it is worth keeping by
// default. It is not worth keeping for somebody who wants a pane at full width
// and does not want to zoom for it, because zooming costs them the fast window
// switching the strip is for. appearance.scroll_column_max is that choice.

// TestAColumnCanFillTheScreenWhenTheCapAllowsIt is the request: 100 percent,
// no zoom.
func TestAColumnCanFillTheScreenWhenTheCapAllowsIt(t *testing.T) {
	m := scrollingOS(t, 3)
	m.Settings.ScrollColumnMax = config.ScrollColumnWidthCeiling

	m.SetScrollColumnWidthSetting(100)

	if m.ScrollColumnWidth != 100 {
		t.Errorf("the width settled at %d, want 100", m.ScrollColumnWidth)
	}
	if got := m.ScrollColumnWidthFraction(); got != 1 {
		t.Errorf("the width resolves to %v of the screen, want the whole of it", got)
	}
}

// TestTheCapHoldsTheWidthDown pins the other half: with the cap left alone, a
// request for 100 settles at the cap rather than being taken.
func TestTheCapHoldsTheWidthDown(t *testing.T) {
	m := scrollingOS(t, 3)
	m.Settings.ScrollColumnMax = config.ScrollColumnWidthMax

	m.SetScrollColumnWidthSetting(100)

	if m.ScrollColumnWidth != config.ScrollColumnWidthMax {
		t.Errorf("the width settled at %d, want the cap %d", m.ScrollColumnWidth, config.ScrollColumnWidthMax)
	}
}

// TestTheSettingsRowStepsToTheCap pins that the panel's stepper reaches
// whatever the cap is, rather than stopping at the constant.
func TestTheSettingsRowStepsToTheCap(t *testing.T) {
	m := scrollingOS(t, 3)
	m.Settings.ScrollColumnMax = config.ScrollColumnWidthCeiling
	m.ScrollColumnWidth = config.ScrollColumnWidthMax

	item := m.scrollColumnWidthItem()
	for range 40 {
		item.adjust(m, 1)
	}

	if m.ScrollColumnWidth != config.ScrollColumnWidthCeiling {
		t.Errorf("stepping the row up ended at %d, want the cap %d",
			m.ScrollColumnWidth, config.ScrollColumnWidthCeiling)
	}
	if got := item.meter(m); got != 1 {
		t.Errorf("the gauge reads %v at the cap, want full", got)
	}
}

// TestAConfiguredCapSurvivesTheLoad pins the config path, since the clamp on
// load is a second place the two values have to be read in the right order.
func TestAConfiguredCapSurvivesTheLoad(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Appearance.ScrollColumnMax = 100
	cfg.Appearance.ScrollColumnWidth = 100

	s := config.DefaultSettings()
	config.ApplyAppearanceConfig(cfg, &s)

	if s.ScrollColumnMax != 100 {
		t.Errorf("the loaded cap is %d, want 100", s.ScrollColumnMax)
	}
	if s.ScrollColumnWidth != 100 {
		t.Errorf("the loaded width is %d, want 100: the width was clamped against the old constant", s.ScrollColumnWidth)
	}
}
