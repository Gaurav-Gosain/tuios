package vt_test

import (
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// backendReply feeds in to the backend this build selects (pure-Go, or
// libghostty under the ghostty tag) and returns what it wrote back to the
// guest, or "" when it wrote nothing within a short wait.
func backendReply(t *testing.T, in string) string {
	t.Helper()
	term := vt.New(80, 24)
	t.Cleanup(func() { _ = term.Close() })
	if _, err := term.Write([]byte(in)); err != nil {
		t.Fatalf("write %q: %v", in, err)
	}
	got := make(chan string, 1)
	go func() {
		buf := make([]byte, 512)
		n, _ := term.Read(buf)
		got <- string(buf[:n])
	}()
	select {
	case s := <-got:
		return s
	case <-time.After(300 * time.Millisecond):
		return ""
	}
}

// TestStatusReportFormPerBackend pins the reply to each DSR and DA query on
// both backends. A reply keeps the form of its query: an ANSI query gets an
// ANSI answer and a DEC private query ("?") gets a private one, as in xterm.
// Issue #253 was CSI 5 n answered with the private CSI ? 0 n, which a strict
// parser (ratatui-image) never recognised.
func TestStatusReportFormPerBackend(t *testing.T) {
	ghostty := vt.Backend == "ghostty"
	pick := func(pure, gh string) string {
		if ghostty {
			return gh
		}
		return pure
	}
	for _, tc := range []struct {
		name, in, want string
	}{
		{"DSR 5, ANSI", "\x1b[5n", "\x1b[0n"},
		{"DSR 5, DEC private", "\x1b[?5n", "\x1b[?0n"},
		{"CPR, ANSI", "\x1b[3;4H\x1b[6n", "\x1b[3;4R"},
		{"DECXCPR, DEC private", "\x1b[3;4H\x1b[?6n", "\x1b[?3;4R"},
		{"DECXCPR under DECOM", "\x1b[10;20r\x1b[?6h\x1b[3;5H\x1b[?6n", "\x1b[?3;5R"},
		{"ANSI and private replies stay in order", "ab\x1b[6n\x1b[?5n", "\x1b[1;3R\x1b[?0n"},
		// Unanswered on both backends, the same as before.
		{"printer status", "\x1b[?15n", ""},
		{"UDK status", "\x1b[?25n", ""},
		{"keyboard status", "\x1b[?26n", ""},
		{"colour scheme", "\x1b[?996n", ""},
		// No sixel (4) until the host is known to show it. See
		// TestSixelAdvertisedFollowsHost.
		{"DA1", "\x1b[c", "\x1b[?62;22c"},
		{"DA1 with 0", "\x1b[0c", "\x1b[?62;22c"},
		{"DA2", "\x1b[>c", pick("\x1b[>1;10;0c", "\x1b[>0;0;0c")},
		{"DA3", "\x1b[=c", pick("", "\x1bP!|00000000\x1b\\")},
		{"XTVERSION", "\x1b[>0q", pick("", "\x1bP>|tuios\x1b\\")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := backendReply(t, tc.in); got != tc.want {
				t.Errorf("%s backend: %q replied %q, want %q", vt.Backend, tc.in, got, tc.want)
			}
		})
	}
}
