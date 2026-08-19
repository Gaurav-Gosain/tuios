package vt_test

import (
	"strings"
	"testing"
)

// CSI parameter parsing and cursor motion. Parameters are where a guest's
// output stops looking like anything a person would write: a program computing
// a column can emit a negative one, an overflowed one, or none at all, and the
// terminal has to have an answer for each.

func TestConform_CSIParameters(t *testing.T) {
	runConform(t, []conformCase{
		{
			name:   "CUP with no parameters homes the cursor",
			in:     "\x1b[3;3HX\x1b[H",
			cursor: "0,0",
			want:   "\n\n  X",
		},
		{
			name:   "an omitted first parameter takes its default",
			in:     "\x1b[;3HX",
			cursor: "3,0",
			want:   "  X",
		},
		{
			name:   "an omitted second parameter takes its default",
			in:     "\x1b[3;HX",
			cursor: "1,2",
			want:   "\n\nX",
		},
		{
			// A zero parameter means the default, which for CUP is row and
			// column one. Programs emit it constantly by computing an index
			// from a value that turned out to be empty.
			name:   "zero parameters mean one",
			in:     "\x1b[0;0HX",
			cursor: "1,0",
			want:   "X",
		},
		{
			name:   "parameters past the screen clamp to it",
			in:     "\x1b[99;99HX",
			cursor: "5,3",
			want:   "\n\n\n     X",
		},
		{
			name:   "parameters past what fits in a parameter clamp too",
			in:     "\x1b[99999;99999HX",
			cursor: "5,3",
			want:   "\n\n\n     X",
		},
		{
			name:   "extra parameters are ignored",
			in:     "\x1b[2;3;4;5HX",
			cursor: "3,1",
			want:   "\n  X",
		},
		{
			// The parser has a fixed parameter capacity. Running past it must
			// not corrupt the parameters it did keep, and must not wedge the
			// parser for what comes after.
			name:   "more parameters than the parser holds",
			in:     "\x1b[" + strings.Repeat("1;", 40) + "HX",
			cursor: "1,0",
			want:   "X",
		},
		{
			// CAN and SUB abandon a sequence in progress. What follows is
			// ordinary text, not the rest of the sequence.
			name:      "CAN abandons a sequence in progress",
			in:        "\x1b[1;1\x18Hxy",
			cursor:    "3,0",
			want:      "Hxy",
			unhandled: true,
		},
		{
			name:      "SUB abandons a sequence in progress",
			in:        "\x1b[1;1\x1aHxy",
			cursor:    "3,0",
			want:      "Hxy",
			unhandled: true,
		},
		{
			name:   "an ESC inside a sequence starts a new one",
			in:     "\x1b[1;\x1b[3;3HX",
			cursor: "3,2",
			want:   "\n\n  X",
		},
		{
			name:   "an unknown private mode is recorded, not printed",
			in:     "\x1b[?9999hAB",
			cursor: "2,0",
			want:   "AB",
		},
		{
			// DEL is ignored wherever it appears inside a sequence, so this
			// is CUU 1 followed by a printed B, not a broken sequence whose
			// bytes leak onto the screen.
			name:   "DEL inside a sequence is ignored, not printed",
			in:     "\x1b[1\x7fAB",
			want:   "B",
			cursor: "1,0",
		},
	})
}

