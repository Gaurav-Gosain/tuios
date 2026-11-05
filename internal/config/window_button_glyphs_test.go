package config

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// The window control pill is drawn into a fixed grid of cells, and every
// single-width grapheme in it gets exactly one cell. A glyph the font does not
// cover falls back to some proportional system font whose advance overruns that
// cell, and the overrun is then clipped or overpainted by whatever comes next,
// which is how the close button lost the right half of its X. The test here
// pins the half of that contract the source can show: the chrome runes must
// measure one cell.

// windowChromeRunes lists every rune tuios paints into window decorations
// through the Nerd Font path.
func windowChromeRunes(t *testing.T) map[rune]string {
	t.Helper()
	out := map[rune]string{}
	add := func(name, s string) {
		for _, r := range s {
			if r == ' ' {
				continue
			}
			out[r] = name
		}
	}
	add("WindowButtonClose", WindowButtonClose)
	add("WindowButtonMinimize", "-")
	add("WindowButtonMaximize", "□")
	add("WindowButtonDot", WindowButtonDot)
	add("WindowPillLeft", WindowPillLeft)
	add("WindowPillRight", WindowPillRight)
	add("WindowBorderTopLeft", WindowBorderTopLeft)
	add("WindowBorderTopRight", WindowBorderTopRight)
	add("WindowBorderBottomLeft", WindowBorderBottomLeft)
	add("WindowBorderBottomRight", WindowBorderBottomRight)
	add("WindowBorderHorizontal", WindowBorderHorizontal)
	add("WindowBorderVertical", WindowBorderVertical)
	return out
}

func TestWindowChromeRunesAreSingleWidth(t *testing.T) {
	for r, name := range windowChromeRunes(t) {
		if w := ansi.StringWidth(string(r)); w != 1 {
			t.Errorf("%s: U+%04X measures %d cells, want 1", name, r, w)
		}
	}

	// The pill contents are what the mouse hit-test offsets are measured
	// against, so their total width is load-bearing, not cosmetic.
	if w := ansi.StringWidth(WindowButtonClose); w != 3 {
		t.Errorf("WindowButtonClose measures %d cells, want 3", w)
	}
	if w := ansi.StringWidth(WindowButtonCloseASCII); w != 3 {
		t.Errorf("WindowButtonCloseASCII measures %d cells, want 3", w)
	}

	// The dots style lays three discs and their gaps out cell by cell, so a
	// disc that measured two would slide the other two under the corner.
	for name, glyph := range map[string]string{
		"WindowButtonDot":      WindowButtonDot,
		"WindowButtonDotASCII": WindowButtonDotASCII,
	} {
		if w := ansi.StringWidth(glyph); w != 1 {
			t.Errorf("%s measures %d cells, want 1", name, w)
		}
	}
}
