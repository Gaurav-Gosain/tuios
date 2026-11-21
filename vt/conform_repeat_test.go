package vt_test

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
			name: "an omitted parameter repeats once",
			in:   "A\x1b[b",
			want: "AA",
		}, {
			name: "a zero parameter repeats once",
			in:   "A\x1b[0b",
			want: "AA",
		}, {
			name:   "REP repeats a double-width character",
			cols:   8,
			in:     "世\x1b[2b",
			want:   "世世世",
			cursor: "6,0",
		}, {
			name: "REP repeats a whole grapheme cluster",
			in:   "é\x1b[2b",
			want: "ééé",
		}, {
			name: "REP repeats an emoji",
			cols: 8,
			in:   "\U0001f44d\x1b[2b",
			want: "\U0001f44d\U0001f44d\U0001f44d",
		}, {
			name: "REP under a designated character set repeats the mapped glyph",
			in:   "\x1b(0q\x1b[2b",
			want: "───",
		}, {
			name: "REP wraps at the margin like any other printing",
			in:   "A\x1b[7b",
			want: "AAAAAA\nAA",
		}, {
			name:   "a count larger than the screen stops after one screenful",
			cols:   4,
			rows:   2,
			in:     "A\x1b[20b",
			want:   "AAAA\nA",
			cursor: "1,1",
		}, {
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
