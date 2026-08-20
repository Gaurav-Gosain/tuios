package vt_test

// Conformance for the mode matrix: the flags that change what every subsequent
// character does.
//
// A mode is worse than a sequence when it is wrong. A mishandled CUP misplaces
// one character; a mishandled DECAWM or IRM misplaces everything after it, and
// the guest has no way to notice because it is not looking at the screen.
//
// Cases follow esctest (tests/esctest/esctest/tests/{decawm,irm,decset}.py),
// the VT510 reference manual's mode tables, and the pending-wrap model xterm
// and ghostty share.

import (
	"testing"
)

// TestConform_InsertReplaceMode covers IRM, ANSI mode 4.
//
// terminfo spells smir as CSI 4 h and rmir as CSI 4 l, and ncurses reaches for
// insert mode whenever a terminal has `mir` and no cheaper insert-character
// path, so this runs under ordinary programs rather than only under a
// conformance suite.
func TestConform_InsertReplaceMode(t *testing.T) {
	runConform(t, []conformCase{
		{
			name:   "IRM shifts the rest of the line right",
			in:     "abcdef\x1b[1;1H\x1b[4hZ",
			want:   "Zabcde",
			cursor: "1,0",
		}, {
			name: "characters pushed past the right margin are lost",
			in:   "abcdef\x1b[1;1H\x1b[4hXY",
			want: "XYabcd",
		}, {
			name:   "resetting IRM goes back to overwriting",
			in:     "abcdef\x1b[1;1H\x1b[4h\x1b[4lZ",
			want:   "Zbcdef",
			cursor: "1,0",
		}, {
			// A double-width cluster opens two columns, not one.
			name:   "IRM opens as many columns as the character is wide",
			in:     "abcdef\x1b[1;1H\x1b[4h世",
			want:   "世abcd",
			cursor: "2,0",
		}, {
			name: "IRM inserts in the middle of a line",
			in:   "abcdef\x1b[1;4H\x1b[4hZ",
			want: "abcZde",
		}, {
			// Insert mode is about the row the cursor is on. The row below is
			// not a continuation of it.
			name: "IRM does not disturb the row below",
			in:   "abcdef\x1b[2;1Hghijkl\x1b[1;1H\x1b[4hZ",
			want: "Zabcde\nghijkl",
		}, {
			// Wrapping is DECAWM's business; insert mode does not change when
			// a line ends.
			name:   "IRM still wraps at the margin",
			in:     "\x1b[4habcdefg",
			want:   "gabcde\nf",
			cursor: "1,1",
		},
	})
}

// TestConform_LineFeedNewLineMode covers LNM, ANSI mode 20.
//
// With LNM set a linefeed also returns the carriage, which is what a program
// writing bare \n into a raw-mode terminal is relying on when it sets the mode.
func TestConform_LineFeedNewLineMode(t *testing.T) {
	runConform(t, []conformCase{
		{
			name:   "without LNM a linefeed keeps the column",
			in:     "ab\ncd",
			want:   "ab\n  cd",
			cursor: "4,1",
		}, {
			name:   "with LNM a linefeed returns the carriage",
			in:     "\x1b[20hab\ncd",
			want:   "ab\ncd",
			cursor: "2,1",
		}, {
			name: "resetting LNM goes back to a bare linefeed",
			in:   "\x1b[20h\x1b[20lab\ncd",
			want: "ab\n  cd",
		},
	})
}

// TestConform_PendingWrap pins what does and does not clear the pending-wrap
// flag.
//
// After a character lands in the last column the cursor stays on it and a flag
// records that the next character wraps first. That is xterm's model and
// ghostty's; tmux instead parks the cursor one column past the end, which is
// why the differential suite lists "wrap then backspace" as a case where tmux
// is the one that diverges. The rule is that anything which moves the cursor
// clears the flag and anything which does not, does not.
func TestConform_PendingWrap(t *testing.T) {
	runConform(t, []conformCase{
		{
			name:   "a line that ends exactly at the margin does not wrap on its own",
			in:     "abcdef",
			want:   "abcdef",
			cursor: "5,0",
		}, {
			name:   "the next character is what wraps",
			in:     "abcdefg",
			want:   "abcdef\ng",
			cursor: "1,1",
		}, {
			// SGR is not cursor motion. A program that colours the next word
			// before printing it must not lose the wrap.
			name:   "SGR does not clear the pending wrap",
			in:     "abcdef\x1b[0mg",
			want:   "abcdef\ng",
			cursor: "1,1",
		}, {
			name:   "a hyperlink does not clear the pending wrap",
			in:     "abcdef\x1b]8;;http://a\x1b\\g",
			want:   "abcdef\ng",
			cursor: "1,1",
		}, {
			// CUF cannot move, because the cursor is already on the last
			// column, but it still clears the flag.
			name:   "CUF clears the pending wrap without moving",
			in:     "abcdef\x1b[Cg",
			want:   "abcdeg",
			cursor: "5,0",
		}, {
			name:   "CUB clears it and moves back one",
			in:     "abcdef\x1b[Dg",
			want:   "abcdgf",
			cursor: "5,0",
		}, {
			// This is the case tmux answers differently. xterm's CursorBack
			// clears the flag first and then moves, so the cursor lands one
			// column earlier than the last.
			name:   "backspace clears it and moves back one",
			in:     "abcdef\bg",
			want:   "abcdgf",
			cursor: "5,0",
		}, {
			name:   "CR clears it",
			in:     "abcdef\rg",
			want:   "gbcdef",
			cursor: "1,0",
		}, {
			name:   "CUP clears it",
			in:     "abcdef\x1b[1;3Hg",
			want:   "abgdef",
			cursor: "3,0",
		}, {
			// A wide cluster that ends flush against the margin has to arm the
			// flag too, or the next character lands on the cluster's own
			// second cell.
			name:   "a wide cluster ending at the margin arms it",
			in:     "世世世X",
			want:   "世世世\nX",
			cursor: "1,1",
		},
	})
}

