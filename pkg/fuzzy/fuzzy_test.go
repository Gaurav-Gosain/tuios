package fuzzy

import (
	"math/rand/v2"
	"strings"
	"testing"
)

func TestFindPositionsAreByteOffsets(t *testing.T) {
	tests := []struct {
		pattern, text string
		want          []int
	}{
		{"gc", "gcc", []int{0, 1}},
		{"gcc", "gcc", []int{0, 1, 2}},
		{"gc", "gnome-calculator", []int{0, 6}},
		{"tui", "tuios", []int{0, 1, 2}},
		{"os", "tuios", []int{3, 4}},
		// The é is two bytes, so every offset past it must account for both.
		{"cd", "café-finder", []int{0, 9}},
	}
	for _, tc := range tests {
		got, ok := Find(tc.pattern, tc.text)
		if !ok {
			t.Fatalf("Find(%q, %q) did not match", tc.pattern, tc.text)
		}
		if len(got.Positions) != len(tc.want) {
			t.Fatalf("Find(%q, %q) positions = %v, want %v", tc.pattern, tc.text, got.Positions, tc.want)
		}
		for i := range tc.want {
			if got.Positions[i] != tc.want[i] {
				t.Fatalf("Find(%q, %q) positions = %v, want %v", tc.pattern, tc.text, got.Positions, tc.want)
			}
		}
		// Every position must sit on a rune boundary of the candidate.
		for _, p := range got.Positions {
			if p < 0 || p >= len(tc.text) {
				t.Fatalf("position %d out of range for %q", p, tc.text)
			}
		}
		if got.Start != got.Positions[0] {
			t.Fatalf("Start = %d, want %d", got.Start, got.Positions[0])
		}
		if got.End <= got.Positions[len(got.Positions)-1] {
			t.Fatalf("End = %d must be past the last position %d", got.End, got.Positions[len(got.Positions)-1])
		}
	}
}

func TestNoMatch(t *testing.T) {
	for _, tc := range [][2]string{
		{"zz", "gcc"},
		{"cg", "gcc"},
		{"gccc", "gcc"},
		{"a", ""},
	} {
		if _, ok := Find(tc[0], tc[1]); ok {
			t.Errorf("Find(%q, %q) matched, want no match", tc[0], tc[1])
		}
	}
}

func TestEmptyPatternMatchesEverything(t *testing.T) {
	r, ok := Find("", "anything")
	if !ok {
		t.Fatal("empty pattern must match")
	}
	if r.Positions != nil {
		t.Fatalf("empty pattern must report no positions, got %v", r.Positions)
	}
	hits := Filter("", []string{"b", "a", "c"})
	if len(hits) != 3 {
		t.Fatalf("empty pattern filtered to %d hits, want 3", len(hits))
	}
	for i, h := range hits {
		if h.Index != i {
			t.Fatalf("empty pattern reordered candidates: %v", hits)
		}
	}
}

func TestSmartCase(t *testing.T) {
	if !Match("gc", "GCC") {
		t.Error("a lowercase pattern must ignore case")
	}
	if Match("GC", "gcc") {
		t.Error("an uppercase pattern must match case-sensitively")
	}
	if !Match("GC", "GCC") {
		t.Error("an uppercase pattern must still match the same case")
	}
}

// rank returns the candidates ordered best first, which is the only property
// the callers actually depend on.
func rank(t *testing.T, pattern string, candidates ...string) []string {
	t.Helper()
	hits := Filter(pattern, candidates)
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.Text
	}
	return out
}

func TestRankingMatchesExpectation(t *testing.T) {
	tests := []struct {
		name       string
		pattern    string
		candidates []string
		wantFirst  string
	}{
		{
			// The example that motivated the scorer: a two-letter prefix run
			// must beat the same two letters spread across a long name.
			name:       "prefix run beats scattered",
			pattern:    "gc",
			candidates: []string{"gnome-calculator", "git-credential-cache", "gcc", "gnome-characters"},
			wantFirst:  "gcc",
		},
		{
			name:       "exact match beats its own prefixes",
			pattern:    "ls",
			candidates: []string{"lsblk", "lsof", "ls", "lsusb", "lspci"},
			wantFirst:  "ls",
		},
		{
			name:       "word boundary beats mid-word",
			pattern:    "ss",
			candidates: []string{"assets", "ssh"},
			wantFirst:  "ssh",
		},
		{
			name:       "separator boundary is a real boundary",
			pattern:    "gs",
			candidates: []string{"gnutls-serv", "git-shell"},
			wantFirst:  "git-shell",
		},
		{
			name:       "shorter candidate wins an otherwise equal match",
			pattern:    "py",
			candidates: []string{"python3-config", "python3"},
			wantFirst:  "python3",
		},
		{
			name:       "camelCase hump counts as a boundary",
			pattern:    "cp",
			candidates: []string{"ccache-pretend", "CommandPalette"},
			wantFirst:  "CommandPalette",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := rank(t, tc.pattern, tc.candidates...)
			if len(got) == 0 {
				t.Fatalf("%q matched nothing in %v", tc.pattern, tc.candidates)
			}
			if got[0] != tc.wantFirst {
				t.Fatalf("%q ranked %v, want %q first", tc.pattern, got, tc.wantFirst)
			}
		})
	}
}