func TestConform_CursorMotion(t *testing.T) {
	runConform(t, []conformCase{
		{
			name:   "CUF with no parameter moves one column",
			in:     "\x1b[CX",
			cursor: "2,0",
			want:   " X",
		},
		{
			name:   "CUF with a zero parameter moves one column",
			in:     "\x1b[0CX",
			cursor: "2,0",
			want:   " X",
		},
		{
			name:   "CUF stops at the last column",
			in:     "\x1b[99CX",
			cursor: "5,0",
			want:   "     X",
		},
		{
			name:   "CUB stops at the first column",
			in:     "\x1b[3;3H\x1b[99DX",
			cursor: "1,2",
			want:   "\n\nX",
		},
		{
			name:   "CUU stops at the first row",
			in:     "\x1b[3;3H\x1b[99AX",
			cursor: "3,0",
			want:   "  X",
		},
		{
			name:   "CUD stops at the last row",
			in:     "\x1b[3;3H\x1b[99BX",
			cursor: "3,3",
			want:   "\n\n\n  X",
		},
		{
			name:   "CHA addresses a column",
			in:     "\x1b[3;3H\x1b[5GX",
			cursor: "5,2",
			want:   "\n\n    X",
		},
		{
			name:   "VPA addresses a row and keeps the column",
			in:     "\x1b[1;3H\x1b[3dX",
			cursor: "3,2",
			want:   "\n\n  X",
		},
		{
			name:   "CNL moves down and to column one",
			in:     "\x1b[1;4H\x1b[2EX",
			cursor: "1,2",
			want:   "\n\nX",
		},
		{
			name:   "CPL moves up and to column one",
			in:     "\x1b[3;4H\x1b[2FX",
			cursor: "1,0",
			want:   "X",
		},
		{
			name:   "HPR moves the cursor right",
			in:     "\x1b[1;2H\x1b[2aX",
			cursor: "4,0",
			want:   "   X",
		},
		{
			name:   "backspace at the first column stays there",
			in:     "\b\bX",
			cursor: "1,0",
			want:   "X",
		},
	})
}

func TestConform_Erase(t *testing.T) {
	const fill = "abcdef\r\nghijkl\r\nmnopqr\r\nstuvwx"

	runConform(t, []conformCase{
		{
			name: "EL clears to the end of the line",
			in:   fill + "\x1b[2;3H\x1b[K",
			want: "abcdef\ngh\nmnopqr\nstuvwx",
		},
		{
			name: "EL 1 clears to the start of the line, inclusive",
			in:   fill + "\x1b[2;3H\x1b[1K",
			want: "abcdef\n   jkl\nmnopqr\nstuvwx",
		},
		{
			name: "EL 2 clears the whole line",
			in:   fill + "\x1b[2;3H\x1b[2K",
			want: "abcdef\n\nmnopqr\nstuvwx",
		},
		{
			name: "ED clears to the end of the screen",
			in:   fill + "\x1b[2;3H\x1b[J",
			want: "abcdef\ngh",
		},
		{
			name: "ED 1 clears to the start of the screen, inclusive",
			in:   fill + "\x1b[2;3H\x1b[1J",
			want: "\n   jkl\nmnopqr\nstuvwx",
		},
		{
			name: "ED 2 clears the whole screen",
			in:   fill + "\x1b[2;3H\x1b[2J",
			want: "",
		},
		{
			// Erasing does not move the cursor, which is what lets a program
			// clear a line and rewrite it in place.
			name:   "ED 2 leaves the cursor alone",
			in:     fill + "\x1b[2;3H\x1b[2J",
			cursor: "2,1",
			want:   "",
		},
		{
			name: "EL ignores the scroll region",
			in:   fill + "\x1b[2;3r\x1b[1;1H\x1b[K",
			want: "\nghijkl\nmnopqr\nstuvwx",
		},
		{
			name: "ECH erases in place without shifting",
			in:   fill + "\x1b[1;2H\x1b[3X",
			want: "a   ef\nghijkl\nmnopqr\nstuvwx",
		},
		{
			name: "ECH with a zero parameter erases one cell",
			in:   fill + "\x1b[1;2H\x1b[0X",
			want: "a cdef\nghijkl\nmnopqr\nstuvwx",
		},
		{
			name: "ECH past the end of the line stops there",
			in:   fill + "\x1b[1;5H\x1b[99X",
			want: "abcd\nghijkl\nmnopqr\nstuvwx",
		},
	})
}