// TestConform_AutoWrapAndOriginMode covers DECAWM and DECOM together, which
// nothing tested before. Wrapping inside a scrolling region is where a full-
// screen program spends its whole life.
func TestConform_AutoWrapAndOriginMode(t *testing.T) {
	runConform(t, []conformCase{
		{
			// The wrap lands on the next line of the region.
			name:   "wrapping inside a region stays inside it",
			cols:   4,
			rows:   5,
			in:     "\x1b[2;3r\x1b[?6h\x1b[1;1Habcdefgh",
			want:   "\nabcd\nefgh",
			cursor: "3,2",
		}, {
			// Reaching the bottom of the region scrolls the region, not the
			// screen: the rows outside it do not move.
			name:   "a wrap past the region bottom scrolls the region",
			cols:   4,
			rows:   5,
			in:     "\x1b[5;1Hzzzz\x1b[2;3r\x1b[?6h\x1b[1;1Habcdefghi",
			want:   "\nefgh\ni\n\nzzzz",
			cursor: "1,2",
		}, {
			name:   "DECAWM off inside a region overwrites the last column",
			cols:   4,
			rows:   5,
			in:     "\x1b[?7l\x1b[2;3r\x1b[?6h\x1b[1;1Habcdefgh",
			want:   "\nabch",
			cursor: "3,1",
		}, {
			// Turning autowrap off does not retroactively cancel a wrap that
			// is already pending.
			name: "DECAWM off after a line fills keeps the cursor on the last column",
			in:   "abcdef\x1b[?7lg",
			want: "abcdeg",
		}, {
			name: "DECAWM back on re-arms wrapping for the next full line",
			in:   "\x1b[?7labcdefgh\x1b[?7hij",
			want: "abcdei\nj",
		},
	})
}

// TestConform_SaveRestoreCarriesTheWrapFlag checks the one item on the DECSC
// list nothing asserted.
//
// xterm's documentation for DECSC lists "state of wrap flag" among what is
// saved, alongside the position, the rendition, the character sets and origin
// mode. A program that saves at the end of a full line, moves away, restores
// and prints expects the wrap it left pending.
func TestConform_SaveRestoreCarriesTheWrapFlag(t *testing.T) {
	runConform(t, []conformCase{
		{
			name:   "DECRC restores a pending wrap",
			in:     "abcdef\x1b7\x1b[1;1H\x1b8g",
			want:   "abcdef\ng",
			cursor: "1,1",
		}, {
			name:   "DECRC restores the absence of a pending wrap",
			in:     "abcde\x1b7f\x1b8g",
			want:   "abcdeg",
			cursor: "5,0",
		}, {
			// The saved cursor is per-screen state; the flag has to travel
			// with the rest of it.
			name:   "DECRC restores origin mode along with the position",
			cols:   6,
			rows:   6,
			in:     "\x1b[2;5r\x1b[?6h\x1b7\x1b[?6l\x1b8\x1b[1;1HX",
			want:   "\nX",
			cursor: "1,1",
		},
	})
}

// TestConform_ModesThisEmulatorIgnores pins the modes that are recorded and
// acted on by nothing, so that a half-implementation cannot appear without a
// test noticing.
//
// Each one is a deliberate call, not an oversight:
//
//   - DECCOLM (?3) changes the column count on a real terminal, clearing the
//     screen and resetting the margins on the way. A pane cannot change its own
//     width, so honouring the clear without the resize would destroy a screen
//     for no reason a guest could predict. tmux, kitty and VTE all ignore it,
//     and xterm only obeys it when a resource is set; this follows them.
//   - DECSCNM (?5) inverts the whole screen. Nothing here reads it.
//   - Reverse wrap (?45) makes a backspace at column one wrap to the end of the
//     line above. xterm and ghostty implement it, tmux does not. A guest has to
//     ask for it, so leaving it off is safe; leaving it half on would not be.
func TestConform_ModesThisEmulatorIgnores(t *testing.T) {
	runConform(t, []conformCase{
		{
			name:   "DECCOLM does not clear the screen or reset the margins",
			in:     "abc\x1b[2;3r\x1b[?3h",
			want:   "abc",
			region: "0,1-6,3",
		}, {
			name: "DECSCNM changes nothing on the grid",
			in:   "\x1b[?5hab",
			want: "ab",
		}, {
			name:   "reverse wrap does not carry a backspace to the line above",
			in:     "abcdef\x1b[2;1H\bX",
			want:   "abcdef\nX",
			cursor: "1,1",
		},
	})
}
