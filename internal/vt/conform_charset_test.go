package vt_test

// Conformance for character set designation and the shift states.
//
// This is the oldest machinery in a terminal and the least exercised by modern
// programs, which is exactly why it rots quietly. It is not dead, though:
// terminfo for `screen`, `linux` and `vt100` all spell smacs/rmacs as SO and SI
// rather than as ESC ( 0 / ESC ( B, so anything running under those TERM values
// draws its boxes through the locking shift. A multiplexer hosts whatever the
// guest picked, so both spellings have to work.
//
// Cases follow esctest (tests/esctest/esctest/tests/{si,so,decsc}.py), ghostty's
// src/terminal/charsets.zig, and the VT510 reference manual's tables for SCS,
// LS0-LS3 and SS2/SS3.

import (
	"testing"
)

func TestConform_CharsetDesignation(t *testing.T) {
	runConform(t, []conformCase{
		{
			// The DEC Special Graphics set is what draws a box. ESC ( 0
			// designates it as G0, which is what GL selects by default.
			name: "ESC ( 0 designates DEC special graphics as G0",
			in:   "\x1b(0lqk",
			want: "┌─┐",
		}, {
			name: "ESC ( B puts ASCII back",
			in:   "\x1b(0lqk\x1b(Bxyz",
			want: "┌─┐xyz",
		}, {
			// The UK set differs from ASCII in exactly one place, which is the
			// only way to tell it was selected at all.
			name: "ESC ( A designates the UK set as G0",
			in:   "\x1b(Aa$b",
			want: "a£b",
		}, {
			// A designator this emulator does not carry has to say so rather
			// than silently leaving the previous set in place, because a guest
			// that asked for a set it did not get will keep sending bytes for
			// it.
			name:      "an unknown final is rejected",
			in:        "\x1b(<abc",
			want:      "abc",
			unhandled: true,
		}, {
			// Designation on its own changes nothing on screen: it loads a
			// slot, and only a shift selects the slot.
			name: "designating G1 alone does not change what prints",
			in:   "\x1b)0lqk",
			want: "lqk",
		},
	})
}

// TestConform_LockingShifts covers SO, SI and the ESC-form locking shifts.
//
// SO (0x0E) selects G1 into GL and SI (0x0F) selects G0 back. esctest's
// tests/so.py and tests/si.py assert exactly this pairing.
func TestConform_LockingShifts(t *testing.T) {
	runConform(t, []conformCase{
		{
			name: "SO selects G1 and SI returns to G0",
			in:   "\x1b)0\x0elqk\x0flqk",
			want: "┌─┐lqk",
		}, {
			// This is the terminfo shape: enacs designates, smacs shifts out,
			// rmacs shifts in. It is what a curses program under TERM=screen
			// sends for every line of every box it draws.
			name: "the terminfo enacs/smacs/rmacs shape draws a box",
			in:   "\x1b(B\x1b)0\x0eqqq\x0fab",
			want: "───ab",
		}, {
			name: "SO with nothing designated in G1 leaves ASCII alone",
			in:   "\x0elqk",
			want: "lqk",
		}, {
			name: "LS2 selects G2",
			in:   "\x1b*0\x1bnlqk",
			want: "┌─┐",
		}, {
			name: "LS3 selects G3",
			in:   "\x1b+0\x1bolqk",
			want: "┌─┐",
		}, {
			// A locking shift is locked: it holds until another shift, not
			// until the next character.
			name: "a locking shift holds across a cursor move",
			in:   "\x1b)0\x0eq\x1b[1;4Hq",
			want: "─  ─",
		},
	})
}

// TestConform_SingleShifts covers SS2 and SS3.
//
// A single shift selects G2 or G3 for exactly one character and then falls back
// to whatever GL was, which is the whole difference from a locking shift. Both
// the seven-bit form (ESC N, ESC O) and the eight-bit C1 form (0x8E, 0x8F) mean
// the same thing; a guest that has not asked for eight-bit controls sends the
// ESC form, and that is the one nearly everything uses.
func TestConform_SingleShifts(t *testing.T) {
	runConform(t, []conformCase{
		{
			name: "SS2 shifts exactly one character",
			in:   "\x1b*0\x1bNqq",
			want: "─q",
		}, {
			name: "SS3 shifts exactly one character",
			in:   "\x1b+0\x1bOqq",
			want: "─q",
		}, {
			// The single shift has to give GL back, not leave it pointing at
			// G2 forever.
			name: "SS2 does not disturb the locking shift underneath it",
			in:   "\x1b)0\x1b*A\x0eq\x1bN$q",
			want: "─£─",
		}, {
			name: "the eight-bit SS2 means the same thing",
			in:   "\x1b*0\x8eqq",
			want: "─q",
		}, {
			name: "the eight-bit SS3 means the same thing",
			in:   "\x1b+0\x8fqq",
			want: "─q",
		},
	})
}

// TestConform_CharsetsAcrossSaveAndReset checks that the shift state travels
// with the saved cursor. The VT510 manual lists the character set state among
// what DECSC saves, and esctest's tests/decsc.py checks it; a program that
// saves, designates, prints and restores expects its old set back.
func TestConform_CharsetsAcrossSaveAndReset(t *testing.T) {
	runConform(t, []conformCase{
		{
			name: "DECSC and DECRC carry the locking shift, not just the designation",
			in:   "\x1b)0\x0e\x1b7\x0f\x1b8q",
			want: "─",
		}, {
			name: "DECRC clears a pending single shift",
			in:   "\x1b*0\x1b7\x1bN\x1b8qq",
			want: "qq",
		}, {
			name: "RIS puts every slot back to ASCII and GL back to G0",
			in:   "\x1b)0\x0e\x1bcq",
			want: "q",
		}, {
			name: "a soft reset puts every slot back to ASCII and GL back to G0",
			in:   "\x1b)0\x0e\x1b[!pq",
			want: "q",
		},
	})
}

// TestConform_SpecialGraphicsTable walks the whole DEC Special Graphics set.
//
// Only `q` was ever asserted before, which would have let any of the other
// thirty mappings drift without a single test noticing. The expected glyphs are
// the ones in the VT100 programmer's manual, and they match ghostty's table in
// src/terminal/charsets.zig.
func TestConform_SpecialGraphicsTable(t *testing.T) {
	// Rendered a few at a time so a failure prints a readable row rather than
	// one 31-column wall.
	for _, tc := range []struct{ in, want string }{
		{"`abc", "◆▒␉␌"},
		{"defg", "␍␊°±"},
		{"hijk", "␤␋┘┐"},
		{"lmno", "┌└┼⎺"},
		{"pqrs", "⎻─⎼⎽"},
		{"tuvw", "├┤┴┬"},
		{"xyz{", "│⩽⩾π"},
		{"|}~", "≠£·"},
	} {
		runConform(t, []conformCase{{
			name: "DEC special graphics " + tc.in,
			cols: 8,
			in:   "\x1b(0" + tc.in,
			want: tc.want,
		}})
	}
}
