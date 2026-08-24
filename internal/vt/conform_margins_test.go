package vt_test

import "testing"

// Scroll regions and origin mode. This is the family with the worst record in
// this codebase: a region that outran the screen was a daemon-wide panic, and
// the interaction between DECOM and cursor addressing is the kind of thing no
// ordinary session exercises and every full-screen program depends on.
//
// The cases named after esctest follow that suite's expectations for DECSTBM,
// which is the same behaviour xterm has.

func TestConform_DECSTBM(t *testing.T) {
	runConform(t, []conformCase{
		{
			name:   "default region is the whole screen",
			in:     "",
			want:   "",
			region: "0,0-6,4",
		},
		{
			name:   "sets the named rows",
			in:     "\x1b[2;3r",
			want:   "",
			region: "0,1-6,3",
			cursor: "0,0",
		},
		{
			// esctest: a region whose top equals its bottom is rejected and
			// leaves the margins as they were.
			name:      "top equal to bottom is rejected",
			in:        "\x1b[3;3r",
			want:      "",
			region:    "0,0-6,4",
			unhandled: true,
		},
		{
			name:      "top equal to bottom does not disturb an existing region",
			in:        "\x1b[2;3r\x1b[3;3r",
			want:      "",
			region:    "0,1-6,3",
			unhandled: true,
		},
		{
			name:      "top greater than bottom is rejected",
			in:        "\x1b[3;2r",
			want:      "",
			region:    "0,0-6,4",
			unhandled: true,
		},
		{
			// esctest: a bottom past the last row clamps to the screen. This is
			// the shape that panicked here, reached by a guest that sized its
			// region before it learned it had been resized smaller.
			name:   "bottom past the screen clamps",
			rows:   4,
			in:     "\x1b[1;35r",
			want:   "",
			region: "0,0-6,4",
		},
		{
			name:      "top past the screen is rejected",
			in:        "\x1b[9;12r",
			want:      "",
			region:    "0,0-6,4",
			unhandled: true,
		},
		{
			name:   "no parameters resets to the whole screen",
			in:     "\x1b[2;3r\x1b[r",
			want:   "",
			region: "0,0-6,4",
		},
		{
			name:   "zero parameters mean the defaults",
			in:     "\x1b[2;3r\x1b[0;0r",
			want:   "",
			region: "0,0-6,4",
		},
		{
			name:   "setting a region homes the cursor",
			in:     "\x1b[3;3H\x1b[2;3r",
			want:   "",
			cursor: "0,0",
		},
		{
			name:   "a region of exactly two rows is legal",
			in:     "\x1b[2;3r",
			want:   "",
			region: "0,1-6,3",
		},
	})
}

func TestConform_ScrollInRegion(t *testing.T) {
	// Four rows of one letter each, so which row moved where is unambiguous.
	const fill = "a\r\nb\r\nc\r\nd"

	runConform(t, []conformCase{
		{
			name: "SU inside a region moves only that region",
			in:   fill + "\x1b[2;3r\x1b[1S",
			want: "a\nc\n\nd",
		},
		{
			name: "SD inside a region moves only that region",
			in:   fill + "\x1b[2;3r\x1b[1T",
			want: "a\n\nb\nd",
		},
		{
			name: "SU of the whole region empties it",
			in:   fill + "\x1b[2;3r\x1b[2S",
			want: "a\n\n\nd",
		},
		{
			name: "SU beyond the region empties it and no more",
			in:   fill + "\x1b[2;3r\x1b[99S",
			want: "a\n\n\nd",
		},
		{
			name: "SD beyond the region empties it and no more",
			in:   fill + "\x1b[2;3r\x1b[99T",
			want: "a\n\n\nd",
		},
		{
			name:   "RI at the top of a region scrolls the region down",
			in:     fill + "\x1b[2;3r\x1b[2;1H\x1bM",
			want:   "a\n\nb\nd",
			cursor: "0,1",
		},
		{
			name:   "IND at the bottom of a region scrolls the region up",
			in:     fill + "\x1b[2;3r\x1b[3;1H\x1bD",
			want:   "a\nc\n\nd",
			cursor: "0,2",
		},
		{
			name:   "NEL at the bottom of a region scrolls and returns",
			in:     fill + "\x1b[2;3r\x1b[3;3H\x1bE",
			want:   "a\nc\n\nd",
			cursor: "0,2",
		},
		{
			name: "a linefeed at the bottom of a region scrolls the region",
			in:   fill + "\x1b[2;3r\x1b[3;1H\n",
			want: "a\nc\n\nd",
		},
		{
			name:   "RI above a region does not scroll it",
			in:     fill + "\x1b[2;3r\x1b[1;1H\x1bM",
			want:   "a\nb\nc\nd",
			cursor: "0,0",
		},
		{
			name: "SU with no region scrolls the whole screen",
			in:   fill + "\x1b[1S",
			want: "b\nc\nd",
		},
	})
}

