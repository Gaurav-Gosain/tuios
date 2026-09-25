package session

import (
	"testing"
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
