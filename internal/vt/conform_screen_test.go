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

// TestConform_DECSTRResetList walks the list DEC documents for a soft reset,
// one case per item, restricted to the state this emulator keeps. What DEC also
// lists and is missing here, DECNRCM, DECSCA, DECSASD, DECKPM, DECRLM and
// DECPCTERM, are modes the emulator has no notion of, so resetting them would
// only store a value nothing reads.
func TestConform_DECSTRResetList(t *testing.T) {
	runConform(t, []conformCase{
		{
			name: "the cursor is enabled again",
			in:   "\x1b[?25l\x1b[!p",
			want: "",
		},
		{
			name:   "origin mode goes back to absolute",
			in:     "\x1b[2;3r\x1b[?6h\x1b[!p\x1b[2;3r\x1b[1;1HX",
			want:   "X",
			cursor: "1,0",
		},
		{
			name:   "the scroll region goes back to the full page",
			in:     "\x1b[2;3r\x1b[!p",
			region: "0,0-6,4",
		},
		{
			name:   "the left and right margins go with it",
			in:     "\x1b[?69h\x1b[2;4s\x1b[!p",
			region: "0,0-6,4",
		},
		{
			name: "the character sets go back to their defaults",
			in:   "\x1b(0\x1b[!pq",
			want: "q",
		},
		{
			name: "SGR goes back to normal",
			in:   "\x1b[31;1;4m\x1b[!pX",
			want: "X",
			cells: []cellWant{
				{x: 0, y: 0, content: "X", underline: ptr(underlineNone), attrs: ptr(uint8(0))},
			},
		},
		{
			// A soft reset in the middle of an open hyperlink would otherwise
			// leave every character after it addressed to somebody's URL.
			name: "an open hyperlink is closed",
			in:   "\x1b]8;;https://example.invalid/\x07a\x1b[!pb",
			want: "ab",
			cells: []cellWant{
				{x: 1, y: 0, content: "b", link: ptr("")},
			},
		},
		{
			name:   "the saved cursor goes back to home",
			in:     "\x1b[3;4H\x1b7\x1b[!p\x1b[2;2H\x1b8X",
			want:   "X",
			cursor: "1,0",
		},
	})
}

// TestConform_DECSTRLeavesTheseAlone covers the other half of the list: what a
// soft reset must not touch. Each is something a program relies on surviving,
// which is the whole reason it reaches for a soft reset rather than RIS.
func TestConform_DECSTRLeavesTheseAlone(t *testing.T) {
	runConform(t, []conformCase{
		{
			name: "the screen is not cleared",
			in:   "abc\r\ndef\x1b[!p",
			want: "abc\ndef",
		},
		{
			// DEC has a soft reset turn autowrap off. xterm and iTerm2 both
			// decline, and esctest marks its own case for it as an intentional
			// deviation from the spec. Following the spec would stop the line
			// wrapping of every program that soft-resets and then prints.
			name:   "autowrap is left on, deviating from the spec on purpose",
			cols:   4,
			in:     "\x1b[!pabcdef",
			want:   "abcd\nef",
			cursor: "2,1",
		},
		{
			// Left alone means left alone in both directions: a guest that
			// turned autowrap off keeps it off across a soft reset.
			name:   "autowrap a guest turned off stays off",
			cols:   4,
			in:     "\x1b[?7l\x1b[!pabcdef",
			want:   "abcf",
			cursor: "3,0",
		},
		{
			name: "the tab stops are not reset",
			cols: 20,
			in:   "\x1b[3g\x1b[!p\tX",
			want: "                   X",
		},
	})
}

// TestConform_DECSTRDoesNotMoveTheCursor is stated on its own because it is the
// item most easily broken by adding to the reset: several of the modes in the
// list home the cursor when set or reset on their own, DECOM among them.
func TestConform_DECSTRDoesNotMoveTheCursor(t *testing.T) {
	for _, prefix := range []string{
		"",
		"\x1b[?6h",
		"\x1b[2;3r",
		"\x1b[?25l",
		"\x1b(0",
		"\x1b[31;1m",
		"\x1b[?69h\x1b[2;5s",
	} {
		emu, _ := newConformEmulator(t, conformCase{
			cols: 6, rows: 4,
			in: prefix + "\x1b[3;4H\x1b[!p",
		})
		if p := emu.CursorPosition(); p.X != 3 || p.Y != 2 {
			t.Errorf("after %q then a soft reset the cursor is at %d,%d, want 3,2",
				prefix, p.X, p.Y)
		}
	}
}
