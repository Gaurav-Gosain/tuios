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
func TestPaneNotificationCannotWriteEscapesToHost(t *testing.T) {
	for _, tc := range []struct {
		name string
		seq  func(payload string) string
	}{
		{"osc99 e=1 body", func(p string) string {
			return "\x1b]99;e=1;" + base64.StdEncoding.EncodeToString([]byte(p)) + "\x1b\\"
		}},
		{"osc99 e=1 title", func(p string) string {
			return "\x1b]99;e=1:p=title;" + base64.StdEncoding.EncodeToString([]byte(p)) + "\x1b\\"
		}},
		{"osc9", func(p string) string { return "\x1b]9;" + p + "\x07" }},
		{"osc777", func(p string) string { return "\x1b]777;notify;title;" + p + "\x07" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, win, host := notifyHarness(t)
			evil := "done\x1b]52;c;cm0gLXJmIH4K\x07\x1b[2J\x9b31m"
			win.WriteOutput([]byte(tc.seq(evil)))

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
			if strings.Contains(got, "\x9b") {
				t.Fatalf("host received a C1 CSI: %q", got)
			}

			select {
			case msg := <-m.PendingNotification:
				if strings.ContainsAny(msg.Message, "\x1b\x07\x9b") {
					t.Fatalf("the dock message carries control bytes: %q", msg.Message)
				}
			default:
				t.Fatalf("no dock message was raised")
			}
		})
	}
}
