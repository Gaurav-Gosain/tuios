package vt

import (
	"strings"
	"testing"
)

// An OSC 8 link's address and parameters are written back out to the host
// terminal when the frame is drawn. The parser ends the sequence at ESC and
// BEL, but a raw 0x9c, a C1 control encoded as UTF-8 and DEL all got through
// into the stored link, and from there into the host's own OSC 8.
func TestHyperlinkDropsControlCharacters(t *testing.T) {
	for _, in := range []string{
		"\x1b]8;;http://a\x9cb\x07X",
		"\x1b]8;;http://a\u009c\u009b2Jb\x07X",
		"\x1b]8;;http://a\x7fb\x07X",
		"\x1b]8;id=\u009cz;http://ab\x07X",
	} {
		e := NewEmulator(20, 5)
		_, _ = e.Write([]byte(in))
		link := e.CellAt(0, 0).Link
		for _, s := range []string{link.URL, link.Params} {
			if strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || (r >= 0x7f && r <= 0x9f) || r == 0xfffd }) {
				t.Errorf("%q stored a link with a control character: url=%q params=%q", in, link.URL, link.Params)
			}
		}
		if !strings.HasPrefix(link.URL, "http://a") {
			t.Errorf("%q lost the link: url=%q", in, link.URL)
		}
	}
}

// OSC 66 text sizing is replayed to the host terminal. The replayed sequence
// must hold no control characters beyond its own opener and terminator.
func TestTextSizingReplayDropsControlCharacters(t *testing.T) {
	e := NewEmulator(40, 5)
	var got []byte
	e.SetTextSizingFunc(func(raw []byte, _, _, _, _ int) { got = raw })
	_, _ = e.Write([]byte("\x1b]66;s=2;big\x9c\u009b2J\x7ftext\x07"))
	if len(got) == 0 {
		t.Fatal("no OSC 66 was forwarded")
	}
	body := string(got[2 : len(got)-1])
	if strings.ContainsFunc(body, func(r rune) bool { return r < 0x20 || (r >= 0x7f && r <= 0x9f) || r == 0xfffd }) {
		t.Fatalf("forwarded OSC 66 carries a control character: %q", got)
	}
}
