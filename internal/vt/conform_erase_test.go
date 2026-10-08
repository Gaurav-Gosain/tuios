package vt_test

// Conformance for DECALN and the selective-erase family.
//
// conform_edit_test.go covers ED and EL. This covers the private forms nothing
// asserted: DECSCA character protection, DECSED and DECSEL, and the alignment
// pattern vttest opens with.
//
// Cases follow the VT510 reference manual entries for DECALN, DECSCA, DECSED
// and DECSEL, esctest tests/{decsca,decsed,decsel,decaln}.py, and xterm's
// CASE_DECALN in charproc.c.

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
	"github.com/charmbracelet/x/ansi"
)

func TestConform_ScreenAlignmentPattern(t *testing.T) {
	runConform(t, []conformCase{
		{
			name:   "DECALN fills the whole screen with E and homes the cursor",
			cols:   4,
			rows:   3,
			in:     "\x1b[2;2Hx\x1b#8",
			want:   "EEEE\nEEEE\nEEEE",
			cursor: "0,0",
		}, {
			// The pattern covers the screen, so the margins that were confining
			// output to part of it have to go with it. xterm calls resetmargins
			// on the way through, and vttest's alignment check is followed by
			// margin tests that assume a clean slate.
			//
			// ghostty resets origin mode here as well. This does not, because
			// with the region back to the whole screen origin mode addresses
			// the same cells either way, and clearing a mode the guest set is
			// a larger side effect than the manual asks for.
			name:   "DECALN resets both pairs of margins",
			cols:   4,
			rows:   4,
			in:     "\x1b[2;3r\x1b[?69h\x1b[2;3s\x1b#8",
			want:   "EEEE\nEEEE\nEEEE\nEEEE",
			region: "0,0-4,4",
		}, {
			// It uses the default pen, which is what makes it an alignment
			// check: a screen of E in the guest's current colours would not
			// show a misaligned attribute.
			name: "DECALN ignores the current pen",
			cols: 3,
			rows: 1,
			in:   "\x1b[31;46m\x1b#8",
			want: "EEE",
			cells: []cellWant{
				{x: 0, y: 0, content: "E", fg: nil, bg: nil},
			},
		},
	})
}

// TestConform_EraseSavedLines covers ED 3.
//
// CSI 3 J is xterm's "erase saved lines". It drops the scrollback and leaves
// the visible screen exactly where it was. xterm, tmux, kitty and ghostty all
// agree on that, and the reason it matters is that the two are separate
// requests: `clear` sends CUP, ED 2 and ED 3 together, so a terminal that
// conflates them looks right there and destroys the screen for anything that
// sends ED 3 on its own to drop history.
func TestConform_EraseSavedLines(t *testing.T) {
	runConform(t, []conformCase{
		{
			name:   "ED 3 drops the scrollback and leaves the screen",
			cols:   6,
			rows:   3,
			in:     "a\r\nb\r\nc\r\nd\r\ne\x1b[3J",
			want:   "c\nd\ne",
			cursor: "1,2",
		}, {
			name: "ED 2 clears the screen and keeps the scrollback",
			cols: 6,
			rows: 3,
			in:   "a\r\nb\r\nc\r\nd\r\ne\x1b[2J",
			want: "",
		}, {
			// What `clear` actually sends. Both halves have to happen.
			name: "the pair a clear sends empties both",
			cols: 6,
			rows: 3,
			in:   "a\r\nb\r\nc\r\nd\r\ne\x1b[H\x1b[2J\x1b[3J",
			want: "",
		},
	})

	// The scrollback half is not visible in a screen dump, so it is checked
	// directly.
	for _, tc := range []struct {
		name string
		in   string
		want int
	}{
		{"ED 3 empties the scrollback", "a\r\nb\r\nc\r\nd\r\ne\x1b[3J", 0},
		{"ED 2 leaves the scrollback alone", "a\r\nb\r\nc\r\nd\r\ne\x1b[2J", 2},
		{"ED 0 leaves the scrollback alone", "a\r\nb\r\nc\r\nd\r\ne\x1b[0J", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			emu := vt.NewEmulator(6, 3)
			if _, err := emu.WriteString(tc.in); err != nil {
				t.Fatalf("write: %v", err)
			}
			if got := emu.ScrollbackLen(); got != tc.want {
				t.Errorf("scrollback holds %d lines, want %d", got, tc.want)
			}
		})
	}
}

