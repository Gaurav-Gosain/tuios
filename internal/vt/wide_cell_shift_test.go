package vt

import (
	"strings"
	"testing"
)

// rowRunes renders a row the way a user would read it: the lead of a wide rune
// contributes its text and its continuation cells contribute nothing, so the
// string's length is the row's column count only when every rune is narrow.
func rowRunes(e *Emulator, y int) string {
	var b strings.Builder
	for x := range e.Width() {
		c := e.CellAt(x, y)
		if c == nil {
			b.WriteString("?")
			continue
		}
		b.WriteString(c.Content)
	}
	return strings.TrimRight(b.String(), " ")
}

// TestCellShiftKeepsWideRunes pins the behaviour that InsertCell and DeleteCell
// shift cells without destroying the double-width runes they move.
//
// The expected values are not invented. They were taken from ghostty by way of
// the tuitest harness, which fixed the identical defect in its own vendored
// emulator and validated it against ghostty directly. tmux agrees on every
// insert and on even deletes, and differs on an odd delete: where a deletion
// cuts a wide rune in half, tmux drops the whole rune and shifts by two columns,
// while ghostty removes the one column it was asked to and blanks the half left
// standing. Following ghostty keeps a delete of n columns a delete of exactly n
// columns, which is what the sequence means.
func TestCellShiftKeepsWideRunes(t *testing.T) {
	for _, tc := range []struct {
		name  string
		start string
		seq   string
		want  string
	}{
		// A row of nothing but wide runes: the case that used to empty the line.
		{"delete 1 of wide", "中日本", "\x1b[1P", " 日本"},
		{"delete 2 of wide", "中日本", "\x1b[2P", "日本"},
		{"delete 3 of wide", "中日本", "\x1b[3P", " 本"},
		{"insert 1 of wide", "中日本", "\x1b[1@", " 中日本"},
		{"insert 2 of wide", "中日本", "\x1b[2@", "  中日本"},
		{"insert 3 of wide", "中日本", "\x1b[3@", "   中日本"},

		// Narrow runes on both sides, so the shift crosses a boundary in each
		// direction rather than only ever starting on a lead.
		{"delete 1 of mixed", "ab中日本cd", "\x1b[1P", "b中日本cd"},
		{"delete 3 of mixed", "ab中日本cd", "\x1b[3P", " 日本cd"},
		{"insert 1 of mixed", "ab中日本cd", "\x1b[1@", " ab中日本cd"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := NewEmulator(20, 3)
			e.WriteString(tc.start)
			e.WriteString("\x1b[H")
			e.WriteString(tc.seq)
			if got := rowRunes(e, 0); got != tc.want {
				t.Errorf("after %q on %q:\n got %q\nwant %q", tc.seq, tc.start, got, tc.want)
			}
		})
	}
}

// TestCellShiftLeavesNoOrphanedHalf states the invariant the repair pass exists
// to hold, over every shift width against a row that is entirely wide runes.
// Checking the invariant rather than a rendering catches the case where a row
// reads correctly but carries a continuation cell with no lead in front of it,
// which draws as a blank that nothing can ever overwrite.
func TestCellShiftLeavesNoOrphanedHalf(t *testing.T) {
	for _, seq := range []string{"P", "@"} {
		for n := 1; n <= 6; n++ {
			e := NewEmulator(20, 3)
			e.WriteString("中日本語")
			e.WriteString("\x1b[H")
			e.WriteString("\x1b[" + string(rune('0'+n)) + seq)

			for x := 0; x < e.Width(); x++ {
				c := e.CellAt(x, 0)
				if c == nil || c.Width != 0 {
					continue
				}
				// A continuation must be preceded by a lead wide enough to
				// reach it.
				lead := e.CellAt(x-1, 0)
				if x == 0 || lead == nil || lead.Width < 2 {
					t.Errorf("CSI %d%s: column %d is a continuation with no lead", n, seq, x)
				}
			}
		}
	}
}
