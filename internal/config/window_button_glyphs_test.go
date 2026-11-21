package config

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
)

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

	if w := ansi.StringWidth(WindowButtonClose); w != 3 {
		t.Errorf("WindowButtonClose measures %d cells, want 3", w)
	}
	if w := ansi.StringWidth(WindowButtonCloseASCII); w != 3 {
		t.Errorf("WindowButtonCloseASCII measures %d cells, want 3", w)
	}

	for name, glyph := range map[string]string{
		"WindowButtonDot":      WindowButtonDot,
		"WindowButtonDotASCII": WindowButtonDotASCII,
	} {
		if w := ansi.StringWidth(glyph); w != 1 {
			t.Errorf("%s measures %d cells, want 1", name, w)
		}
	}
}
