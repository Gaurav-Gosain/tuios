package vt

import (
	"os"
	"strings"
	"testing"
)

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
		'✳',
		'✅',
		'✐',
		'✝',
		'✟',
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
	for i := 0; i < len(data); i += 16 * 1024 {
		_, _ = e.Write(data[i:min(i+16*1024, len(data))])
	}

	var input strings.Builder
	for x := range 80 {
		if c := e.scr.CellAt(x, 18); c != nil {
			input.WriteString(c.String())
		}
	}
	if got := strings.TrimSpace(input.String()); got != "❯" {
		t.Errorf("the input row reads %q, want just the prompt", got)
	}
	if e.title != "✳ Say hello in three words" {
		t.Errorf("the title is %q", e.title)
	}
}
