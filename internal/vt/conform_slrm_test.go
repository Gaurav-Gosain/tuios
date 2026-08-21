package vt_test

// Conformance for the left and right margins: DECLRMM (private mode 69) and
// DECSLRM (CSI Pl ; Pr s).
//
// conform_margins_test.go covers the vertical pair. The horizontal pair had no
// tests at all, which matters more than it sounds: this emulator answers
// DECLRMM with a set, so a guest that asks for horizontal margins is told it
// has them. Advertising a feature and then ignoring half of it is worse than
// refusing it, because the guest lays its output out on the strength of the
// answer.
//
// Cases follow the VT420 and VT510 reference manuals for DECSLRM and DECLRMM,
// esctest tests/decslrm.py, and xterm's implementation, which is where the
// feature reached the modern world.

import (
	"testing"
)

// TestConform_SetLeftRightMargins covers what DECSLRM accepts.
func TestConform_SetLeftRightMargins(t *testing.T) {
	runConform(t, []conformCase{
		{
			name:   "DECSLRM sets the horizontal pair",
			cols:   8,
			in:     "\x1b[?69h\x1b[3;6s",
			want:   "",
			region: "2,0-6,4",
		}, {
			// Without DECLRMM the same sequence is SCOSC, save cursor, which
			// is why the mode exists: the two share a final byte.
			name:   "without DECLRMM the sequence saves the cursor instead",
			cols:   8,
			in:     "\x1b[1;4HX\x1b[3;6s\x1b[1;1H\x1b[uY",
			want:   "   XY",
			region: "0,0-8,4",
		}, {
			// A left margin at or past the right one describes no columns, so
			// the pair is refused and the previous one stands. The emulator
			// logs it as a sequence it did not act on, which is honest.
			name:      "a left margin past the right one is ignored",
			cols:      8,
			in:        "\x1b[?69h\x1b[6;3s",
			want:      "",
			region:    "0,0-8,4",
			unhandled: true,
		}, {
			name:   "omitted parameters mean the whole width",
			cols:   8,
			in:     "\x1b[?69h\x1b[3;6s\x1b[s",
			want:   "",
			region: "0,0-8,4",
		}, {
			// Resetting the mode has to give the columns back, or output goes
			// on being confined to a region the guest no longer believes in.
			name:   "resetting DECLRMM releases the margins",
			cols:   8,
			in:     "\x1b[?69h\x1b[3;6s\x1b[?69l",
			want:   "",
			region: "0,0-8,4",
		}, {
			// DECSLRM homes the cursor, as DECSTBM does for the vertical pair.
			name:   "DECSLRM homes the cursor",
			cols:   8,
			in:     "\x1b[3;4H\x1b[?69h\x1b[3;6sX",
			want:   "X",
			cursor: "1,0",
		},
	})
}

// TestConform_PrintingInsideLeftRightMargins is the half that was missing.
//
// The whole point of a right margin is that text wraps there. A terminal that
// sets the margin and then lets printing run to the screen edge has changed
// nothing a guest can use.
func TestConform_PrintingInsideLeftRightMargins(t *testing.T) {
	runConform(t, []conformCase{
		{
			name:   "text wraps at the right margin, not the screen edge",
			cols:   8,
			in:     "\x1b[?69h\x1b[3;6s\x1b[1;3HABCDEF",
			want:   "  ABCD\n  EF",
			cursor: "4,1",
		}, {
			name:   "the wrap lands on the left margin",
			cols:   8,
			in:     "\x1b[?69h\x1b[3;6s\x1b[1;3HABCDE",
			want:   "  ABCD\n  E",
			cursor: "3,1",
		}, {
			// Reaching the bottom row inside the margins scrolls only the
			// columns the margins name, leaving the ones outside alone.
			name: "wrapping off the last row scrolls only the margin columns",
			cols: 8,
			rows: 2,
			in:   "zzzzzzzz\x1b[2;1Hyyyyyyyy\x1b[?69h\x1b[3;6s\x1b[2;3HABCDEFG",
			want: "zzEFGGzz\nyyG   yy",
		}, {
			// CUF cannot leave the margins when it starts inside them.
			name:   "CUF stops at the right margin",
			cols:   8,
			in:     "\x1b[?69h\x1b[3;6s\x1b[1;3H\x1b[9CX",
			want:   "     X",
			cursor: "5,0",
		}, {
			name:   "CUB stops at the left margin",
			cols:   8,
			in:     "\x1b[?69h\x1b[3;6s\x1b[1;5H\x1b[9DX",
			want:   "  X",
			cursor: "3,0",
		}, {
			name:   "CR returns to the left margin",
			cols:   8,
			in:     "\x1b[?69h\x1b[3;6s\x1b[1;5H\rX",
			want:   "  X",
			cursor: "3,0",
		},
	})
}

