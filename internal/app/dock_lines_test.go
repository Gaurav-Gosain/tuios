package app

import "testing"

// TestDockLinesStripsControlSequences: a command's stdout is untrusted text
// that the rail draws. SGR survives; an OSC title, an erase and a bell do not.
// The rail's screen cannot show the fault, because the emulator consumes an
// OSC as a title change, so the boundary is checked on what dockLines returns.
func TestDockLinesStripsControlSequences(t *testing.T) {
	out := []byte("\x1b[31mRED\x1b[0m \x1b[2J\x1b]0;TITLE\x07PLAIN\n\x1b]52;c;eA==\x07two\n")
	want := "\x1b[31mRED\x1b[0m PLAIN\ntwo"
	if got := dockLines(out); got != want {
		t.Fatalf("dockLines = %q, want %q", got, want)
	}
}
