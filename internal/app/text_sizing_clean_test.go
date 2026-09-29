package app

import (
	"strings"
	"testing"
)

// The OSC 66 a backend hands over is replayed to the host. The ghostty
// backend passes the payload on as the parser left it, so the app layer strips
// it. Only the opener and the terminator may be control characters.
func TestOSC66ReplayIsCleanedInTheAppLayer(t *testing.T) {
	for _, raw := range []string{
		"\x1b]66;s=2;big\x9c\u009b2J\x7ftext\a",
		"\x1b]66;s=2;big\x9c\u009b2J\x7ftext\x1b\\",
	} {
		got := string(cleanOSC66([]byte(raw)))
		if !strings.HasPrefix(got, "\x1b]66;s=2;big") || !strings.HasSuffix(got, "text\a") {
			t.Fatalf("cleanOSC66(%q) = %q, want the sequence kept", raw, got)
		}
		body := got[2 : len(got)-1]
		if strings.ContainsFunc(body, func(r rune) bool { return r < 0x20 || (r >= 0x7f && r <= 0x9f) || r == 0xfffd }) {
			t.Fatalf("cleanOSC66(%q) = %q, body holds a control character", raw, got)
		}
	}
}
