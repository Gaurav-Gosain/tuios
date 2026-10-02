package session

import (
	"testing"
	"time"
)

// TestUnsubscribedConnectionReceivesNoEvents verifies a connection that never
// subscribed is never sent events: it only ever gets its own verb responses.
func TestUnsubscribedConnectionReceivesNoEvents(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "work")

	c := dialVerb(t, sp)
	// A normal request/response works.
	result(t, c.call(t, `{"id":1,"verb":"list-windows","params":{"session":"work"}}`))

	// Cause events on the daemon.
	if _, err := sess.AddDaemonWindow("noise", nil); err != nil {
		t.Fatalf("AddDaemonWindow: %v", err)
	}

	// No event line should arrive; a short read must time out.
	_ = c.conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if _, err := c.r.ReadBytes('\n'); err == nil {
		t.Fatal("unsubscribed connection unexpectedly received an event line")
	}
}

// TestWaitForSessionExistsAlreadyTrue verifies the wait returns immediately when
// the session already exists.
func TestWaitForSessionExistsAlreadyTrue(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "work")

	c := dialVerb(t, sp)
	resp := c.call(t, `{"id":1,"verb":"wait-for","params":{"condition":"session-exists","session":"work","timeout":8000}}`)
	res := result(t, resp)
	if res["matched"] != true {
		t.Fatalf("wait result not matched: %v", res)
	}
}
