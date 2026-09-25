package session

import (
	"testing"
	"time"
)

// waitOnlyPane waits for the session name to exist with n windows, and
// returns the session and its newest window. start-agent holds its reply
// until the agent is ready, so a test finds the pane from the daemon's side
// and makes it ready.
func waitOnlyPane(t *testing.T, d *Daemon, name string, n int) (*Session, string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if sess := d.manager.GetSession(name); sess != nil {
			if ws := sess.GetState().Windows; len(ws) == n {
				return sess, ws[n-1].ID
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("session %s never had %d windows", name, n)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
