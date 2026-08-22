package vt

import "testing"

// The DECSCUSR parameters and the shape each names. 0 and 1 are the same
// blinking block; 2 is the shape a pane has before anything asks.
var decscusrCases = []struct {
	seq    string
	style  CursorStyle
	steady bool
}{
	{"\x1b[0 q", CursorBlock, false},
	{"\x1b[1 q", CursorBlock, false},
	{"\x1b[2 q", CursorBlock, true},
	{"\x1b[3 q", CursorUnderline, false},
	{"\x1b[4 q", CursorUnderline, true},
	{"\x1b[5 q", CursorBar, false},
	{"\x1b[6 q", CursorBar, true},
	{"\x1b[ q", CursorBlock, false},
}

// TestCursorStyleIsReadableFromTheEmulator runs against whichever backend is
// compiled, so the two cannot drift on what a guest asked for. The renderer
// reads the shape here every frame rather than being told about changes, which
// is what makes a restored or re-focused pane come back with the right cursor,
// so a backend that parses DECSCUSR but does not report it would be a silent
// block cursor everywhere.
func TestCursorStyleIsReadableFromTheEmulator(t *testing.T) {
	for _, tc := range decscusrCases {
		term := New(10, 5)
		if _, err := term.Write([]byte(tc.seq)); err != nil {
			t.Fatalf("write %q: %v", tc.seq, err)
		}
		style, steady := term.CursorStyle()
		if style != tc.style || steady != tc.steady {
			t.Errorf("%q: got style %d steady %v, want style %d steady %v",
				tc.seq, style, steady, tc.style, tc.steady)
		}
		_ = term.Close()
	}
}

// TestCursorStyleDefaultsToASteadyBlock pins the shape a pane has before its
// guest says anything, which is what tuios has always shown.
func TestCursorStyleDefaultsToASteadyBlock(t *testing.T) {
	term := New(10, 5)
	defer func() { _ = term.Close() }()
	style, steady := term.CursorStyle()
	if style != CursorBlock || !steady {
		t.Errorf("a fresh emulator reports style %d steady %v, want block steady", style, steady)
	}
}

// TestCursorStyleSurvivesTheAlternateScreen is the vim case: DECSCUSR is a
// property of the terminal, not of the screen that happened to be active, so
// entering and leaving the alternate screen must not silently return the pane
// to a block.
//
// The second half is why the shape is held on the terminal rather than read off
// the active screen's cursor. The pure emulator saves and restores that cursor
// across an alternate-screen switch, so a shape set inside the alternate screen
// is discarded on the way out, while the ghostty backend observes DECSCUSR as it
// goes past and keeps it. Reading per-screen would have made the two backends
// disagree about the pane vim just exited.
func TestCursorStyleSurvivesTheAlternateScreen(t *testing.T) {
	term := New(10, 5)
	defer func() { _ = term.Close() }()
	if _, err := term.Write([]byte("\x1b[6 q\x1b[?1049h")); err != nil {
		t.Fatalf("enter alt screen: %v", err)
	}
	if style, steady := term.CursorStyle(); style != CursorBar || !steady {
		t.Errorf("on the alternate screen: style %d steady %v, want bar steady", style, steady)
	}
	if _, err := term.Write([]byte("\x1b[?1049l")); err != nil {
		t.Fatalf("leave alt screen: %v", err)
	}
	if style, steady := term.CursorStyle(); style != CursorBar || !steady {
		t.Errorf("back on the main screen: style %d steady %v, want bar steady", style, steady)
	}

	if _, err := term.Write([]byte("\x1b[?1049h\x1b[4 q\x1b[?1049l")); err != nil {
		t.Fatalf("set a shape inside the alternate screen: %v", err)
	}
	if style, steady := term.CursorStyle(); style != CursorUnderline || !steady {
		t.Errorf("after a shape set inside the alternate screen: style %d steady %v, want underline steady",
			style, steady)
	}
}

// TestRestoreCursorStyleRoundTrips is the reattach path in miniature: the
// daemon reads the shape off its emulator, it travels on the wire, and the
// client's fresh emulator is primed with it. A backend that dropped either half
// would rebuild every pane as a block.
func TestRestoreCursorStyleRoundTrips(t *testing.T) {
	for _, tc := range decscusrCases {
		src := New(10, 5)
		if _, err := src.Write([]byte(tc.seq)); err != nil {
			t.Fatalf("write %q: %v", tc.seq, err)
		}
		style, steady := src.CursorStyle()
		_ = src.Close()

		dst := New(10, 5)
		dst.RestoreCursorStyle(style, steady)
		gotStyle, gotSteady := dst.CursorStyle()
		if gotStyle != tc.style || gotSteady != tc.steady {
			t.Errorf("%q restored as style %d steady %v, want style %d steady %v",
				tc.seq, gotStyle, gotSteady, tc.style, tc.steady)
		}
		_ = dst.Close()
	}
}

// TestCursorStyleAfterRestoreYieldsToTheGuest keeps a primed shape from
// outliving its welcome: output that arrives after the restore is the guest
// talking now, and it wins.
func TestCursorStyleAfterRestoreYieldsToTheGuest(t *testing.T) {
	term := New(10, 5)
	defer func() { _ = term.Close() }()
	term.RestoreCursorStyle(CursorBar, true)
	if _, err := term.Write([]byte("\x1b[4 q")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if style, steady := term.CursorStyle(); style != CursorUnderline || !steady {
		t.Errorf("got style %d steady %v, want underline steady", style, steady)
	}
}
