package vt_test

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// cellRow renders one row as "content/width" per occupied cell, so a test can
// say exactly which cluster sits in which column and how wide the emulator
// thinks it is. A screen dump alone cannot: a dropped wide character and a
// wrapped one both leave the same blank column behind.
func cellRow(emu *vt.Emulator, y int) string {
	var b strings.Builder
	for x := range emu.Width() {
		c := emu.CellAt(x, y)
		if x > 0 {
			b.WriteByte(' ')
		}
		if c == nil {
			b.WriteString("<nil>")
			continue
		}
		b.WriteString(c.Content)
		b.WriteByte('/')
		b.WriteByte(byte('0' + c.Width))
	}
	return b.String()
}

// TestWideGrapheme_WrapsWholeAtRightMargin covers a character that disappeared.
// A double-width cluster printed where only one column was left was written as
// a two-cell character into the last column, which the cell buffer refuses, so
// the guest's character vanished with nothing on screen to show for it. Any CJK
// or emoji text meeting the right margin at an odd column lost a character.
func TestWideGrapheme_WrapsWholeAtRightMargin(t *testing.T) {
	tests := []struct {
		name       string
		cols       int
		in         string
		wantRow0   string
		wantRow1   string
		wantCursor [2]int
	}{
		{
			name:       "wide fits in the last two columns",
			cols:       6,
			in:         "abcd世",
			wantRow0:   "a/1 b/1 c/1 d/1 世/2 /0",
			wantRow1:   " /1  /1  /1  /1  /1  /1",
			wantCursor: [2]int{5, 0},
		},
		{
			name:       "wide with one column left wraps whole",
			cols:       6,
			in:         "abcde世",
			wantRow0:   "a/1 b/1 c/1 d/1 e/1  /1",
			wantRow1:   "世/2 /0  /1  /1  /1  /1",
			wantCursor: [2]int{2, 1},
		},
		{
			name:       "odd screen width leaves a blank and wraps",
			cols:       5,
			in:         "世世世",
			wantRow0:   "世/2 /0 世/2 /0  /1",
			wantRow1:   "世/2 /0  /1  /1  /1",
			wantCursor: [2]int{2, 1},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			emu := vt.NewEmulator(tc.cols, 3)
			if _, err := emu.WriteString(tc.in); err != nil {
				t.Fatalf("write: %v", err)
			}
			if got := cellRow(emu, 0); got != tc.wantRow0 {
				t.Errorf("row 0 = %q\n    want %q", got, tc.wantRow0)
			}
			if got := cellRow(emu, 1); got != tc.wantRow1 {
				t.Errorf("row 1 = %q\n    want %q", got, tc.wantRow1)
			}
			p := emu.CursorPosition()
			if [2]int{p.X, p.Y} != tc.wantCursor {
				t.Errorf("cursor = %d,%d, want %d,%d", p.X, p.Y, tc.wantCursor[0], tc.wantCursor[1])
			}
		})
	}
}