// TestConform_EditingInsideLeftRightMargins checks the operations that shift
// cells sideways. These already respect the pair; the cases are here so that a
// change to the shift path cannot quietly lose it.
func TestConform_EditingInsideLeftRightMargins(t *testing.T) {
	runConform(t, []conformCase{
		{
			name: "ICH shifts only within the margins",
			cols: 8,
			in:   "12345678\x1b[?69h\x1b[3;6s\x1b[1;3H\x1b[@",
			want: "12 34578",
		}, {
			name: "DCH shifts only within the margins",
			cols: 8,
			in:   "12345678\x1b[?69h\x1b[3;6s\x1b[1;3H\x1b[P",
			want: "12456 78",
		}, {
			name: "IL moves only the margin columns down",
			cols: 8,
			in:   "12345678\x1b[2;1Habcdefgh\x1b[?69h\x1b[3;6s\x1b[1;3H\x1b[L",
			want: "12    78\nab3456gh\n  cdef",
		}, {
			name: "DL moves only the margin columns up",
			cols: 8,
			in:   "12345678\x1b[2;1Habcdefgh\x1b[?69h\x1b[3;6s\x1b[1;3H\x1b[M",
			want: "12cdef78\nab    gh",
		}, {
			// Erasing is deliberately not margin-aware. The VT510 manual
			// lists what DECSLRM affects, and it is the operations that shift
			// or wrap cells: DECIC, DECDC, IL, DL, ICH, DCH and line
			// wrapping. Erase in line is not among them, and a program that
			// clears to the end of a line means the line. xterm erases to the
			// screen edge here as well.
			name: "EL clears to the screen edge, which margins do not narrow",
			cols: 8,
			in:   "12345678\x1b[?69h\x1b[3;6s\x1b[1;3H\x1b[K",
			want: "12",
		},
	})
}

// TestConform_LeftRightMarginsWithOriginMode covers both pairs at once, which
// is where a full-screen program that uses them actually lives.
func TestConform_LeftRightMarginsWithOriginMode(t *testing.T) {
	runConform(t, []conformCase{
		{
			// Under DECOM the home position is the top-left of the region, and
			// with both pairs set that means both margins.
			name:   "origin mode homes to the corner of both margin pairs",
			cols:   8,
			rows:   5,
			in:     "\x1b[?69h\x1b[3;6s\x1b[2;4r\x1b[?6h\x1b[1;1HX",
			want:   "\n  X",
			cursor: "3,1",
		}, {
			name:   "an address past both margins clamps to the far corner",
			cols:   8,
			rows:   5,
			in:     "\x1b[?69h\x1b[3;6s\x1b[2;4r\x1b[?6h\x1b[99;99HX",
			want:   "\n\n\n     X",
			cursor: "5,3",
		}, {
			// The report a guest reads back has to use the same coordinates it
			// just addressed with.
			name:   "the cursor comes back where origin mode put it",
			cols:   8,
			rows:   5,
			in:     "\x1b[?69h\x1b[3;6s\x1b[2;4r\x1b[?6h\x1b[2;2HX",
			want:   "\n\n   X",
			cursor: "4,2",
		},
	})
}
