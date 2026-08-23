package app

import (
	"image/color"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

func TestDimReachesACellThatNamedNoColourOfItsOwn(t *testing.T) {
	// A shell prompt is mostly cells carrying no colour at all. Gating the dim
	// on "does this cell carry anything of its own" left the setting doing
	// nothing to most of a pane even with a theme set, which reads as a broken
	// option rather than a subtle one.
	withDim(t, 60)
	win := newTestWindow(t, "plain", 40, 6)
	win.WriteOutput([]byte("plain text with no sgr at all\r\n"))
	win.MarkContentDirty()
	m := newTestOS(win)

	focused := m.renderTerminal(win, true, false)
	win.MarkContentDirty()
	unfocused := m.renderTerminal(win, false, false)

	if focused == unfocused {
		t.Fatal("an unfocused pane of uncoloured text rendered identically to the focused one")
	}
	if !strings.Contains(unfocused, "plain text with no sgr at all") {
		t.Error("the dim ate the content")
	}
}

func TestDimSurvivesAWrappedNilCellColour(t *testing.T) {
	// color.Color's RGBA has a value receiver, so calling it through an
	// interface holding a nil pointer panics rather than returning zeros. Cells
	// arrive that way, which is why the rest of this package screens with
	// isNilColor.
	var dst, src uv.Cell
	src.Content = "x"
	src.Style.Fg = (*color.RGBA)(nil)
	src.Style.Bg = (*color.RGBA)(nil)

	fg, bg := color.RGBA{R: 200, G: 200, B: 200, A: 255}, color.RGBA{R: 20, G: 20, B: 30, A: 255}
	got := dimCell(&dst, &src, fg, bg, 0.5)
	if got == nil {
		t.Fatal("dimCell returned nil")
	}
	if isNilColor(got.Style.Fg) {
		t.Error("a wrapped-nil fg was not replaced by the ground's own ink")
	}
}

func TestAScrolledBackPaneDoesNotServeAFrameFromTheOtherFocusState(t *testing.T) {
	// The scrollback path used to return the cache without the dim key, and it
	// is reached only once the keyed check has already failed, so it fired
	// exactly on the mismatch it was meant to catch.
	withDim(t, 50)
	win := dimTestWindow(t, 40, 6)
	win.ScrollbackOffset = 1
	m := newTestOS(win)

	unfocused := m.renderTerminal(win, false, false)
	focused := m.renderTerminal(win, true, false)
	if focused == unfocused {
		t.Error("a scrolled-back pane served its dimmed frame to a focused render")
	}
}

func TestTheRailKeepsAnASCIISafeGlyphSetUnderASCIIMode(t *testing.T) {
	// The accessors already give up a glyph the terminal cannot draw and keep
	// one it can, per glyph. A second ASCII branch after one throws away a set
	// the terminal could have drawn.
	prevASCII, prevSet := config.UseASCIIOnly, theme.ActiveGlyphSetID()
	config.UseASCIIOnly = true
	theme.SetActiveGlyphs("ascii")
	t.Cleanup(func() {
		config.UseASCIIOnly = prevASCII
		theme.SetActiveGlyphs(prevSet)
	})

	for _, c := range []struct{ name, got, want string }{
		{"focus", config.GetRailFocusMark(), ">"},
		{"bullet", config.GetRailBullet(), "."},
		{"collapse", config.GetRailCollapseGlyph(), "<<"},
		{"expand", config.GetRailExpandGlyph(), ">>"},
	} {
		if c.got != c.want {
			t.Errorf("%s = %q, want the ascii set's own %q", c.name, c.got, c.want)
		}
	}
}
