package dockengine

import "testing"

// TestDockLinesStripsControlSequences: a command's stdout is untrusted text
// that the rail draws. SGR survives; an OSC title, an erase and a bell do not,
// and neither do the runes that are not ESC sequences but steer a terminal all
// the same: a right-to-left override, the one-byte CSI and a zero-width space.
// The rail's screen cannot show either fault: the compositor's string-to-cell
// parse keeps only SGR and OSC 8 today, so the sequences never reach it, and
// the runes reach it as cells nobody can see. So the boundary is checked on
// what Lines returns.
func TestDockLinesStripsControlSequences(t *testing.T) {
	for _, tc := range []struct{ out, want string }{
		{"\x1b[31mRED\x1b[0m \x1b[2J\x1b]0;TITLE\x07PLAIN\n\x1b]52;c;eA==\x07two\n", "\x1b[31mRED\x1b[0m PLAIN\ntwo"},
		{"ab\u202ecd\u009b2Jef\u200bgh\n", "abcd2Jefgh"},
	} {
		if got := Lines([]byte(tc.out)); got != tc.want {
			t.Errorf("Lines = %q, want %q", got, tc.want)
		}
	}
}

// TestDockLinesKeepsCleanLinks: a rail row keeps an OSC 8 link (issue #2179),
// rewritten in one form without its parameters, and closed at the line's end
// when the command left it open, so it cannot run into the next row. A link
// whose address has a space or a control character is dropped and its text
// kept. A dock cell keeps no link at all.
func TestDockLinesKeepsCleanLinks(t *testing.T) {
	for _, tc := range []struct{ out, want string }{
		{"\x1b]8;id=1;https://e.com/a\x1b\\PR\x1b]8;;\x07 x\n", "\x1b]8;;https://e.com/a\x1b\\PR\x1b]8;;\x1b\\ x"},
		{"\x1b]8;;https://e.com/b\x07open\nnext\n", "\x1b]8;;https://e.com/b\x1b\\open\x1b]8;;\x1b\\\nnext"},
		{"\x1b]8;;https://e.com/a b\x07bad\x1b]8;;\x07\n", "bad"},
		{"\x1b]8;;https://e.com/\x1b[2Jc\x07cut\n", "cut"},
		{"\x1b]8;;https://e.com/u\nrest\n", "\nrest"},
	} {
		if got := Lines([]byte(tc.out)); got != tc.want {
			t.Errorf("Lines(%q) = %q, want %q", tc.out, got, tc.want)
		}
	}
	if got := FirstLine([]byte("\x1b]8;;https://e.com/a\x07PR\x1b]8;;\x07\n")); got != "PR" {
		t.Errorf("FirstLine = %q, want the link dropped", got)
	}
}
