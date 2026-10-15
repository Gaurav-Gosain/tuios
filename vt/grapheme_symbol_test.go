package vt

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestStyledSymbolCellsAllocateNothing pins the cost of the cells a TUI draws
// its borders, meters and graphs with: one box drawing, block or braille
// character between two SGRs. Each one used to cost a string allocation for
// its cell content, which on a btop or nvim replay was most of the bytes the
// emulator allocated.
func TestStyledSymbolCellsAllocateNothing(t *testing.T) {
	e := NewEmulator(80, 24)
	seq := "\x1b[H\x1b[31m─\x1b[32m│\x1b[33m█\x1b[34m⣿\x1b[35m→\x1b[m"
	e.WriteString(seq)
	if got := testing.AllocsPerRun(200, func() { e.WriteString(seq) }); got > 0 {
		t.Errorf("styled symbol cells allocate %.1f times per write, want 0", got)
	}
}

// TestClusterStringServesEverySymbolWhole checks the static table behind
// the symbol fast path: every rune in its range comes back as exactly its own
// UTF-8, and text outside the range or longer than one rune still comes back
// as the buffer.
func TestClusterStringServesEverySymbolWhole(t *testing.T) {
	e := NewEmulator(10, 2)
	whole := func() string {
		var run clusterRun
		return run.str(e, e.grapheme, len(e.grapheme))
	}
	for r := rune(symbolFirst - 1); r <= symbolEnd; r++ {
		e.grapheme = utf8.AppendRune(e.grapheme[:0], r)
		if got, want := whole(), string(r); got != want {
			t.Fatalf("U+%04X: got %q, want %q", r, got, want)
		}
		e.grapheme = utf8.AppendRune(e.grapheme, 0x301)
		if got, want := whole(), string(r)+"́"; got != want {
			t.Fatalf("U+%04X with a mark: got %q, want %q", r, got, want)
		}
	}
}

// TestSymbolClusterExtendsAcrossWrites checks that a symbol drawn from the
// static table at the end of one Write still takes a continuation that
// arrives in the next, the same as the unsplit write.
func TestSymbolClusterExtendsAcrossWrites(t *testing.T) {
	for _, tc := range []struct{ first, second string }{
		{"\x1b[31m→", "️!"},
		{"\x1b[31m─", "́x"},
		{"\x1b[31m⣿", "⃝\x1b[m"},
	} {
		whole := NewEmulator(10, 2)
		whole.WriteString(tc.first + tc.second)
		split := NewEmulator(10, 2)
		split.WriteString(tc.first)
		split.WriteString(tc.second)
		for x := range 10 {
			w, s := whole.CellAt(x, 0), split.CellAt(x, 0)
			if (w == nil) != (s == nil) || (w != nil && (w.Content != s.Content || w.Width != s.Width)) {
				t.Errorf("%q + %q: cell %d is %+v split, %+v whole", tc.first, tc.second, x, s, w)
			}
		}
	}
}

// TestRepeatedClustersAllocateNothing pins the cost of text outside ASCII and
// the symbol block that repeats, as CJK text, a redrawn status line and emoji
// lists do. Each run of such text used to cost a string allocation for the
// cells drawn from it; a cluster seen recently now comes from the table.
func TestRepeatedClustersAllocateNothing(t *testing.T) {
	e := NewEmulator(80, 4)
	seq := "\x1b[H日本語のテキスト 中文字符 한국어 émoji 😀🎉👍🏽 👨‍👩‍👧 é\x1b[31mä\x1b[m"
	e.WriteString(seq)
	if got := testing.AllocsPerRun(200, func() { e.WriteString(seq) }); got > 0 {
		t.Errorf("repeated clusters allocate %.1f times per write, want 0", got)
	}
}

// TestNovelClusterRunAllocatesBoundedly checks the other side: a long run of
// clusters the table has never seen costs a bounded number of allocations, not
// one per character, and every cell still holds its own character.
func TestNovelClusterRunAllocatesBoundedly(t *testing.T) {
	e := NewEmulator(200, 4)
	next := rune(0x4E00)
	run := func() string {
		var b strings.Builder
		b.WriteString("\x1b[H")
		for range 100 {
			b.WriteRune(next)
			next++
		}
		return b.String()
	}
	runs := make([]string, 101)
	for i := range runs {
		runs[i] = run()
	}
	i := 0
	got := testing.AllocsPerRun(100, func() {
		e.WriteString(runs[i])
		i++
	})
	if got > maxRunMisses+1 {
		t.Errorf("a run of 100 new clusters allocates %.1f times, want at most %d", got, maxRunMisses+1)
	}
	for x := range 100 {
		want := string(rune(0x4E00 + 100*100 + x))
		if c := e.CellAt(2*x, 0); c == nil || c.Content != want {
			t.Fatalf("cell %d holds %+v, want %q", 2*x, c, want)
		}
	}
}
