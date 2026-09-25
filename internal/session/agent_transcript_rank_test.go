package session

import "testing"

// newTestSessionWithWindow gives a test one real window without a daemon.
func newTestSessionWithWindow(t *testing.T) *Session {
	t.Helper()
	sess := newTestSession(t)
	if _, err := sess.AddDaemonWindow("shell", nil); err != nil {
		t.Fatalf("AddDaemonWindow: %v", err)
	}
	return sess
}