// TestWideGrapheme_ArmsPendingWrapAtMargin covers the other half of the same
// bug. A wide cluster ending flush against the right margin left the cursor
// sitting on its own second cell without arming the pending wrap, so the next
// character overwrote the wide character instead of starting a new line.
func TestWideGrapheme_ArmsPendingWrapAtMargin(t *testing.T) {
	emu := vt.NewEmulator(6, 3)
	if _, err := emu.WriteString("世世世X"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got, want := cellRow(emu, 0), "世/2 /0 世/2 /0 世/2 /0"; got != want {
		t.Errorf("row 0 = %q\n    want %q", got, want)
	}
	if got, want := cellRow(emu, 1), "X/1  /1  /1  /1  /1  /1"; got != want {
		t.Errorf("row 1 = %q\n    want %q", got, want)
	}
}

// TestWideGrapheme_NoWrapLeavesColumnBlank checks that with autowrap off there
// is still no half a character left in the last column.
func TestWideGrapheme_NoWrapLeavesColumnBlank(t *testing.T) {
	emu := vt.NewEmulator(6, 2)
	if _, err := emu.WriteString("\x1b[?7labcde世X"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got, want := cellRow(emu, 0), "a/1 b/1 c/1 d/1 e/1 X/1"; got != want {
		t.Errorf("row 0 = %q\n    want %q", got, want)
	}
	if got, want := cellRow(emu, 1), " /1  /1  /1  /1  /1  /1"; got != want {
		t.Errorf("row 1 = %q\n    want %q", got, want)
	}
}

// TestGrapheme_ASCIIBaseKeepsCombiningMarks covers an accent that disappeared.
// The printable-ASCII fast path drew its character and moved on, so a combining
// mark arriving next was treated as a cluster of its own and put in the
// following cell, where the next character overwrote it. Decomposed text, which
// is what NFD filenames and several locales produce, rendered stripped of its
// accents.
func TestGrapheme_ASCIIBaseKeepsCombiningMarks(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		split []string
		want  string
	}{
		{
			name: "accented e in one write",
			in:   "éX",
			want: "é/1 X/1  /1  /1  /1  /1",
		},
		{
			name: "two marks on one base",
			in:   "é̂X",
			want: "é̂/1 X/1  /1  /1  /1  /1",
		},
		{
			name: "keycap sequence is one cluster",
			in:   "1️⃣X",
			want: "1️⃣/2 /0 X/1  /1  /1  /1",
		},
		{
			name: "decomposed word",
			in:   "café",
			want: "c/1 a/1 f/1 é/1  /1  /1",
		},
		{
			// A PTY read can end anywhere, including between a base and its
			// marks. The cluster has to survive the split.
			name:  "split across writes",
			split: []string{"e", "́X"},
			want:  "é/1 X/1  /1  /1  /1  /1",
		},
		{
			name:  "split between every rune",
			split: []string{"1", "️", "⃣", "X"},
			want:  "1️⃣/2 /0 X/1  /1  /1  /1",
		},
		{
			// An escape sequence closes the cluster, so a mark after one is not
			// pulled back onto a character the cursor has already left.
			name: "cursor move ends the cluster",
			in:   "e\x1b[1;4H́",
			want: "e/1  /1  /1 ́/0  /1  /1",
		},
		{
			// A designated character set maps the byte to something else, and
			// rebuilding the cell from the byte the guest sent would undo the
			// mapping. With a set designated the cluster is closed rather than
			// left open, so the glyph survives even though the mark does not
			// join it. A combining mark on a line-drawing glyph means nothing
			// anyway; losing the glyph would be visible.
			name: "a designated character set is not undone by a mark",
			in:   "\x1b(0q́",
			want: "─/1 ́/0  /1  /1  /1  /1",
		},
		{
			name: "reselecting ASCII lets marks join again",
			in:   "\x1b(0q\x1b(Bq́",
			want: "─/1 q́/1  /1  /1  /1  /1",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			emu := vt.NewEmulator(6, 2)
			writes := tc.split
			if writes == nil {
				writes = []string{tc.in}
			}
			for _, w := range writes {
				if _, err := emu.WriteString(w); err != nil {
					t.Fatalf("write %q: %v", w, err)
				}
			}
			if got := cellRow(emu, 0); got != tc.want {
				t.Errorf("row 0 = %q\n    want %q", got, tc.want)
			}
		})
	}
}

// TestGrapheme_CombiningAtRightMarginStaysInItsCell checks that a mark arriving
// on the last character of a full row extends that character rather than
// wrapping on its own, and that a mark which would widen a cluster past the edge
// does not cost the base character.
func TestGrapheme_CombiningAtRightMarginStaysInItsCell(t *testing.T) {
	emu := vt.NewEmulator(6, 2)
	if _, err := emu.WriteString("abcdef́X"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got, want := cellRow(emu, 0), "a/1 b/1 c/1 d/1 e/1 f́/1"; got != want {
		t.Errorf("row 0 = %q\n    want %q", got, want)
	}
	if got, want := cellRow(emu, 1), "X/1  /1  /1  /1  /1  /1"; got != want {
		t.Errorf("row 1 = %q\n    want %q", got, want)
	}

	// A variation selector turns a narrow base wide. In the last column there
	// is no room for the second cell, and widening anyway would have the buffer
	// drop the cell and the character with it.
	emu2 := vt.NewEmulator(6, 2)
	if _, err := emu2.WriteString("abcde#️⃣"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got, want := cellRow(emu2, 0), "a/1 b/1 c/1 d/1 e/1 #️⃣/1"; got != want {
		t.Errorf("row 0 = %q\n    want %q", got, want)
	}
}
