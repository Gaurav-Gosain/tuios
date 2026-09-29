package app

import (
	"encoding/base64"
	"strings"
	"sync"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// hostRecorder stands in for the host terminal and keeps every byte.
type hostRecorder struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (h *hostRecorder) Write(p []byte) (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.buf.Write(p)
}

func (h *hostRecorder) String() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.buf.String()
}

// notifyHarness builds a pane whose notifications are wired the way a real
// client wires them, with the host terminal recorded.
func notifyHarness(t *testing.T) (*OS, *terminal.Window, *hostRecorder) {
	t.Helper()
	host := &hostRecorder{}
	kp := NewKittyPassthroughWithOptions(KittyPassthroughOptions{Output: host, Caps: &HostCapabilities{}})
	m := &OS{Settings: config.DefaultSettings(), KittyPassthrough: kp}
	win := terminal.NewDaemonWindow("notify-win", "t", 0, 0, 40, 10, 0, "pty-notify", make(chan struct{}, 1), 100)
	t.Cleanup(win.Close)
	m.setupNotificationPassthrough(win)
	return m, win, host
}

// hostPayloads strips the synchronized-update wrapper WriteToHost adds and
// returns what is left.
func hostPayloads(s string) string {
	s = strings.ReplaceAll(s, "\x1b[?2026h", "")
	return strings.ReplaceAll(s, "\x1b[?2026l", "")
}

// A pane's desktop notification is forwarded to the host terminal. OSC 99 with
// e=1 carries its text base64 encoded, so it can hold ESC and BEL after
// decoding. Those must not reach the host, or any output a pane prints can
// write arbitrary sequences to the user's real terminal.
//
// OSC 9 and OSC 777 carry their text as it is. A raw ESC ends the OSC early,
// and libghostty then drops it, so those cases carry C1 controls instead:
// both emulators keep C1 bytes inside the OSC text, raw or UTF-8 encoded.
func TestPaneNotificationCannotWriteEscapesToHost(t *testing.T) {
	const (
		evil = "done\x1b]52;c;cm0gLXJmIH4K\x07\x1b[2J\x9b31m"
		// U+009B CSI, U+009D OSC and U+009C ST, first as raw bytes and then
		// UTF-8 encoded.
		evilC1 = "done\x9b31m\x9d52;c;cm0=\x9c\u009b2J\u009d52;c;cm0=\u009c"
	)
	b64 := func(p string) string { return base64.StdEncoding.EncodeToString([]byte(p)) }
	for _, tc := range []struct {
		name string
		seq  string
	}{
		{"osc99 e=1 body", "\x1b]99;e=1;" + b64(evil) + "\x1b\\"},
		{"osc99 e=1 title", "\x1b]99;e=1:p=title;" + b64(evil) + "\x1b\\"},
		{"osc99 e=1 c1", "\x1b]99;e=1;" + b64(evilC1) + "\x1b\\"},
		{"osc9", "\x1b]9;" + evilC1 + "\x07"},
		{"osc777", "\x1b]777;notify;title;" + evilC1 + "\x07"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, win, host := notifyHarness(t)
			win.WriteOutput([]byte(tc.seq))

			got := hostPayloads(host.String())
			if !strings.HasPrefix(got, "\x1b]9;") {
				t.Fatalf("no notification reached the host: %q", got)
			}
			if n := strings.Count(got, "\x1b"); n != 1 {
				t.Fatalf("host received %d ESC, want only the OSC 9 opener: %q", n, got)
			}
			if n := strings.Count(got, "\x07"); n != 1 {
				t.Fatalf("host received %d BEL, want only the OSC 9 terminator: %q", n, got)
			}
			if hasC1(got) {
				t.Fatalf("host received a C1 control: %q", got)
			}

			select {
			case msg := <-m.PendingNotification:
				if strings.ContainsAny(msg.Message, "\x1b\x07") || hasC1(msg.Message) {
					t.Fatalf("the dock message carries control bytes: %q", msg.Message)
				}
			default:
				t.Fatalf("no dock message was raised")
			}
		})
	}
}

// hasC1 reports whether s holds a C1 CSI, OSC or ST byte. A byte search
// catches both the raw byte and the second byte of its UTF-8 encoding.
func hasC1(s string) bool {
	return strings.IndexByte(s, 0x9b) >= 0 || strings.IndexByte(s, 0x9c) >= 0 || strings.IndexByte(s, 0x9d) >= 0
}
