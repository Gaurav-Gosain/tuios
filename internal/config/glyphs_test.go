package config

import (
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// withGlyphSet selects a set for one test and puts the default back.
func withGlyphSet(t *testing.T, id string) {
	t.Helper()
	theme.SetActiveGlyphs(id)
	t.Cleanup(func() { theme.SetActiveGlyphs(theme.GlyphSetNone) })
}

func TestASetChangesTheGlyphsTheChromeIsDrawnWith(t *testing.T) {
	withGlyphSet(t, "heavy")
	if got := GetWindowSeparatorChar(); got != "━" {
		t.Errorf("rule = %q, want heavy's ━", got)
	}
	if got := GetRailFocusMark(); got != "█" {
		t.Errorf("rail focus = %q, want heavy's █", got)
	}
	if got := GetRailBullet(); got != "▪" {
		t.Errorf("rail bullet = %q, want heavy's ▪", got)
	}
	// heavy says nothing about the collapse arrow, so the built-in stands.
	if got := GetRailCollapseGlyph(); got != "«" {
		t.Errorf("collapse = %q, want the built-in « an unnamed role keeps", got)
	}
}

func TestAWindowControlKeepsItsWidthWhateverTheSetSays(t *testing.T) {
	// The press rectangles are fixed offsets measured against these widths, so
	// a set that could change them could move a button out from under the
	// pointer. The set names the mark; the renderer owns the padding.
	for _, id := range []string{theme.GlyphSetNone, "unicode", "heavy", "ascii"} {
		withGlyphSet(t, id)
		for _, c := range []struct {
			name  string
			got   string
			cells int
		}{
			{"close", GetWindowButtonClose(), 3},
			{"maximize", GetWindowButtonMaximize(), 3},
			{"minimize", GetWindowButtonMinimize(), 4},
			{"dot", GetWindowButtonDot(), 1},
		} {
			if w := lipgloss.Width(c.got); w != c.cells {
				t.Errorf("%s: %s = %q, %d cells, want %d", id, c.name, c.got, w, c.cells)
			}
		}
	}
}

func TestTheSetsBorderDrawsOnlyWhenBorderStyleAsksForIt(t *testing.T) {
	// A set's border could have won whenever it defined one, which is one fewer
	// thing to say and turns an option the user already set into a silent
	// no-op. Selected by name instead, so both settings stay live.
	withGlyphSet(t, "heavy")
	prev := BorderStyle
	t.Cleanup(func() { BorderStyle = prev })

	BorderStyle = "rounded"
	if got := GetBorderForStyle().TopLeft; got != "╭" {
		t.Errorf("top-left = %q under border_style=rounded, want the rounded corner", got)
	}

	BorderStyle = BorderStyleGlyphs
	if got := GetBorderForStyle().TopLeft; got != "┏" {
		t.Errorf("top-left = %q under border_style=glyphs, want heavy's ┏", got)
	}
	if got := GetBorderForStyle().Middle; got != "╋" {
		t.Errorf("junction = %q, want heavy's own, so a divider joins its border", got)
	}
}

func TestASetLeavingTheBorderPartlyUnsaidFallsBackPerRune(t *testing.T) {
	// The likely case for a hand-written set: four corners and "the rest as
	// usual". Falling back whole would give the frame a stroke its corners do
	// not meet.
	prev := BorderStyle
	t.Cleanup(func() { BorderStyle = prev })
	BorderStyle = BorderStyleGlyphs
	withGlyphSet(t, theme.GlyphSetNone)
	b := GetBorderForStyle()
	if b.Top != "─" || b.TopLeft != "╭" {
		t.Errorf("border = %+v, want the rounded border where the set says nothing", b)
	}
}

func TestASetsGlyphOutsideASCIILosesToASCIIModePerRole(t *testing.T) {
	withGlyphSet(t, "heavy")
	prev := UseASCIIOnly
	UseASCIIOnly = true
	t.Cleanup(func() { UseASCIIOnly = prev })

	if got := GetRailFocusMark(); got != ">" {
		t.Errorf("rail focus = %q, want the ASCII default: █ is not 7-bit", got)
	}
	if got := GetWindowButtonMinimize(); got != "  - " {
		t.Errorf("minimize = %q, want the ASCII four-cell button", got)
	}

	// The ascii set is 7-bit throughout, so nothing of it is given up.
	withGlyphSet(t, "ascii")
	if got := GetRailFocusMark(); got != ">" {
		t.Errorf("rail focus = %q under the ascii set, want its own >", got)
	}
	if got := GetDockSeparator(); got != " | " {
		t.Errorf("separator = %q under the ascii set, want its own", got)
	}
}

func TestTheGapAndTheClockComeOffTheConfig(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Appearance.Gap = 3
	cfg.Appearance.ClockFormat = "15:04"
	ApplyAppearanceConfig(cfg)
	t.Cleanup(func() { ApplyAppearanceConfig(DefaultConfig()) })

	if PaneGap != 3 {
		t.Errorf("PaneGap = %d, want 3", PaneGap)
	}
	if got := GetClockFormat(); got != "15:04" {
		t.Errorf("clock format = %q, want 15:04", got)
	}

	// A gap past the cap is clamped rather than refused: the frame still draws
	// and the value the user asked for is the one they get up to the ceiling.
	cfg.Appearance.Gap = 99
	ApplyAppearanceConfig(cfg)
	if PaneGap != PaneGapMax {
		t.Errorf("PaneGap = %d for a gap of 99, want the %d cap", PaneGap, PaneGapMax)
	}
}

func TestAClockLayoutWithNoTimeInItIsWarnedAbout(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Appearance.ClockFormat = "hello"
	res := ValidateConfig(cfg)
	var warned bool
	for _, w := range res.Warnings {
		if w.Key == "clock_format" {
			warned = true
		}
	}
	if !warned {
		t.Errorf("warnings = %v, want one about clock_format", res.Warnings)
	}

	cfg.Appearance.ClockFormat = "Mon 3:04PM"
	for _, w := range ValidateConfig(cfg).Warnings {
		if w.Key == "clock_format" {
			t.Errorf("unexpected warning for a real layout: %s", w.Message)
		}
	}
}

func TestAnUnknownGlyphSetIsRefusedAtTheRegistry(t *testing.T) {
	cfg := DefaultConfig()
	if err := SetOptionValue(cfg, "appearance.glyphs", "nope"); err == nil {
		t.Error("setting an unknown glyph set was accepted; it would be recorded and never drawn")
	}
	if err := SetOptionValue(cfg, "appearance.glyphs", "heavy"); err != nil {
		t.Errorf("setting a built-in set failed: %v", err)
	}
}
