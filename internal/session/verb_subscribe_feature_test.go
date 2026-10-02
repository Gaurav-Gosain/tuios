//go:build !slim

package session

import "testing"

// TestSubscribeTwoSubscribersSameSequence verifies two independent connections
// subscribed to the same events see identical sequence numbers over the wire.
func TestSubscribeTwoSubscribersSameSequence(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "work")

	a := dialVerb(t, sp)
	b := dialVerb(t, sp)
	result(t, a.call(t, `{"id":1,"verb":"subscribe","params":{"session":"work","types":["window-created"]}}`))
	result(t, b.call(t, `{"id":1,"verb":"subscribe","params":{"session":"work","types":["window-created"]}}`))

	if _, err := sess.AddDaemonWindow("x", nil); err != nil {
		t.Fatalf("AddDaemonWindow: %v", err)
	}

	ea := a.readResp(t)
	eb := b.readResp(t)
	if ea["type"] != EventWindowCreated || eb["type"] != EventWindowCreated {
		t.Fatalf("types = %v / %v, want window-created", ea["type"], eb["type"])
	}
	sa, _ := ea["seq"].(float64)
	sb, _ := eb["seq"].(float64)
	if sa == 0 || sa != sb {
		t.Fatalf("subscribers saw different seq: a=%v b=%v", ea["seq"], eb["seq"])
	}
}

// TestSubscribeRejectsSecondSubscription verifies a connection cannot open two
// event streams at once.
func TestSubscribeRejectsSecondSubscription(t *testing.T) {
	_, sp := startTestDaemon(t)
	c := dialVerb(t, sp)
	result(t, c.call(t, `{"id":1,"verb":"subscribe","params":{}}`))
	resp := c.call(t, `{"id":2,"verb":"subscribe","params":{}}`)
	if code := errCode(t, resp); code != ErrVerbInvalidRequest {
		t.Fatalf("second subscribe error = %q, want %q", code, ErrVerbInvalidRequest)
	}
}
