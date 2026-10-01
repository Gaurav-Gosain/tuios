package vt_test

// Conformance for a screen that gets shorter or taller.
//
// A shorter screen has to put rows somewhere. Ghostty and kitty take the blank
// rows below the cursor first and then move rows off the top into the
// scrollback, so no row that holds text is lost. tmux deletes the rows below
// the cursor whatever they hold. This emulator follows ghostty, the other
// backend tuios builds, so a pane reads the same on either.

import (
	"testing"
)

func TestConform_ResizeRows(t *testing.T) {
	runConform(t, []conformCase{
		{
			name: "a shorter screen moves the rows above the cursor into the scrollback",
			cols: 10, rows: 5,
			in:      "a\r\nb\r\nc\r\nd\r\n$ ",
			resize:  [][2]int{{10, 2}},
			want:    "d\n$",
			history: ptr("a\nb\nc"),
			cursor:  "2,1",
		},
		{
			name: "a shorter screen drops blank rows below the cursor before any text",
			cols: 10, rows: 5,
			in:      "a\r\nb\r\n$ ",
			resize:  [][2]int{{10, 3}},
			want:    "a\nb\n$",
			history: ptr(""),
			cursor:  "2,2",
		},
		{
			// A program that draws a status line on the last row and leaves
			// the cursor at the top used to lose the status line.
			name: "a row of text below the cursor is kept",
			cols: 10, rows: 5,
			in:      "a\r\nb\x1b[5;1Hstatus\x1b[1;1H",
			resize:  [][2]int{{10, 2}},
			want:    "\nstatus",
			history: ptr("a\nb\n"),
			cursor:  "0,0",
		},
		{
			// The rows leaving the top go to the scrollback even though the
			// guest had set a scroll region. Scrolling within the region
			// dropped them.
			name: "a scroll region does not stop rows reaching the scrollback",
			cols: 10, rows: 5,
			in:      "1\r\n2\r\n3\r\n4\r\n5\x1b[2;4r\x1b[5;2H",
			resize:  [][2]int{{10, 3}},
			want:    "3\n4\n5",
			history: ptr("1\n2"),
			cursor:  "1,2",
			region:  "0,0-10,3",
		},
		{
			// The main screen under an alternate screen keeps its rows too.
			// Only the active screen's cursor was kept in view, so the shell
			// prompt was cut off the bottom of the main screen and the cursor
			// came back on the row above it. Growing back takes main1 back
			// from the history, because the main cursor is on the last row.
			name: "the main screen under an alternate screen keeps its prompt",
			cols: 10, rows: 3,
			in:      "main1\r\nmain2\r\n$ \x1b[?1049h\x1b[Hfull",
			resize:  [][2]int{{10, 2}, {10, 3}},
			then:    "\x1b[?1049l",
			want:    "main1\nmain2\n$",
			history: ptr(""),
			cursor:  "2,2",
		},
		{
			name: "a saved cursor moves up with the text it was saved on",
			cols: 10, rows: 4,
			in:      "a\r\nb\r\nc\r\nd\x1b[3;2H\x1b7\x1b[4;2H",
			resize:  [][2]int{{10, 2}},
			then:    "\x1b8X",
			want:    "cX\nd",
			history: ptr("a\nb"),
			cursor:  "2,0",
		},
		{
			name: "the alternate screen has no scrollback and drops the rows above its cursor",
			cols: 10, rows: 4,
			in:      "\x1b[?1049h\x1b[Ha\r\nb\r\nc\r\nd",
			resize:  [][2]int{{10, 2}},
			want:    "c\nd",
			history: ptr(""),
			cursor:  "1,1",
		},
	})
}
