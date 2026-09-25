package vt

import (
	"os"
	"strings"
	"testing"
)

// The 8-bit C1 controls and UTF-8 occupy the same bytes, and a terminal has to
// choose. This one is a UTF-8 terminal, so the 8-bit forms are not recognised
// while a string sequence is being collected.
//
// What went wrong without that: 0x9C is the 8-bit String Terminator, and it is
// also the middle byte of U+2733 (e2 9c b3). A window title of "✳ Say hello in
// three words" therefore ended its own OSC after one byte, and the rest of the
// payload was printed at the cursor, which in a terminal UI is wherever that
// program last put it. In Claude Code that is the input box, so the title
// appeared as text inside it and typing overwrote it character by character.

// screenOf is everything the emulator has on its grid.
func screenOf(e *Emulator, w, h int) string {
	var b strings.Builder
	for y := range h {
		for x := range w {
			if c := e.scr.CellAt(x, y); c != nil {
				b.WriteString(c.String())
			}
		}
	}
	return strings.TrimSpace(b.String())
}

// TestATitleWhoseBytesContainAC1ControlIsNotTornApart.
//
// Negative control: letting the transition table see a byte with the top bit
// set while a string is being collected puts the tail of each of these on the
// screen and leaves the title empty.
func TestATitleWhoseBytesContainAC1ControlIsNotTornApart(t *testing.T) {
	for _, r := range []rune{
		'✳', // ✳ e2 9c b3, the one that was reported
		'✅', // ✅ e2 9c 85
		'✐', // ✐ e2 9c 90, which carries the 8-bit DCS byte
		'✝', // ✝ e2 9c 9d, the 8-bit OSC byte
		'✟', // ✟ e2 9c 9f, the 8-bit APC byte
	} {
		e := NewEmulator(40, 4)
		_, _ = e.Write([]byte("\x1b]0;" + string(r) + " Title\x07"))

		if want := string(r) + " Title"; e.title != want {
			t.Errorf("%q: title is %q, want %q", r, e.title, want)
		}
		if got := screenOf(e, 40, 4); got != "" {
			t.Errorf("%q: the title leaked onto the screen as %q", r, got)
		}
	}
}

// TestATitleSplitAcrossReadsSurvives, because a 16KB reader splits wherever it
// lands, including inside one of these characters.
func TestATitleSplitAcrossReadsSurvives(t *testing.T) {
	seq := "\x1b]0;✳ Title\x07"
	for i := 1; i < len(seq); i++ {
		e := NewEmulator(40, 4)
		_, _ = e.Write([]byte(seq[:i]))
		_, _ = e.Write([]byte(seq[i:]))
		if e.title != "✳ Title" {
			t.Errorf("split at %d: title is %q", i, e.title)
		}
		if got := screenOf(e, 40, 4); got != "" {
			t.Errorf("split at %d: the title leaked onto the screen as %q", i, got)
		}
	}
}

// TestTheStreamThatWasReported replays the bytes a real Claude Code session
// wrote, captured with TUIOS_PTY_LOG while the fault was on screen.
//
// It is the whole reason that switch exists: a rendering fault in a live
// session cannot be reasoned about from a screenshot, and a capture taken
// afterwards shows the grid as it is now rather than at the moment it went
// wrong. With the bytes it is a unit test.
func TestTheStreamThatWasReported(t *testing.T) {
	data, err := os.ReadFile("testdata/claude_title_ghost.raw")
	if err != nil {
		t.Skipf("no captured stream: %v", err)
	}
	e := NewEmulator(80, 24)
	// In the chunks a 16KB reader would have produced.
	for i := 0; i < len(data); i += 16 * 1024 {
		_, _ = e.Write(data[i:min(i+16*1024, len(data))])
	}

	var input strings.Builder
	for x := range 80 {
		if c := e.scr.CellAt(x, 18); c != nil {
			input.WriteString(c.String())
		}
	}
	// The input box holds its prompt mark and nothing else.
	if got := strings.TrimSpace(input.String()); got != "❯" {
		t.Errorf("the input row reads %q, want just the prompt", got)
	}
	if e.title != "✳ Say hello in three words" {
		t.Errorf("the title is %q", e.title)
	}
}
