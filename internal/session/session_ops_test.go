package session

import (
	"testing"
)

// newTestSession creates a session backed by a real shell for the state-op tests.
func newTestSession(t *testing.T) *Session {
	t.Helper()
	sess, err := NewSession("ops-test", &SessionConfig{}, 80, 24)
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	t.Cleanup(sess.Stop)
	return sess
}
