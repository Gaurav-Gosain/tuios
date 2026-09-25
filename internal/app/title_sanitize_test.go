package app

import (
	"strings"
	"testing"
)

// TestPrintableTitleDropsDecorative asserts the shared sanitizer strips the
// decorative codepoints an agent writes into a terminal title while leaving
// our own status glyphs, box drawing, arrows and ordinary text untouched.
//
// The junk is a Dingbat sparkle, a Miscellaneous-Symbols diamond, an emoji and
// a symbol carrying the emoji variation selector, plus the codepoints Claude
// Code writes with OSC 0: U+2733 while idle and the U+2802/U+2810 Braille pair
// it alternates while working. The Braille frames used to survive and were the
// tofu box the rail showed.
func TestPrintableTitleDropsDecorative(t *testing.T) {
	for _, tc := range []struct {
		in   string
		drop []rune
		keep []string
	}{
		{"✳ claude ♦ \U0001f680 build\uFE0F", []rune{'✳', '♦', '\U0001f680', '\uFE0F'}, []string{"claude", "build"}},
		{"✳ Claude Code", []rune{'✳'}, []string{"Claude Code"}},
		{"⠂ Claude Code", []rune{'⠂'}, []string{"Claude Code"}},
		{"⠐ Acknowledge request", []rune{'⠐'}, []string{"Acknowledge request"}},
	} {
		got := printableTitle(tc.in)
		for _, bad := range tc.drop {
			if strings.ContainsRune(got, bad) {
				t.Errorf("printableTitle(%q) kept U+%04X: %q", tc.in, bad, got)
			}
		}
		for _, want := range tc.keep {
			if !strings.Contains(got, want) {
				t.Errorf("printableTitle(%q) dropped %q: %q", tc.in, want, got)
			}
		}
	}

	for _, keep := range []string{"● run │ tests → ok", "●▲○■× café 日本語 (v2) ─ ok"} {
		if got := printableTitle(keep); got != keep {
			t.Errorf("printableTitle mangled legitimate glyphs: got %q want %q", got, keep)
		}
	}
}
