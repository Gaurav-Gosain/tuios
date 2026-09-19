package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestOptionArrowIsRecognisedAsTheArrowItWas.
//
// A macOS terminal sends the readline word motions for Option+Left and
// Option+Right. Nothing in what arrives says an arrow key was pressed, so the
// binding misses and, until this existed, nothing said why: the advice fires on
// a composed glyph and these two carry a real Alt modifier and no glyph.
//
// Negative control: dropping the darwin check makes the linux case below
// report a rewritten arrow for a chord a linux user bound themselves.
func TestOptionArrowIsRecognisedAsTheArrowItWas(t *testing.T) {
	onDarwin(t)

	for got, want := range map[string]string{"alt+b": "alt+left", "alt+f": "alt+right"} {
		msg := tea.KeyPressMsg{Code: rune(got[len(got)-1]), Mod: tea.ModAlt}
		_, arrow, ok := macRewrittenAltArrow(msg)
		if !ok || arrow != want {
			t.Errorf("%s was read as %q (ok=%v), want %s", got, arrow, ok, want)
		}
	}

	// Not every alt chord. alt+n is a binding of its own and saying it was
	// meant as an arrow would be wrong.
	if _, _, ok := macRewrittenAltArrow(tea.KeyPressMsg{Code: 'n', Mod: tea.ModAlt}); ok {
		t.Error("alt+n was read as a rewritten arrow")
	}
	// Not with another modifier: no terminal sends ctrl+alt+b for an arrow.
	if _, _, ok := macRewrittenAltArrow(tea.KeyPressMsg{Code: 'b', Mod: tea.ModAlt | tea.ModCtrl}); ok {
		t.Error("ctrl+alt+b was read as a rewritten arrow")
	}

	darwinHost = false
	if _, _, ok := macRewrittenAltArrow(tea.KeyPressMsg{Code: 'b', Mod: tea.ModAlt}); ok {
		t.Error("alt+b on linux was read as a rewritten arrow")
	}
}
