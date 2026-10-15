package vt_test

// Conformance for REP, repeat previous character.
//
// REP is how a program draws a rule without sending a hundred bytes, and
// terminfo advertises it as `rep`. ncurses reaches for it whenever the same
// character has to fill a run, which on a wide terminal is most horizontal
// lines.
//
// Cases follow ECMA-48's definition of REP, repeat the preceding graphic
// character, esctest tests/rep.py, and xterm's handling in charproc.c, which
// repeats whatever it last displayed.

import (
	"testing"
)

func TestConform_RepeatPreviousCharacter(t *testing.T) {
	runConform(t, []conformCase{
		{
			name:   "REP repeats an ASCII character",
			in:     "A\x1b[3b",
			want:   "AAAA",
			cursor: "4,0",
		}, {
			// An omitted or zero parameter means one, which is the rule for
			// every count-taking CSI.
			name: "an omitted parameter repeats once",
			in:   "A\x1b[b",
			want: "AA",
		}, {
			name: "a zero parameter repeats once",
			in:   "A\x1b[0b",
			want: "AA",
		}, {
			// The character to repeat is the last one displayed, and a
			// double-width one is a character like any other. Dropping it
			// leaves the guest's rule half drawn with no hint why.
			name:   "REP repeats a double-width character",
			cols:   8,
			in:     "世\x1b[2b",
			want:   "世世世",
			cursor: "6,0",
		}, {
			// A base with a combining mark is one character on screen, so REP
			// has to carry the mark. Repeating only the base silently changes
			// the text.
			name: "REP repeats a whole grapheme cluster",
			in:   "é\x1b[2b",
			want: "ééé",
		}, {
			name: "REP repeats an emoji",
			cols: 8,
			in:   "\U0001f44d\x1b[2b",
			want: "\U0001f44d\U0001f44d\U0001f44d",
		}, {
			// The designated set applies to the repeat as well, because REP
			// repeats the character the guest sent and the mapping is still in
			// force.
			name: "REP under a designated character set repeats the mapped glyph",
			in:   "\x1b(0q\x1b[2b",
			want: "───",
		}, {
			name: "REP wraps at the margin like any other printing",
			in:   "A\x1b[7b",
			want: "AAAAAA\nAA",
		}, {
			// A count larger than the screen cannot be allowed to spin: the
			// parameter arrives unclamped from the guest. Repeating stops
			// after one screenful, and the rows that ran off the top went to
			// scrollback like any other scrolled output.
			name:   "a count larger than the screen stops after one screenful",
			cols:   4,
			rows:   2,
			in:     "A\x1b[20b",
			want:   "AAAA\nA",
			cursor: "1,1",
		}, {
			// A parameter too large to hold is treated as absent by the
			// parser, so REP falls back to its default of one. xterm would
			// saturate and repeat until the screen filled. Either answer is
			// safe, and no program sends this on purpose; the case is here so
			// that the behaviour is a decision rather than an accident.
			name: "a parameter past the parameter limit repeats once",
			cols: 4,
			rows: 2,
			in:   "A\x1b[2147483647b",
			want: "AA",
		}, {
			name: "REP before anything has been printed does nothing",
			in:   "\x1b[3b",
			want: "",
		},
	})
}
