package vt_test

import "testing"

// Insert and delete, of lines and of characters, at the boundaries where they
// are decided: the edges of the screen, the edges of a scroll region, and the
// cell next to a double-width character.

func TestConform_InsertDeleteLines(t *testing.T) {
	const fill = "a\r\nb\r\nc\r\nd"

	runConform(t, []conformCase{
		{
			name: "IL pushes the rest down and drops the last",
			in:   fill + "\x1b[2;1H\x1b[1L",
			want: "a\n\nb\nc",
		},
		{
			name: "IL of several lines",
			in:   fill + "\x1b[2;1H\x1b[2L",
			want: "a\n\n\nb",
		},
		{
			name: "IL of more lines than there are clears to the bottom",
			in:   fill + "\x1b[2;1H\x1b[99L",
			want: "a",
		},
		{
			name: "DL pulls the rest up and blanks the last",
			in:   fill + "\x1b[2;1H\x1b[1M",
			want: "a\nc\nd",
		},
		{
			name: "DL of more lines than there are clears to the bottom",
			in:   fill + "\x1b[2;1H\x1b[99M",
			want: "a",
		},
		{
			// Inserting at the top of a region moves only the region's rows;
			// the row below it must not be dragged along.
			name: "IL inside a region stays inside it",
			in:   fill + "\x1b[2;3r\x1b[2;1H\x1b[1L",
			want: "a\n\nb\nd",
		},
		{
			name: "DL inside a region stays inside it",
			in:   fill + "\x1b[2;3r\x1b[2;1H\x1b[1M",
			want: "a\nc\n\nd",
		},
		{
			// Outside the region both are ignored outright, rather than
			// operating on the screen.
			name: "IL below a region does nothing",
			in:   fill + "\x1b[2;3r\x1b[4;1H\x1b[1L",
			want: "a\nb\nc\nd",
		},
		{
			name: "DL above a region does nothing",
			in:   fill + "\x1b[2;3r\x1b[1;1H\x1b[1M",
			want: "a\nb\nc\nd",
		},
		{
			name:   "IL moves the cursor to the first column",
			in:     fill + "\x1b[2;4H\x1b[1L",
			cursor: "0,1",
			want:   "a\n\nb\nc",
		},
		{
			name: "IL at the last row just blanks it",
			in:   fill + "\x1b[4;1H\x1b[1L",
			want: "a\nb\nc",
		},
	})
}

func TestConform_InsertDeleteCharacters(t *testing.T) {
	const fill = "abcdef\r\nghijkl"

	runConform(t, []conformCase{
		{
			name: "ICH pushes the rest right and drops what falls off",
			in:   fill + "\x1b[1;2H\x1b[1@",
			want: "a bcde\nghijkl",
		},
		{
			name: "ICH of several characters",
			in:   fill + "\x1b[1;2H\x1b[3@",
			want: "a   bc\nghijkl",
		},
		{
			name: "ICH of more than the row holds clears to the end",
			in:   fill + "\x1b[1;2H\x1b[99@",
			want: "a\nghijkl",
		},
		{
			name: "ICH at the last column just blanks it",
			in:   fill + "\x1b[1;6H\x1b[1@",
			want: "abcde\nghijkl",
		},
		{
			name: "DCH pulls the rest left and blanks the end",
			in:   fill + "\x1b[1;2H\x1b[1P",
			want: "acdef\nghijkl",
		},
		{
			name: "DCH of several characters",
			in:   fill + "\x1b[1;2H\x1b[3P",
			want: "aef\nghijkl",
		},
		{
			name: "DCH of more than the row holds clears to the end",
			in:   fill + "\x1b[1;2H\x1b[99P",
			want: "a\nghijkl",
		},
		{
			// Neither touches any other row, which is what distinguishes them
			// from the line operations above.
			name: "ICH leaves the row below alone",
			in:   fill + "\x1b[1;1H\x1b[3@",
			want: "   abc\nghijkl",
		},
	})
}

// TestConform_EditingNextToWideCharacters covers the case that turns an edit
// into corruption rather than a shift. Shifting a row by one column cuts every
// double-width character in half, and half a character left in a cell is drawn
// whole by every reader: a row comes out one column wider than the screen it
// came from, and lands on whatever is next to it.
func TestConform_EditingNextToWideCharacters(t *testing.T) {
	runConform(t, []conformCase{
		{
			name: "DCH that splits a wide character blanks it",
			in:   "世界ab\x1b[1;2H\x1b[1P",
			want: " 界ab",
			cells: []cellWant{
				{x: 0, y: 0, content: " ", width: 1},
			},
		},
		{
			name: "ICH that splits a wide character blanks it",
			in:   "世界ab\x1b[1;2H\x1b[1@",
			want: "   界a",
		},
		{
			name: "ECH on the second cell of a wide character blanks the whole",
			in:   "世界ab\x1b[1;2H\x1b[1X",
			want: "  界ab",
		},
		{
			name: "writing over the lead cell blanks the trailing one",
			in:   "世界\x1b[1;1HX",
			want: "X 界",
			cells: []cellWant{
				{x: 0, y: 0, content: "X", width: 1},
				{x: 1, y: 0, content: " ", width: 1},
			},
		},
		{
			name: "writing over the trailing cell blanks the lead",
			in:   "世界\x1b[1;2HX",
			want: " X界",
			cells: []cellWant{
				{x: 0, y: 0, content: " ", width: 1},
				{x: 1, y: 0, content: "X", width: 1},
			},
		},
	})
}

func TestConform_Tabs(t *testing.T) {
	runConform(t, []conformCase{
		{
			name:   "tab stops start every eight columns",
			cols:   20,
			in:     "\tA\tB",
			want:   "        A       B",
			cursor: "17,0",
		},
		{
			name:   "a tab past the last stop goes to the last column",
			cols:   20,
			in:     "\t\t\t\t\t\tX",
			want:   "                   X",
			cursor: "19,0",
		},
		{
			name:   "HTS sets a stop where the cursor is",
			cols:   20,
			in:     "\x1b[1;3H\x1bH\x1b[1;1H\tX",
			want:   "  X",
			cursor: "3,0",
		},
		{
			name:   "TBC clears the stop at the cursor",
			cols:   20,
			in:     "\x1b[1;9H\x1b[g\x1b[1;1H\tX",
			want:   "                X",
			cursor: "17,0",
		},
		{
			name:   "TBC 3 clears every stop",
			cols:   20,
			in:     "\x1b[3g\x1b[1;1H\tX",
			want:   "                   X",
			cursor: "19,0",
		},
		{
			name:   "CHT moves forward several stops",
			cols:   20,
			in:     "\x1b[2IX",
			want:   "                X",
			cursor: "17,0",
		},
		{
			name:   "CBT moves back to the previous stop",
			cols:   20,
			in:     "\x1b[1;12H\x1b[ZX",
			want:   "        X",
			cursor: "9,0",
		},
		{
			name:   "CBT at the first column stays there",
			cols:   20,
			in:     "\x1b[1;1H\x1b[ZX",
			want:   "X",
			cursor: "1,0",
		},
		{
			// A count of zero means one, the same as everywhere else.
			name:   "CHT with a zero parameter moves one stop",
			cols:   20,
			in:     "\x1b[0IX",
			want:   "        X",
			cursor: "9,0",
		},
		{
			name:   "RIS puts the default stops back",
			cols:   20,
			in:     "\x1b[3g\x1bc\tX",
			want:   "        X",
			cursor: "9,0",
		},
	})
}
