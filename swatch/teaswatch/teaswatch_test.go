package teaswatch

import (
	"fmt"
	"testing"

	"tuios.dev/swatch"
)

// FuzzParsePaletteReply feeds the OSC 4 reply parser what a terminal could
// send. Whatever the bytes, it must not panic, it must accept only the
// sixteen slots, and a colour it accepts must read back the same when
// written in the four-digit form terminals answer with.
func FuzzParsePaletteReply(f *testing.F) {
	for _, s := range []string{
		"\x1b]4;1;rgb:cdcd/0000/0000\x1b\\",
		"\x1b]4;15;rgb:ff/ff/ff\x07",
		"4;3;rgb:f/8/0",
		"4;16;rgb:00/00/00",
		"4;-1;rgb:00/00/00",
		"10;rgb:0000/0000/0000",
		"4;2;rgb:12345/0/0",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		idx, c, ok := ParsePaletteReply(s)
		if !ok {
			return
		}
		if idx < 0 || idx > 15 || c == nil {
			t.Fatalf("accepted slot %d colour %v from %q", idx, c, s)
		}
		r, g, b, _ := c.RGBA()
		again := fmt.Sprintf("4;%d;rgb:%04x/%04x/%04x", idx, r, g, b)
		idx2, c2, ok := ParsePaletteReply(again)
		if !ok || idx2 != idx || swatch.Hex(c2) != swatch.Hex(c) {
			t.Fatalf("%q read as %d %s, and %q as %d %s", s, idx, swatch.Hex(c), again, idx2, swatch.Hex(c2))
		}
	})
}
