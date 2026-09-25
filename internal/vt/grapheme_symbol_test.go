package vt

import (
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

// TestGraphemeStringServesEverySymbolWhole checks the static table behind
// the symbol fast path: every rune in its range comes back as exactly its own
// UTF-8, and text outside the range or longer than one rune still comes back
// as the buffer.
func TestGraphemeStringServesEverySymbolWhole(t *testing.T) {
	e := NewEmulator(10, 2)
	for r := rune(symbolFirst - 1); r <= symbolEnd; r++ {
		e.grapheme = utf8.AppendRune(e.grapheme[:0], r)
		if got, want := e.graphemeString(), string(r); got != want {
			t.Fatalf("U+%04X: got %q, want %q", r, got, want)
		}
		e.grapheme = utf8.AppendRune(e.grapheme, 0x301)
		if got, want := e.graphemeString(), string(r)+"́"; got != want {
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
