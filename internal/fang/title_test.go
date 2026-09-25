package fang

import (
	"math/rand"
	"testing"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// TestTitleMatchesXText checks title against the golang.org/x/text casing it
// replaces, on random ASCII text and on text in other scripts. x/text is
// imported here only, so the binary does not carry its tables.
func TestTitleMatchesXText(t *testing.T) {
	want := cases.Title(language.AmericanEnglish)
	const ascii = "aZ09_ '.:,;-=/\"()!?@#$%^&*+<>[]{}|\\`~\t"
	unicodeRunes := []rune("éÉöÖçñÑæøåαβγΑΒΓжЖ日本語’‘·アあ한ـ١")
	rng := rand.New(rand.NewSource(1))
	check := func(s string) {
		t.Helper()
		if got, w := title(s), want.String(s); got != w {
			t.Errorf("title(%q) = %q, x/text gives %q", s, got, w)
		}
	}
	for i := 0; i < 200000; i++ {
		n := rng.Intn(12)
		b := make([]byte, n)
		for j := range b {
			if rng.Intn(3) == 0 {
				b[j] = byte('a' + rng.Intn(26))
			} else {
				b[j] = ascii[rng.Intn(len(ascii))]
			}
		}
		check(string(b))
	}
	for i := 0; i < 20000; i++ {
		n := rng.Intn(8)
		r := make([]rune, n)
		for j := range r {
			if rng.Intn(2) == 0 {
				r[j] = unicodeRunes[rng.Intn(len(unicodeRunes))]
			} else {
				r[j] = rune(ascii[rng.Intn(len(ascii))])
			}
		}
		check(string(r))
	}
	for _, s := range []string{"One-line", "KEY=VALUE", "don't", "e.g.", "a..b", "2fa", "unknown command \"x\" for \"tuios\""} {
		check(s)
	}
}
