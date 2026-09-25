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
