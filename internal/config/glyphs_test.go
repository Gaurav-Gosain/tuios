package config

import (
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
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
	if got := Global.GetWindowSeparatorChar(); got != "━" {
		t.Errorf("rule = %q, want heavy's ━", got)
	}
	if got := Global.GetRailFocusMark(); got != "█" {
		t.Errorf("rail focus = %q, want heavy's █", got)
	}
	if got := Global.GetRailBullet(); got != "▪" {
		t.Errorf("rail bullet = %q, want heavy's ▪", got)
	}
	// heavy says nothing about the collapse arrow, so the built-in stands.
	if got := Global.GetRailCollapseGlyph(); got != "«" {
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
			{"close", Global.GetWindowButtonClose(), 3},
			{"maximize", Global.GetWindowButtonMaximize(), 3},
			{"minimize", Global.GetWindowButtonMinimize(), 4},
			{"dot", Global.GetWindowButtonDot(), 1},
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
	prev := Global.BorderStyle
	t.Cleanup(func() { Global.BorderStyle = prev })

	Global.BorderStyle = "rounded"
	if got := Global.GetBorderForStyle().TopLeft; got != "╭" {
		t.Errorf("top-left = %q under border_style=rounded, want the rounded corner", got)
	}

	Global.BorderStyle = BorderStyleGlyphs
	if got := Global.GetBorderForStyle().TopLeft; got != "┏" {
		t.Errorf("top-left = %q under border_style=glyphs, want heavy's ┏", got)
	}
	if got := Global.GetBorderForStyle().Middle; got != "╋" {
		t.Errorf("junction = %q, want heavy's own, so a divider joins its border", got)
	}
}

func TestASetLeavingTheBorderPartlyUnsaidFallsBackPerRune(t *testing.T) {
	// The likely case for a hand-written set: four corners and "the rest as
	// usual". Falling back whole would give the frame a stroke its corners do
	// not meet.
	prev := Global.BorderStyle
	t.Cleanup(func() { Global.BorderStyle = prev })
	Global.BorderStyle = BorderStyleGlyphs
	withGlyphSet(t, theme.GlyphSetNone)
	b := Global.GetBorderForStyle()
	if b.Top != "─" || b.TopLeft != "╭" {
		t.Errorf("border = %+v, want the rounded border where the set says nothing", b)
	}
}

func TestASetsGlyphOutsideASCIILosesToASCIIModePerRole(t *testing.T) {
	withGlyphSet(t, "heavy")
	prev := Global.UseASCIIOnly
	Global.UseASCIIOnly = true
	t.Cleanup(func() { Global.UseASCIIOnly = prev })

	if got := Global.GetRailFocusMark(); got != ">" {
		t.Errorf("rail focus = %q, want the ASCII default: █ is not 7-bit", got)
	}
	if got := Global.GetWindowButtonMinimize(); got != "  - " {
		t.Errorf("minimize = %q, want the ASCII four-cell button", got)
	}

	// The ascii set is 7-bit throughout, so nothing of it is given up.
	withGlyphSet(t, "ascii")
	if got := Global.GetRailFocusMark(); got != ">" {
		t.Errorf("rail focus = %q under the ascii set, want its own >", got)
	}
	if got := Global.GetDockSeparator(); got != " | " {
		t.Errorf("separator = %q under the ascii set, want its own", got)
	}
}

func TestTheGapAndTheClockComeOffTheConfig(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Appearance.Gap = 3
	cfg.Appearance.ClockFormat = "15:04"
	ApplyAppearanceConfig(cfg, &Global)
	t.Cleanup(func() { ApplyAppearanceConfig(DefaultConfig(), &Global) })

	if Global.PaneGap != 3 {
		t.Errorf("PaneGap = %d, want 3", Global.PaneGap)
	}
	if got := Global.GetClockFormat(); got != "15:04" {
		t.Errorf("clock format = %q, want 15:04", got)
	}

	// A gap past the cap is clamped rather than refused: the frame still draws
	// and the value the user asked for is the one they get up to the ceiling.
	cfg.Appearance.Gap = 99
	ApplyAppearanceConfig(cfg, &Global)
	if Global.PaneGap != PaneGapMax {
		t.Errorf("PaneGap = %d for a gap of 99, want the %d cap", Global.PaneGap, PaneGapMax)
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

// TestGlyphBorderInASCIIModeStaysASCII pins the fallback a glyph set's border
// falls back to when the mode forbids what the set asked for.
//
// The rune check rejects a set's non-ASCII runes in ASCII mode, and the base it
// fell back to was the rounded border, so border_style = "glyphs" with
// --ascii-only drew ╭ corners on exactly the terminals that mode exists for.
func TestGlyphBorderInASCIIModeStaysASCII(t *testing.T) {
	prevASCII, prevStyle := Global.UseASCIIOnly, Global.BorderStyle
	prevSet := theme.ActiveGlyphSetID()
	t.Cleanup(func() {
		Global.UseASCIIOnly, Global.BorderStyle = prevASCII, prevStyle
		theme.SetActiveGlyphs(prevSet)
	})

	Global.UseASCIIOnly = true
	Global.BorderStyle = BorderStyleGlyphs
	// heavy names a full box-drawing border, none of which is 7-bit.
	theme.SetActiveGlyphs("heavy")

	b := Global.GetBorderForStyle()
	for name, rune := range map[string]string{
		"top": b.Top, "bottom": b.Bottom, "left": b.Left, "right": b.Right,
		"top_left": b.TopLeft, "top_right": b.TopRight,
		"bottom_left": b.BottomLeft, "bottom_right": b.BottomRight,
	} {
		if !overlay.IsASCII(rune) {
			t.Errorf("border %s is %q in ASCII mode", name, rune)
		}
	}

	drawn := Global.ResolvedGlyphs()
	if got := drawn["border.top_left"]; !overlay.IsASCII(got) {
		t.Errorf("ResolvedGlyphs reports border.top_left as %q in ASCII mode", got)
	}
}
