package session

import (
	"encoding/json"
	"net"
	"testing"
	"time"
)

// twoWindowSession returns a session with two live daemon windows and their ids,
// which is the smallest shape the cross-agent verbs need: a sender and a
// recipient.
func twoWindowSession(t *testing.T, d *Daemon, name string) (*Session, string, string) {
	t.Helper()
	sess := makeSessionWithWindow(t, d, name)
	if _, err := sess.AddDaemonWindow("Second", nil); err != nil {
		t.Fatalf("AddDaemonWindow: %v", err)
	}
	st := sess.GetState()
	if len(st.Windows) < 2 {
		t.Fatalf("wanted two windows, got %d", len(st.Windows))
	}
	return sess, st.Windows[0].ID, st.Windows[1].ID
}

// runInPane types a command into a pane's shell.
func runInPane(t *testing.T, d *Daemon, sess *Session, window, line string) {
	t.Helper()
	pty, err := d.resolvePTYForTarget(sess, window)
	if err != nil {
		t.Fatalf("resolvePTYForTarget: %v", err)
	}
	waitForQuiet(t, pty, 200*time.Millisecond, 5*time.Second)
	if _, err := pty.Write([]byte(line + "\r")); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// waitForQuiet waits until the pane has printed nothing for quiet, so a test
// compares screens after the shell has drawn its prompt.
func waitForQuiet(t *testing.T, pty *PTY, quiet, limit time.Duration) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if last := pty.LastOutput(); last != 0 && time.Since(time.Unix(0, last)) >= quiet {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the pane did not go quiet within %v", limit)
}

// attachTUI attaches a TUI client to session over socketPath, which is the
// daemon's own socket or its link socket, and returns the client.
func attachTUI(t *testing.T, socketPath, session string) *TUIClient {
	t.Helper()
	conn, err := net.DialTimeout("unix", socketPath, 3*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	c := NewTUIClient()
	c.conn = conn
	if err := c.handshake("test", 80, 24, nil); err != nil {
		_ = conn.Close()
		t.Fatalf("handshake: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if _, err := c.AttachSession(session, false, 80, 24); err != nil {
		t.Fatalf("attach: %v", err)
	}
	return c
}

func jsonParams(v map[string]any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