func TestConform_OriginMode(t *testing.T) {
	runConform(t, []conformCase{
		{
			name:   "CUP without origin mode addresses the screen",
			in:     "\x1b[2;3r\x1b[1;1HX",
			want:   "X",
			cursor: "1,0",
		},
		{
			name:   "CUP with origin mode addresses the region",
			in:     "\x1b[2;3r\x1b[?6h\x1b[1;1HX",
			want:   "\nX",
			cursor: "1,1",
		},
		{
			name:   "origin mode clamps to the bottom of the region",
			in:     "\x1b[2;3r\x1b[?6h\x1b[9;1HX",
			want:   "\n\nX",
			cursor: "1,2",
		},
		{
			name:   "setting origin mode homes the cursor into the region",
			in:     "\x1b[2;3r\x1b[4;4H\x1b[?6hX",
			want:   "\nX",
			cursor: "1,1",
		},
		{
			name:   "leaving origin mode homes the cursor to the screen",
			in:     "\x1b[2;3r\x1b[?6h\x1b[3;3H\x1b[?6lX",
			want:   "X",
			cursor: "1,0",
		},
		{
			// Cursor motion stays inside the region while origin mode is on,
			// so a program cannot walk out of the area it asked for.
			name:   "CUU stops at the top of the region",
			in:     "\x1b[2;3r\x1b[?6h\x1b[9A X",
			want:   "\n X",
			cursor: "2,1",
		},
		{
			name:   "CUD stops at the bottom of the region",
			in:     "\x1b[2;3r\x1b[?6h\x1b[9BX",
			want:   "\n\nX",
			cursor: "1,2",
		},
	})
}

func TestConform_NextLine(t *testing.T) {
	const fill = "a\r\nb\r\nc\r\nd"

	runConform(t, []conformCase{
		{
			name:   "NEL moves down and to column one",
			in:     "\x1b[1;3HX\x1bEY",
			want:   "  X\nY",
			cursor: "1,1",
		},
		{
			name:   "NEL at the bottom of a region scrolls the region",
			in:     fill + "\x1b[2;3r\x1b[3;3H\x1bE",
			want:   "a\nc\n\nd",
			cursor: "0,2",
		},
		{
			name:   "NEL at the bottom of the screen scrolls the screen",
			in:     fill + "\x1b[4;3H\x1bE",
			want:   "b\nc\nd",
			cursor: "0,3",
		},
	})
}

// TestConform_DECALN checks the alignment pattern vttest and esctest open with.
// A terminal that ignores it reports a blank screen for every check that
// follows, which makes the whole suite look like it passed.
func TestConform_DECALN(t *testing.T) {
	runConform(t, []conformCase{
		{
			name:   "fills the screen and homes the cursor",
			cols:   4,
			rows:   3,
			in:     "\x1b[2;2H\x1b#8",
			want:   "EEEE\nEEEE\nEEEE",
			cursor: "0,0",
		},
		{
			// The pattern covers the screen, not the region: it is an
			// alignment check for the grid itself.
			name: "ignores the scroll region",
			cols: 4,
			rows: 3,
			in:   "\x1b[2;3r\x1b#8",
			want: "EEEE\nEEEE\nEEEE",
		},
		{
			name: "overwrites what was there",
			cols: 4,
			rows: 2,
			in:   "abcd\x1b#8",
			want: "EEEE\nEEEE",
		},
	})
}

// TestConform_MarginsSurviveAResize is the regression the panic came from,
// stated as a screen rather than as an absence of a crash: after the screen
// shrinks under a region that was sized for the old one, scrolling still moves
// the rows that exist.
func TestConform_MarginsSurviveAResize(t *testing.T) {
	tc := conformCase{
		cols: 6,
		rows: 8,
		in:   "a\r\nb\r\nc\r\nd",
	}
	emu, _ := newConformEmulator(t, tc)
	emu.Resize(6, 4)
	if _, err := emu.WriteString("\x1b[1;8r\x1b[1S"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got, want := dumpScreen(emu), normalizeWant("b\nc\nd"); got != want {
		t.Errorf("screen mismatch\n--- got ---\n%s\n--- want ---\n%s", boxed(got), boxed(want))
	}
	if r := emu.ScrollRegion(); r.Max.Y > emu.Height() {
		t.Errorf("region %v escapes a %d-row screen", r, emu.Height())
	}
}

// TestConform_ResizeToTheSameSizeKeepsTheMargins pins that a resize to the size
// the terminal already is does nothing at all.
//
// A real resize resets DECSTBM, on both backends and in every terminal that
// implements it, and that is correct. A resize that changes no dimension is not
// a real resize, and it used to reset the margins anyway - which matters here
// because tuios announces a pane's size from every client attached to it, so a
// second client attaching, or any client re-announcing after a layout that
// moved nothing, arrived as a resize to the size already set. A full-screen
// program in that pane lost its scroll region to a message about nothing.
//
// NEGATIVE CONTROL: fails on both backends without the guard at the top of
// Resize - the region comes back as the whole screen.
func TestConform_ResizeToTheSameSizeKeepsTheMargins(t *testing.T) {
	emu, _ := newConformEmulator(t, conformCase{cols: 20, rows: 10})
	if _, err := emu.Write([]byte("\x1b[3;7r")); err != nil {
		t.Fatalf("write: %v", err)
	}
	before := emu.ScrollRegion()
	if before.Min.Y == 0 && before.Max.Y == emu.Height() {
		t.Fatalf("the scroll region never took: %v on a %d-row screen", before, emu.Height())
	}

	emu.Resize(20, 10)

	if after := emu.ScrollRegion(); after != before {
		t.Errorf("a resize to the size the terminal already was moved the scroll region from %v to %v",
			before, after)
	}

	// A real resize still resets them, which is the behaviour every terminal
	// has and which this must not have quietly changed.
	emu.Resize(20, 12)
	if after := emu.ScrollRegion(); after == before {
		t.Errorf("a resize that did change the height left the scroll region at %v; a real resize resets it", after)
	}
}