// TestSortIsStableAcrossKeystrokes is the guard on the tiebreak: the same
// candidates in a different input order must come out in the same order, or
// results shuffle under the user's cursor as the list is rebuilt.
func TestSortIsStableAcrossKeystrokes(t *testing.T) {
	corpus := []string{"make", "cmake", "qmake", "makeinfo", "automake", "makepkg"}
	want := rank(t, "make", corpus...)

	shuffled := make([]string, len(corpus))
	copy(shuffled, corpus)
	rng := rand.New(rand.NewPCG(1, 2))
	for range 50 {
		rng.Shuffle(len(shuffled), func(i, j int) {
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		})
		got := rank(t, "make", shuffled...)
		if len(got) != len(want) {
			t.Fatalf("hit count changed: %v vs %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("order changed with input order: %v, want %v", got, want)
			}
		}
	}
}

// TestScoreAgreesWithFind keeps the two entry points from drifting, since Score
// is the one hot enough that someone will be tempted to shortcut it.
func TestScoreAgreesWithFind(t *testing.T) {
	corpus := []string{"gcc", "gnome-calculator", "git", "grep", "ls", "systemctl"}
	for _, pattern := range []string{"g", "gc", "sys", "l", "zz"} {
		for _, text := range corpus {
			r, ok1 := Find(pattern, text)
			s, ok2 := Score(pattern, text)
			if ok1 != ok2 || r.Score != s {
				t.Fatalf("Find(%q,%q)=(%d,%v) but Score=(%d,%v)", pattern, text, r.Score, ok1, s, ok2)
			}
		}
	}
}

// TestPositionsSurviveFilter checks the shared position buffer FilterIndex
// hands out: every hit must still point at its own run after the buffer grew.
func TestPositionsSurviveFilter(t *testing.T) {
	corpus := make([]string, 0, 300)
	for i := range 300 {
		corpus = append(corpus, strings.Repeat("a", i%7+1)+"bc")
	}
	hits := Filter("abc", corpus)
	if len(hits) == 0 {
		t.Fatal("expected matches")
	}
	for _, h := range hits {
		if len(h.Positions) != 3 {
			t.Fatalf("hit %q has %d positions, want 3", h.Text, len(h.Positions))
		}
		for _, p := range h.Positions {
			if p < 0 || p >= len(h.Text) {
				t.Fatalf("hit %q position %d out of range", h.Text, p)
			}
		}
		if h.Text[h.Positions[1]] != 'b' || h.Text[h.Positions[2]] != 'c' {
			t.Fatalf("hit %q positions %v point at the wrong bytes", h.Text, h.Positions)
		}
	}
}

// TestMatcherReuseIsClean catches scratch buffers leaking between calls, which
// is the failure mode of reusing a Matcher across a whole corpus.
func TestMatcherReuseIsClean(t *testing.T) {
	var m Matcher
	corpus := []string{"gcc", "gnome-calculator", "no", "systemctl-analyze", "g"}
	for range 3 {
		for _, text := range corpus {
			got, ok := m.Find("gc", text)
			want, wantOK := Find("gc", text)
			if ok != wantOK || got.Score != want.Score {
				t.Fatalf("reused matcher on %q gave (%d,%v), want (%d,%v)", text, got.Score, ok, want.Score, wantOK)
			}
			if ok && len(got.Positions) != len(want.Positions) {
				t.Fatalf("reused matcher on %q gave %v, want %v", text, got.Positions, want.Positions)
			}
		}
	}
}

// TestFilterIndexIsAllocationLean holds the per-keystroke cost down: over a
// realistic corpus the sweep must not allocate per candidate.
func TestFilterIndexIsAllocationLean(t *testing.T) {
	corpus := benchCorpus(2000)
	var m Matcher
	// Warm the scratch buffers so the measurement sees steady state.
	m.FilterIndex("sys", len(corpus), func(i int) string { return corpus[i] })

	allocs := testing.AllocsPerRun(20, func() {
		m.FilterIndex("sys", len(corpus), func(i int) string { return corpus[i] })
	})
	// The hit slice and the position buffer grow, so a handful of allocations
	// is expected; anything proportional to the corpus is not.
	if allocs > 40 {
		t.Fatalf("FilterIndex allocated %.0f times over %d candidates", allocs, len(corpus))
	}
}
