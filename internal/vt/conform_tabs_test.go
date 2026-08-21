package vt_test

// Conformance for tab stops: HT, HTS, TBC, CHT and CBT.
//
// Tabs are how a shell lines up completion columns and how `ls` lays out a
// directory, so a terminal that gets them wrong produces ragged output for the
// commands people run most. They also survive a resize, which is where a stop
// table can quietly go stale.
//
// Cases follow esctest tests/{ht,hts,tbc,cht,cbt}.py, the VT510 reference
// manual entries for the same, and xterm's TabZonk/TabSet in charproc.c.

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

func TestConform_TabStops(t *testing.T) {
	runConform(t, []conformCase{
		{
			// Every eight columns from the left is the default table.
			name:   "the default stops are every eight columns",
			cols:   20,
			in:     "\tA\tB",
			want:   "        A       B",
			cursor: "17,0",
		}, {
			name:   "HTS sets a stop at the cursor",
			cols:   20,
			in:     "\x1b[1;5H\x1bH\x1b[1;1H\tX",
			want:   "    X",
			cursor: "5,0",
		}, {
			// TBC with no parameter, or zero, clears the stop under the
			// cursor and leaves the rest.
			name:   "TBC clears the stop under the cursor",
			cols:   20,
			in:     "\x1b[1;9H\x1b[g\x1b[1;1H\tX",
			want:   "                X",
			cursor: "17,0",
		}, {
			// TBC 3 clears the whole table, after which a tab has nowhere to
			// go but the last column.
			name:   "TBC 3 clears every stop",
			cols:   20,
			in:     "\x1b[3g\x1b[1;1H\tX",
			want:   "                   X",
			cursor: "19,0",
		}, {
			name:   "CHT moves several stops at once",
			cols:   30,
			in:     "\x1b[3IX",
			want:   "                        X",
			cursor: "25,0",
		}, {
			// A count past the last stop saturates at the last column rather
			// than running off the row.
			name:   "CHT past the last stop stops at the last column",
			cols:   20,
			in:     "\x1b[9IX",
			want:   "                   X",
			cursor: "19,0",
		}, {
			name:   "CBT moves back several stops",
			cols:   20,
			in:     "\x1b[1;20H\x1b[2ZX",
			want:   "        X",
			cursor: "9,0",
		}, {
			name:   "CBT past the first stop stops at column one",
			cols:   20,
			in:     "\x1b[1;20H\x1b[9ZX",
			want:   "X",
			cursor: "1,0",
		}, {
			// A tab never wraps. It saturates on the row it started on, which
			// is what keeps a long completion list from scrolling.
			name:   "a tab at the last column does not wrap",
			cols:   20,
			in:     "\x1b[1;20H\tX",
			want:   "                   X",
			cursor: "19,0",
		}, {
			// Inside horizontal margins a tab stops at the right margin, the
			// same way it stops at the screen edge without them.
			name:   "a tab inside horizontal margins stops at the right margin",
			cols:   20,
			in:     "\x1b[?69h\x1b[3;10s\x1b[1;3H\t\tX",
			want:   "         X",
			cursor: "9,0",
		},
	})
}

// TestConform_TabStopsAcrossResize checks that a wider screen gains stops.
//
// A guest that widens its window and then tabs expects the new columns to be
// usable. A table built for the old width leaves everything past the old edge
// unreachable by a tab, so `ls` in a widened window packs its output into the
// left half. xterm rebuilds the default table on a resize; esctest's
// tests/cht.py assumes the stops are there.
func TestConform_TabStopsAcrossResize(t *testing.T) {
	for _, tc := range []struct {
		name             string
		from, to         int
		in               string
		wantCursorColumn int
	}{
		{
			name: "growing the screen adds stops in the new columns",
			from: 20, to: 40,
			in:               "\x1b[1;1H\x1b[4I",
			wantCursorColumn: 32,
		}, {
			name: "shrinking and growing again leaves the default table",
			from: 40, to: 20,
			in:               "\x1b[1;1H\x1b[4I",
			wantCursorColumn: 19,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			emu := vt.NewEmulator(tc.from, 4)
			emu.Resize(tc.to, 4)
			if _, err := emu.WriteString(tc.in); err != nil {
				t.Fatalf("write: %v", err)
			}
			if got := emu.CursorPosition().X; got != tc.wantCursorColumn {
				t.Errorf("after resizing %d to %d, four forward tabs left the cursor at column %d, want %d",
					tc.from, tc.to, got, tc.wantCursorColumn)
			}
		})
	}
}
