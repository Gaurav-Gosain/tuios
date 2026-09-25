package session

import (
	"testing"
)

// The restored mark is the only thing at session level that distinguishes a
// session the daemon rebuilt from saved state from one that has been alive for
// days. These tests pin the two halves of its life: it is set by a restore and
// only by a restore, and the first attach takes it off for good.

// TestClearingTheRestoredMarkDoesNotPushState pins the reason the clear is not
// published. It runs inside the attach handler, after the connection's session
// is recorded, so a push would reach the attaching client on the same socket
// ahead of the attach reply it is blocked on, and that client fails the attach
// with "unexpected response" instead of opening.
func TestClearingTheRestoredMarkDoesNotPushState(t *testing.T) {
	tmpDir := t.TempDir()
	defer useResurrectionDir(tmpDir)()

	sess := newTestSession(t)
	sess.MarkRestored()

	pushes := 0
	sess.SetStateSink(func(*SessionState) { pushes++ })

	sess.ClearRestored()

	if pushes != 0 {
		t.Errorf("clearing the restored mark pushed state %d times; it would outrun the attach reply", pushes)
	}
	if sess.GetState().Restored {
		t.Error("the mark was not cleared")
	}
}
