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

// TestConform_SelectiveErase covers DECSCA, DECSED and DECSEL.
//
// Nothing here implements character protection, so all three are logged as
// sequences the emulator did not act on. That is a defensible place to be:
// tmux does not implement them either, and this emulator sits where tmux sits.
// It is not a free choice, though. A guest that sends CSI ? 2 J expecting the
// screen cleared gets nothing at all, and the two cases below record which half
// of the divergence each sequence falls on so that a future implementation has
// something to aim at.
func TestConform_SelectiveErase(t *testing.T) {
	runConform(t, []conformCase{
		{
			name:      "DECSCA is not implemented",
			in:        "\x1b[1\"qAB",
			want:      "AB",
			unhandled: true,
		}, {
			name:      "DECSED is not implemented, so a selective erase erases nothing",
			in:        "ABC\x1b[1;1H\x1b[?2J",
			want:      "ABC",
			unhandled: true,
		}, {
			name:      "DECSEL is not implemented, so a selective erase erases nothing",
			in:        "ABC\x1b[1;2H\x1b[?0K",
			want:      "ABC",
			unhandled: true,
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
