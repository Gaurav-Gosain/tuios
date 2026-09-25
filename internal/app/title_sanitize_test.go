package app

import (
	"strings"
	"testing"
)

// decorativeTitle carries the junk an agent tends to inject into a terminal
// title: a Dingbat sparkle, a Miscellaneous-Symbols diamond, an emoji, and a
// symbol carrying the emoji variation selector. None of it should survive as
// chrome.
const decorativeTitle = "✳ claude ♦ \U0001f680 build️"

// TestPrintableTitleDropsDecorative asserts the shared sanitizer strips
// decorative symbol/emoji codepoints while leaving our own status glyphs and
// legitimate box-drawing/arrow characters untouched.
func TestPrintableTitleDropsDecorative(t *testing.T) {
	got := printableTitle(decorativeTitle)
	for _, bad := range []rune{'✳', '♦', '\U0001f680', '️'} {
		if strings.ContainsRune(got, bad) {
			t.Errorf("printableTitle kept decorative U+%04X: %q", bad, got)
		}
	}
	if !strings.Contains(got, "claude") || !strings.Contains(got, "build") {
		t.Errorf("printableTitle dropped legitimate text: %q", got)
	}

	// Our state glyphs, box-drawing, and arrows must pass through verbatim.
	keep := "● run │ tests → ok"
	if got := printableTitle(keep); got != keep {
		t.Errorf("printableTitle mangled legitimate glyphs: got %q want %q", got, keep)
	}
}

// TestPrintableTitleDropsSpinnerFrames pins the codepoints Claude Code actually
// writes with OSC 0: U+2733 while idle and the U+2802/U+2810 Braille pair it
// alternates while working. The Braille frames used to survive and were the
// tofu box the rail showed. Our own status glyphs and ordinary text stay.
func TestPrintableTitleDropsSpinnerFrames(t *testing.T) {
	for _, in := range []string{"✳ Claude Code", "⠂ Claude Code", "⠐ Acknowledge request"} {
		got := printableTitle(in)
		for _, bad := range []rune{'✳', '⠂', '⠐'} {
			if strings.ContainsRune(got, bad) {
				t.Errorf("printableTitle kept U+%04X from %q: %q", bad, in, got)
			}
		}
	}

	keep := "●▲○■× café 日本語 (v2) ─ ok"
	if got := printableTitle(keep); got != keep {
		t.Errorf("printableTitle mangled status glyphs or text: got %q want %q", got, keep)
	}
}
