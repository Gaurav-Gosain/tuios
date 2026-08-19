package vt_test

import "testing"

// The alternate screen, the saved cursor, and the two resets. These decide what
// a full-screen program leaves behind when it exits, which is the difference
// between a shell prompt back where it was and a scrollback with a hole in it.

func TestConform_AlternateScreen(t *testing.T) {
	runConform(t, []conformCase{
		{
			name: "1049 keeps the main screen while the alternate is up",
			in:   "main\x1b[?1049hX",
			want: "X",
		},
		{
			name: "leaving 1049 brings the main screen back",
			in:   "main\x1b[?1049hX\x1b[?1049l",
			want: "main",
		},
		{
			name:   "1049 saves and restores the cursor",
			in:     "\x1b[2;3Habc\x1b[?1049h\x1b[1;1Hzz\x1b[?1049l",
			want:   "\n  abc",
			cursor: "5,1",
		},
		{
			name: "the alternate screen starts empty",
			in:   "main\x1b[?1049h",
			want: "",
		},
		{
			// A program that leaves the alternate screen twice must not end up
			// somewhere else. The second reset is a no-op, not a second switch.
			name: "leaving the alternate screen twice is harmless",
			in:   "main\x1b[?1049hX\x1b[?1049l\x1b[?1049l",
			want: "main",
		},
		{
			name: "entering the alternate screen twice does not clear it again",
			in:   "\x1b[?1049hX\x1b[?1049hY",
			want: "XY",
		},
		{
			// The alternate screen has its own scroll region, and returning
			// must not bring it back to the main one.
			name:   "the alternate screen has its own scroll region",
			in:     "\x1b[2;3r\x1b[?1049h",
			region: "0,0-6,4",
		},
		{
			name:   "leaving the alternate screen restores the main region",
			in:     "\x1b[2;3r\x1b[?1049h\x1b[1;2r\x1b[?1049l",
			region: "0,1-6,3",
		},
		{
			name: "1047 switches without saving the cursor",
			in:   "main\x1b[?1047hX\x1b[?1047l",
			want: "main",
		},
		{
			name:   "1048 saves and restores the cursor without switching",
			in:     "\x1b[2;3H\x1b[?1048h\x1b[1;1H\x1b[?1048l",
			cursor: "2,1",
			want:   "",
		},
	})
}

func TestConform_SaveAndRestoreCursor(t *testing.T) {
	runConform(t, []conformCase{
		{
			name:   "DECSC and DECRC carry the position",
			in:     "\x1b[3;4H\x1b7\x1b[1;1H\x1b8X",
			cursor: "4,2",
			want:   "\n\n   X",
		},
		{
			name:   "SCOSC and SCORC carry the position",
			in:     "\x1b[3;4H\x1b[s\x1b[1;1H\x1b[uX",
			cursor: "4,2",
			want:   "\n\n   X",
		},
		{
			// DECRC with nothing saved goes home rather than somewhere
			// arbitrary.
			name:   "DECRC with nothing saved homes the cursor",
			in:     "\x1b[3;4H\x1b8X",
			cursor: "1,0",
			want:   "X",
		},
		{
			// The saved cursor carries the pen with it, which is what lets a
			// program set a colour, save, print elsewhere, and come back.
			name: "DECSC and DECRC carry the pen",
			in:   "\x1b[31m\x1b7\x1b[0m\x1b8X",
			want: "X",
			cells: []cellWant{
				{x: 0, y: 0, content: "X", fg: indexed(1)},
			},
		},
		{
			name: "DECSC and DECRC carry the character set",
			in:   "\x1b(0\x1b7\x1b(B\x1b8q",
			want: "─",
		},
		{
			name:   "RIS clears the saved cursor",
			in:     "\x1b[3;4H\x1b7\x1bc\x1b8X",
			cursor: "1,0",
			want:   "X",
		},
	})
}

func TestConform_Resets(t *testing.T) {
	runConform(t, []conformCase{
		{
			name:   "RIS clears the screen",
			in:     "abc\x1bc",
			want:   "",
			cursor: "0,0",
		},
		{
			name:   "RIS resets the scroll region",
			in:     "\x1b[2;3r\x1bc",
			region: "0,0-6,4",
		},
		{
			name: "RIS resets the pen",
			in:   "\x1b[31;4m\x1bcX",
			want: "X",
			cells: []cellWant{
				{x: 0, y: 0, content: "X", fg: nil, underline: ptr(underlineNone)},
			},
		},
		{
			name: "RIS leaves the alternate screen",
			in:   "main\x1b[?1049hX\x1bc",
			want: "",
		},
		{
			// DECSTR is the soft reset: it puts the modes and the region back
			// but leaves what is on the screen alone, which is what makes it
			// usable in the middle of a session.
			name:   "DECSTR resets the scroll region",
			in:     "\x1b[2;3r\x1b[!p",
			region: "0,0-6,4",
		},
		{
			name: "DECSTR leaves the screen contents alone",
			in:   "abc\x1b[!p",
			want: "abc",
		},
		{
			name:   "DECSTR resets origin mode",
			in:     "\x1b[2;3r\x1b[?6h\x1b[!p\x1b[2;3r\x1b[1;1HX",
			want:   "X",
			cursor: "1,0",
		},
	})
}
