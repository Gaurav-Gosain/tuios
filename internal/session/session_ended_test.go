package session

import (
	"testing"
	"time"
)

// This file covers the contract that makes 'tuios attach' exit when its session
// is killed: the daemon must tell every attached client that the session is
// gone, and the client must surface that exactly once.
//
// Before this existed, killing a session closed its PTYs and dropped it from the
// manager but left every attached client connected to nothing, so the client sat
// in a dead UI with no way to learn what had happened.

// attachTestClient connects a TUIClient to the test daemon and attaches it to a
// session, returning the client with its read loop running.
func attachTestClient(t *testing.T, sessionName string) *TUIClient {
	t.Helper()

	c := NewTUIClient()
	if err := c.Connect("test", 80, 24); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	if _, err := c.AttachSession(sessionName, false, 80, 24); err != nil {
		t.Fatalf("attach %s: %v", sessionName, err)
	}
	c.StartReadLoop()
	return c
}

// TestKilledSessionNotifiesAttachedClient is the regression test for the
// lingering client: killing a session must reach the attached client, naming the
// session that ended.
func TestKilledSessionNotifiesAttachedClient(t *testing.T) {
	d, _ := startTestDaemon(t)
	makeSessionWithWindow(t, d, "work")

	client := attachTestClient(t, "work")

	ended := make(chan string, 4)
	client.OnSessionEnded(func(name, _ string) { ended <- name })

	if err := d.manager.DeleteSession("work"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}

	select {
	case name := <-ended:
		if name != "work" {
			t.Errorf("session ended for %q, want work", name)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the attached client was never told its session was killed; it would linger in a dead UI")
	}
}

// TestSessionEndedFiresExactlyOnce guards against a client quitting twice or
// reporting the wrong exit reason because the notification was duplicated.
func TestSessionEndedFiresExactlyOnce(t *testing.T) {
	d, _ := startTestDaemon(t)
	makeSessionWithWindow(t, d, "work")

	client := attachTestClient(t, "work")

	ended := make(chan string, 8)
	client.OnSessionEnded(func(name, _ string) { ended <- name })

	if err := d.manager.DeleteSession("work"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}

	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("no session-ended notification")
	}

	// A second notification would mean the client saw the session die twice.
	select {
	case name := <-ended:
		t.Fatalf("session-ended fired more than once (second: %q)", name)
	case <-time.After(500 * time.Millisecond):
	}
}

// TestSessionEndedReachesEveryAttachedClient covers the multi-client case: a
// killed session must not leave one of its clients behind.
func TestSessionEndedReachesEveryAttachedClient(t *testing.T) {
	d, _ := startTestDaemon(t)
	makeSessionWithWindow(t, d, "work")

	first := attachTestClient(t, "work")
	second := attachTestClient(t, "work")

	firstEnded := make(chan struct{}, 1)
	secondEnded := make(chan struct{}, 1)
	first.OnSessionEnded(func(string, string) { firstEnded <- struct{}{} })
	second.OnSessionEnded(func(string, string) { secondEnded <- struct{}{} })

	if err := d.manager.DeleteSession("work"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}

	for name, ch := range map[string]chan struct{}{"first": firstEnded, "second": secondEnded} {
		select {
		case <-ch:
		case <-time.After(5 * time.Second):
			t.Errorf("the %s client was never told its session was killed", name)
		}
	}
}

// TestOtherSessionsAreUnaffected keeps the notification targeted: killing one
// session must not evict clients attached to another.
func TestOtherSessionsAreUnaffected(t *testing.T) {
	d, _ := startTestDaemon(t)
	makeSessionWithWindow(t, d, "work")
	makeSessionWithWindow(t, d, "notes")

	survivor := attachTestClient(t, "notes")

	ended := make(chan string, 1)
	survivor.OnSessionEnded(func(name, _ string) { ended <- name })

	if err := d.manager.DeleteSession("work"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}

	select {
	case name := <-ended:
		t.Fatalf("killing work evicted the client attached to notes (reported %q)", name)
	case <-time.After(750 * time.Millisecond):
	}
}

// TestKillSessionVerbNotifiesAttachedClient proves the notification is reached
// through the control plane too, which is the path an agent or a script uses.
func TestKillSessionVerbNotifiesAttachedClient(t *testing.T) {
	d, socketPath := startTestDaemon(t)
	makeSessionWithWindow(t, d, "work")

	client := attachTestClient(t, "work")

	ended := make(chan string, 1)
	client.OnSessionEnded(func(name, _ string) { ended <- name })

	c := dialVerb(t, socketPath)
	resp := c.call(t, `{"id":1,"verb":"kill-session","params":{"session":"work"}}`)
	result(t, resp)

	select {
	case name := <-ended:
		if name != "work" {
			t.Errorf("session ended for %q, want work", name)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("kill-session over the control plane did not notify the attached client")
	}
}

// TestSessionEndedLeavesTheConnectionUsable checks the daemon does not slam the
// connection shut along with the notification. The client needs a live socket to
// finish its own shutdown; tearing it down here would turn a clean exit back
// into the connection error this work replaced.
func TestSessionEndedLeavesTheConnectionUsable(t *testing.T) {
	d, _ := startTestDaemon(t)
	makeSessionWithWindow(t, d, "work")
	makeSessionWithWindow(t, d, "notes")

	client := attachTestClient(t, "work")

	ended := make(chan struct{}, 1)
	client.OnSessionEnded(func(string, string) { ended <- struct{}{} })

	disconnected := make(chan error, 1)
	client.OnDisconnect(func(err error) { disconnected <- err })

	if err := d.manager.DeleteSession("work"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("no session-ended notification")
	}

	select {
	case err := <-disconnected:
		t.Fatalf("the daemon dropped the connection instead of leaving the client to exit cleanly: %v", err)
	case <-time.After(500 * time.Millisecond):
	}

	// And it is still live enough to carry another request, which is what a
	// client finishing its own shutdown relies on.
	if _, err := client.SwitchSession("notes", 80, 24); err != nil {
		t.Fatalf("the connection was unusable after session-ended: %v", err)
	}
}
