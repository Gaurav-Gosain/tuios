package fuzzy

import (
	"math/rand/v2"
	"strings"
	"testing"
	"unicode/utf8"
)

// checkInvariants holds every match to the contract the callers rely on: one
// position per pattern character, ascending, on rune boundaries, inside the
// candidate, pointing at characters that actually match, and bracketed by
// Start and End.
func checkInvariants(t *testing.T, pattern, text string, r Result) {
	t.Helper()
	want := utf8.RuneCountInString(pattern)
	if len(r.Positions) != want {
		t.Fatalf("Find(%q, %q): %d positions, want %d (%v)", pattern, text, len(r.Positions), want, r.Positions)
	}
	if want == 0 {
		return
	}
	pat := []rune(pattern)
	for i, p := range r.Positions {
		if p < 0 || p >= len(text) {
			t.Fatalf("Find(%q, %q): position %d out of range", pattern, text, p)
		}
		if !utf8.RuneStart(text[p]) {
			t.Fatalf("Find(%q, %q): position %d is mid-rune", pattern, text, p)
		}
		if i > 0 && p <= r.Positions[i-1] {
			t.Fatalf("Find(%q, %q): positions not ascending: %v", pattern, text, r.Positions)
		}
		got, _ := utf8.DecodeRuneInString(text[p:])
		if fold(got) != fold(pat[i]) {
			t.Fatalf("Find(%q, %q): position %d holds %q, want %q", pattern, text, p, got, pat[i])
		}
	}
	if r.Start != r.Positions[0] {
		t.Fatalf("Find(%q, %q): Start %d, want %d", pattern, text, r.Start, r.Positions[0])
	}
	last := r.Positions[len(r.Positions)-1]
	_, size := utf8.DecodeRuneInString(text[last:])
	if r.End != last+size {
		t.Fatalf("Find(%q, %q): End %d, want %d", pattern, text, r.End, last+size)
	}
}

// TestInvariantsOverRandomInputs sweeps shapes a hand-written table misses:
// repeated characters, patterns as long as their candidate, multi-byte runes,
// and separators in every position.
func TestInvariantsOverRandomInputs(t *testing.T) {
	alphabet := []rune("abcAB-_./ 1é☃")
	rng := rand.New(rand.NewPCG(42, 99))
	pick := func(n int) string {
		var b strings.Builder
		for range n {
			b.WriteRune(alphabet[rng.IntN(len(alphabet))])
		}
		return b.String()
	}

	for range 20000 {
		text := pick(rng.IntN(12) + 1)
		pattern := pick(rng.IntN(4) + 1)
		r, ok := Find(pattern, text)
		if !ok {
			continue
		}
		checkInvariants(t, pattern, text, r)
	}
}

// TestSubstringAlwaysMatches is the floor under the matcher: anything a
// substring search would have found, the scored one must find too, or
// converging the old call sites onto it would have lost results.
func TestSubstringAlwaysMatches(t *testing.T) {
	alphabet := []rune("abcAB-_.1é")
	rng := rand.New(rand.NewPCG(7, 3))
	for range 5000 {
		var b strings.Builder
		for range rng.IntN(10) + 2 {
			b.WriteRune(alphabet[rng.IntN(len(alphabet))])
		}
		text := b.String()
		runes := []rune(text)
		i := rng.IntN(len(runes))
		j := i + rng.IntN(len(runes)-i) + 1
		pattern := string(runes[i:j])

		r, ok := Find(strings.ToLower(pattern), text)
		if !ok {
			t.Fatalf("Find(%q, %q) missed a substring of its own candidate", pattern, text)
		}
		checkInvariants(t, strings.ToLower(pattern), text, r)
	}
}

// TestScoreRewardsTheTighterMatch is the ranking property in the abstract: the
// same characters packed together must never score below the same characters
// spread apart.
func TestScoreRewardsTheTighterMatch(t *testing.T) {
	cases := []struct{ pattern, tight, loose string }{
		{"abc", "abc", "axbxc"},
		{"abcd", "abcd", "axxbxxcxxd"},
		{"abc", "zz-abc", "zz-aXbXc"},
		{"gc", "gcc", "gnome-calculator"},
	}
	for _, tc := range cases {
		tight, ok1 := Score(tc.pattern, tc.tight)
		loose, ok2 := Score(tc.pattern, tc.loose)
		if !ok1 || !ok2 {
			t.Fatalf("%q did not match one of %q / %q", tc.pattern, tc.tight, tc.loose)
		}
		if tight <= loose {
			t.Errorf("%q: %q scored %d, not above the spread-out %q at %d",
				tc.pattern, tc.tight, tight, tc.loose, loose)
		}
	}
}
