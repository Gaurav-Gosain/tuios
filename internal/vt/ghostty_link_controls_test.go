//go:build ghostty

package vt

import (
	"strings"
	"testing"
)

// The ghostty backend reads a link's address back from libghostty, which
// keeps the bytes the parser let through. They are written back to the host
// inside OSC 8, so they are stripped the same way the Go emulator strips them.
func TestGhosttyHyperlinkDropsControlCharacters(t *testing.T) {
	for _, in := range []string{
		"\x1b]8;;http://a\u009c\u009b2Jb\x07X\x1b]8;;\x07",
		"\x1b]8;;http://a\x7fb\x07X\x1b]8;;\x07",
		"\x1b]8;;http://a\x9cb\x07X\x1b]8;;\x07",
	} {
		g := NewGhosttyTerminal(20, 5)
		_, _ = g.Write([]byte(in))
		c := g.CellAt(0, 0)
		if c == nil {
			t.Fatalf("%q: no cell", in)
		}
		if strings.ContainsFunc(c.Link.URL, func(r rune) bool { return r < 0x20 || (r >= 0x7f && r <= 0x9f) || r == 0xfffd }) {
			t.Errorf("%q stored a link with a control character: %q", in, c.Link.URL)
		}
		_ = g.Close()
	}
}
