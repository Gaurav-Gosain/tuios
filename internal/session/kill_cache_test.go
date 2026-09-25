package session

import (
	"testing"
	"time"
)

// TestDeleteSessionRemovesAndTerminates is the manager-level guarantee behind a
// kill: the session leaves the manager and its PTY process is actually killed,
// not merely detached. A session that stays in the manager or whose shell keeps
// running is the daemon-side half of a kill that "did not remove it properly".
func TestDeleteSessionRemovesAndTerminates(t *testing.T) {
	d, _ := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "doomed")

	ids := sess.ListPTYIDs()
	if len(ids) == 0 {
		t.Fatal("precondition: session has no PTY to terminate")
	}
	pty := sess.GetPTY(ids[0])
	if pty == nil {
		t.Fatal("precondition: could not get the session PTY")
	}

	if err := d.manager.DeleteSession("doomed"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}

	if d.manager.GetSession("doomed") != nil {
		t.Error("session still present in the manager after DeleteSession")
	}
	if d.manager.GetSessionByID(sess.ID) != nil {
		t.Error("session still present in the ID index after DeleteSession")
	}

	// The shell process is killed asynchronously (Process.Kill then Wait), so poll.
	deadline := time.Now().Add(3 * time.Second)
	for !pty.IsExited() {
		if time.Now().After(deadline) {
			t.Fatal("the session PTY process was not terminated after DeleteSession")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
