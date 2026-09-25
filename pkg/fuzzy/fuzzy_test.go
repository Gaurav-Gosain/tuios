package fuzzy

import (
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
