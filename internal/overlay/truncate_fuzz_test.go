package overlay

import (
	"testing"

	"charm.land/lipgloss/v2"
)

// truncateLinear is Truncate as it was before the bisection: drop one rune at
// a time from the end until the rest fits.
func truncateLinear(s string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= maxWidth {
		return s
	}
	ell := Ellipsis()
	if lipgloss.Width(ell) >= maxWidth {
		ell = ""
	}
	target := max(maxWidth-lipgloss.Width(ell), 0)
	runes := []rune(s)
	for len(runes) > 0 && lipgloss.Width(string(runes)) > target {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + ell
}

// FuzzTruncateMatchesLinearScan holds the bisection to the linear scan it
// replaced. The two agree whenever a longer prefix is never narrower than a
// shorter one, and this is what checks that the width function keeps that
// promise for text, wide runes, clusters and escape sequences.
func FuzzTruncateMatchesLinearScan(f *testing.F) {
	for _, s := range []string{
		"",
		"plain ascii text that runs past the budget",
		"wide 漢字 and emoji 😀 mixed into a line",
		"é combining marks é and ZWJ 👨‍👩‍👧 family",
		"flags 🇺🇸🇬🇧 and variation ☺️ ⌚︎ selectors",
		"\x1b[31mstyled\x1b[0m text with \x1b]8;;http://x\x1b\\a link\x1b]8;;\x1b\\ in it",
		"\x1b[38;2;1;2;3mtruecolor run that is long enough to cut\x1b[m",
	} {
		for _, w := range []int{1, 2, 3, 5, 8, 13, 21} {
			f.Add(s, w)
		}
	}
	f.Fuzz(func(t *testing.T, s string, w int) {
		if w > 300 || len(s) > 300 {
			return
		}
		if got, want := Truncate(s, w), truncateLinear(s, w); got != want {
			t.Fatalf("Truncate(%q, %d) = %q, the linear scan gives %q", s, w, got, want)
		}
	})
}