// TestConform_SelectiveErase covers DECSCA, DECSED and DECSEL.
//
// DECSCA 1 protects what is printed next, and DECSCA 0 or 2 stops. A selective
// erase (DECSED, DECSEL) erases only the cells that are not protected. The
// plain erases (ED, EL) erase protected cells too: DEC protection guards a
// cell against the selective forms only. That is what xterm and ghostty do.
// tmux implements none of it.
//
// DECSED and DECSEL used to be unhandled, and a guest that sent CSI ? 2 J
// expecting the screen cleared got nothing at all. Then they erased exactly
// as ED and EL do, because DECSCA was not implemented, so a protected cell
// was erased too.
func TestConform_SelectiveErase(t *testing.T) {
	runConform(t, []conformCase{
		{
			name: "DECSCA alone changes nothing visible",
			in:   "\x1b[1\"qAB",
			want: "AB",
		}, {
			name: "DECSED keeps the protected cells",
			in:   "\x1b[1\"qAB\x1b[0\"qCD\x1b[1;1H\x1b[?2J",
			want: "AB",
		}, {
			name: "DECSEL keeps the protected cells",
			in:   "ab\x1b[1\"qCD\x1b[2\"qef\x1b[1;1H\x1b[?2K",
			want: "  CD",
		}, {
			name: "ED erases the protected cells too",
			in:   "\x1b[1\"qAB\x1b[0\"q\x1b[1;1H\x1b[2J",
			want: "",
		}, {
			name: "an unknown DECSCA value leaves protection as it was",
			in:   "\x1b[1\"qA\x1b[7\"qB\x1b[0\"q\x1b[1;1H\x1b[?2K",
			want: "AB",
		}, {
			name: "a soft reset stops protecting",
			in:   "\x1b[1\"q\x1b[!pAB\x1b[1;1H\x1b[?2K",
			want: "",
		}, {
			name: "DECRC brings the protection DECSC saved",
			in:   "\x1b[1\"q\x1b7\x1b[0\"q\x1b8AB\x1b[1;1H\x1b[?2K",
			want: "AB",
		}, {
			name: "overwriting a protected cell unprotected it",
			in:   "\x1b[1\"qAB\x1b[0\"q\x1b[1;1Hx\x1b[1;1H\x1b[?2K",
			want: " B",
		}, {
			name: "protection moves with a delete",
			in:   "ab\x1b[1\"qCD\x1b[0\"q\x1b[1;1H\x1b[2P\x1b[?2K",
			want: "CD",
		}, {
			name: "protection moves with an insert",
			in:   "\x1b[1\"qCD\x1b[0\"q\x1b[1;1H\x1b[2@\x1b[?2K",
			want: "  CD",
		}, {
			name: "protection scrolls with its row",
			in:   "x\r\n\x1b[1\"qP\x1b[0\"q\x1b[S\x1b[1;1H\x1b[?2J",
			want: "P",
		}, {
			// The protection moves with its row, and a row that moves in
			// keeps none of what was there before. Each case below fails when
			// the protection stays where the row was.
			name: "protection moves down with an inserted line",
			in:   "ab\r\n\x1b[1\"qP\x1b[0\"q\x1b[1;1H\x1b[L\x1b[?2J",
			want: "\n\nP",
		}, {
			name: "protection moves up with a deleted line",
			in:   "ab\r\n\x1b[1\"qP\x1b[0\"q\x1b[1;1H\x1b[M\x1b[?2J",
			want: "P",
		}, {
			name: "protection moves down with a reverse index in a region",
			in:   "\x1b[2;4r\x1b[2;1H\x1b[1\"qP\x1b[0\"q\x1b[2;1H\x1bM\x1b[?2J",
			want: "\n\nP",
		}, {
			name: "protection moves down with an inserted line inside side margins",
			in:   "\x1b[?69h\x1b[2;5sab\x1b[1\"qC\x1b[0\"qd\x1b[1;2H\x1b[L\x1b[?69l\x1b[?2J",
			want: "\n  C",
		}, {
			name: "protection moves up with a deleted line inside side margins",
			in:   "a\r\nab\x1b[1\"qC\x1b[0\"qd\x1b[?69h\x1b[2;5s\x1b[1;2H\x1b[M\x1b[?69l\x1b[?2J",
			want: "  C",
		}, {
			// An erased cell is not protected any more. The background a
			// selective erase paints shows it: a cell still protected keeps
			// the default one.
			name:  "an erase in line unprotects the cells it erases",
			in:    "\x1b[1\"qA\x1b[0\"q\x1b[1;1H\x1b[K\x1b[41m\x1b[?J",
			cells: []cellWant{{x: 0, y: 0, bg: ansi.Red}},
		}, {
			name:  "a line scrolled in at the bottom is not protected",
			in:    "\x1b[1\"qP\x1b[0\"q\x1b[S\x1b[41m\x1b[1;1H\x1b[?J",
			cells: []cellWant{{x: 0, y: 3, bg: ansi.Red}},
		}, {
			// These two cases used to want "ABC" and an unhandled sequence:
			// the selective erase erased nothing.
			name: "DECSED erases like ED when nothing is protected",
			in:   "ABC\x1b[1;1H\x1b[?2J",
			want: "",
		}, {
			name: "DECSEL erases like EL when nothing is protected",
			in:   "ABC\x1b[1;2H\x1b[?0K",
			want: "A",
		}, {
			name: "DECSED below the cursor",
			in:   "ABC\r\nDEF\r\nGHI\x1b[2;2H\x1b[?J",
			want: "ABC\nD",
		}, {
			name: "DECSED above the cursor",
			in:   "ABC\r\nDEF\r\nGHI\x1b[2;2H\x1b[?1J",
			want: "\n  F\nGHI",
		}, {
			name: "DECSEL to the left of the cursor",
			in:   "ABCD\x1b[1;2H\x1b[?1K",
			want: "  CD",
		}, {
			name: "DECSEL of the whole line",
			in:   "ABC\r\nDEF\x1b[1;2H\x1b[?2K",
			want: "\nDEF",
		}, {
			// The unprotected forms do work, and DA1 claims selective erase
			// (parameter 6), so a guest is entitled to try. It gets the plain
			// behaviour, which for an unprotected screen is the same answer.
			name: "the unprotected forms erase as normal",
			in:   "ABC\x1b[1;2H\x1b[0K",
			want: "A",
		},
	})
}
