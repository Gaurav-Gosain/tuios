package app

import "testing"

// TestDockLinesStripsControlSequences: a command's stdout is untrusted text
// that the rail draws. SGR survives; an OSC title, an erase and a bell do not,
// and neither do the runes that are not ESC sequences but steer a terminal all
// the same: a right-to-left override, the one-byte CSI and a zero-width space.
// The rail's screen cannot show either fault: the compositor's string-to-cell
// parse keeps only SGR and OSC 8 today, so the sequences never reach it, and
// the runes reach it as cells nobody can see. So the boundary is checked on
// what dockLines returns.
func TestDockLinesStripsControlSequences(t *testing.T) {
	for _, tc := range []struct{ out, want string }{
		{"\x1b[31mRED\x1b[0m \x1b[2J\x1b]0;TITLE\x07PLAIN\n\x1b]52;c;eA==\x07two\n", "\x1b[31mRED\x1b[0m PLAIN\ntwo"},
		{"ab‮cd\u009b2Jef​gh\n", "abcd2Jefgh"},
	} {
		if got := dockLines([]byte(tc.out)); got != tc.want {
			t.Errorf("dockLines = %q, want %q", got, tc.want)
		}
	}
}
