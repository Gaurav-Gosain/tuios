package session

import (
	"testing"
)

// TestNotifyCallbackParksTheNotification checks the daemon's emulator hands a
// desktop notification to the read goroutine instead of dropping it, for each
// of the three sequences.
func TestNotifyCallbackParksTheNotification(t *testing.T) {
	sess, winID := bareSessionWithWindow(t)
	pty := sess.GetPTY(ptyIDOfWindow(t, sess, winID))
	for _, tc := range []struct {
		name, seq, title, body string
	}{
		{"osc 9", "\x1b]9;Approval requested: go test\x07", "", "Approval requested: go test"},
		{"osc 777", "\x1b]777;notify;Claude Code;Claude needs your permission to use Bash\x07", "Claude Code", "Claude needs your permission to use Bash"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			feedVT(t, pty, tc.seq)
			n, ok := pty.takeAgentNotify()
			if !ok {
				t.Fatal("no notification parked")
			}
			if n.title != tc.title || n.body != tc.body {
				t.Fatalf("parked %+v, want title %q body %q", n, tc.title, tc.body)
			}
			if _, again := pty.takeAgentNotify(); again {
				t.Fatal("a taken notification came back")
			}
		})
	}
	// Progress is OSC 9 too, and must not read as a notification.
	feedVT(t, pty, "\x1b]9;4;1;50\x07")
	if _, ok := pty.takeAgentNotify(); ok {
		t.Fatal("an OSC 9;4 progress report was parked as a notification")
	}
}

// windowStateOf returns a copy of a window's state.
func windowStateOf(t *testing.T, sess *Session, windowID string) WindowState {
	t.Helper()
	for _, w := range sess.GetState().Windows {
		if w.ID == windowID {
			return w
		}
	}
	t.Fatalf("window %s not found", windowID)
	return WindowState{}
}
