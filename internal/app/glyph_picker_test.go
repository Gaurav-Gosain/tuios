package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// newGlyphPickerOS builds a model with the picker open and a config to write
// into, which is the state every path below starts from.
func newGlyphPickerOS(t *testing.T) *OS {
	t.Helper()
	m := &OS{Settings: config.Global, Width: 120, Height: 40}
	m.UserConfig = config.DefaultConfig()
	before := theme.ActiveGlyphSetID()
	t.Cleanup(func() { theme.SetActiveGlyphs(before) })
	m.OpenGlyphPicker()
	return m
}

// TestGlyphPickerKeepsTheQueryWhenNothingMatches is the bug this branch was
// warned about: four overlays closed unconditionally and then returned early on
// no selection, so a query that matched nothing threw the query away and left
// the live preview applied with no way back to it.
func TestGlyphPickerKeepsTheQueryWhenNothingMatches(t *testing.T) {
	m := newGlyphPickerOS(t)
	m.GlyphPickerQuery = "no-such-glyph-set"
	m.GlyphPickerRefilter()

	if got := len(m.glyphPickerItems()); got != 0 {
		t.Fatalf("fixture matched %d sets; the test needs a query that matches none", got)
	}
	if cmd := m.GlyphPickerApplySelection(); cmd != nil {
		t.Error("applying nothing handed back a save command")
	}
	if !m.ShowGlyphPicker {
		t.Error("the picker closed with nothing selected, discarding the query")
	}
	if m.GlyphPickerQuery != "no-such-glyph-set" {
		t.Errorf("the query was discarded: %q", m.GlyphPickerQuery)
	}
}

// TestGlyphPickerCancelRestoresTheOriginal checks Esc puts back the set that was
// active when the picker opened, so a live preview cannot stick.
func TestGlyphPickerCancelRestoresTheOriginal(t *testing.T) {
	m := newGlyphPickerOS(t)
	original := m.GlyphPickerOriginal

	items := m.glyphPickerItems()
	var other string
	for _, id := range items {
		if id != original {
			other = id
			break
		}
	}
	if other == "" {
		t.Skip("only one glyph set is available, so there is no preview to revert")
	}
	m.applyGlyphSet(other)
	if theme.ActiveGlyphSetID() != other {
		t.Fatalf("preview did not apply: active is %q", theme.ActiveGlyphSetID())
	}

	m.CancelGlyphPicker()
	if got := theme.ActiveGlyphSetID(); got != original {
		t.Errorf("cancel left %q active, want the original %q", got, original)
	}
	if m.ShowGlyphPicker {
		t.Error("cancel left the picker open")
	}
}

// TestGlyphPickerApplyWritesTheConfig checks a committed set reaches the config,
// which is what makes it survive a restart and what get-config reports.
func TestGlyphPickerApplyWritesTheConfig(t *testing.T) {
	m := newGlyphPickerOS(t)
	m.ConfigReadOnly = true // no file write from a test

	items := m.glyphPickerItems()
	if len(items) < 2 {
		t.Skip("need two glyph sets to pick a different one")
	}
	target := items[0]
	if target == m.GlyphPickerOriginal {
		target = items[1]
	}
	for i, id := range items {
		if id == target {
			m.GlyphPickerSelected = i
		}
	}
	m.GlyphPickerApplySelection()

	if got := m.UserConfig.Appearance.Glyphs; got != target {
		t.Errorf("config records glyph set %q, want %q", got, target)
	}
	if got, _ := config.GetOptionValue(m.UserConfig, "appearance.glyphs"); got != target {
		t.Errorf("the registry reads back %q, want %q; a person and an agent disagree", got, target)
	}
	if m.ShowGlyphPicker {
		t.Error("apply left the picker open")
	}
}

// TestGlyphPickerPreviewsTheShape checks each row draws the set's own glyphs
// rather than the active set's. A picker whose rows all look alike is a list,
// not a preview.
func TestGlyphPickerPreviewsTheShape(t *testing.T) {
	m := newGlyphPickerOS(t)

	ascii, ok := m.GlyphPickerSamples["ascii"]
	if !ok {
		t.Fatal("the built-in ascii set has no sample")
	}
	heavy, ok := m.GlyphPickerSamples["heavy"]
	if !ok {
		t.Fatal("the built-in heavy set has no sample")
	}
	if ascii.Frame == heavy.Frame {
		t.Errorf("two different sets preview identically: %q", ascii.Frame)
	}
	if !ascii.ASCII {
		t.Errorf("the ascii set does not report itself 7-bit: %q", ascii.Frame)
	}
	if heavy.ASCII {
		t.Errorf("the heavy set reports itself 7-bit: %q", heavy.Frame)
	}
	if !strings.Contains(heavy.Frame, "┏") {
		t.Errorf("the heavy set's preview does not draw its own corner: %q", heavy.Frame)
	}
}

// TestGlyphSamplesLeaveTheSelectionAlone checks building the previews puts the
// active set back. Each sample borrows the selection to read what its set
// draws, and a borrow that is not returned would repaint the whole screen in
// whichever set happened to be sampled last.
func TestGlyphSamplesLeaveTheSelectionAlone(t *testing.T) {
	before := theme.ActiveGlyphSetID()
	t.Cleanup(func() { theme.SetActiveGlyphs(before) })

	theme.SetActiveGlyphs("heavy")
	m := &OS{Settings: config.Global, Width: 120, Height: 40}
	m.buildGlyphSamples()

	if got := theme.ActiveGlyphSetID(); got != "heavy" {
		t.Errorf("sampling left %q active, want heavy", got)
	}
}
