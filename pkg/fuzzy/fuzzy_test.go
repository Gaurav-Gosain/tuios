package fuzzy

import (
	"math/rand/v2"
	"strings"
	"testing"
)

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
