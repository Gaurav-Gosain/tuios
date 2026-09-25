package session

import (
	"testing"
	"time"
)

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

// TestWaitForWindowExit verifies wait-for window-exit resolves when the target
// window's shell process exits.
func TestWaitForWindowExit(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "work")

	c := dialVerb(t, sp)
	done := make(chan map[string]any, 1)
	go func() {
		done <- c.call(t, `{"id":1,"verb":"wait-for","params":{"condition":"window-exit","session":"work","timeout":8000}}`)
	}()

	time.Sleep(150 * time.Millisecond)
	pty, err := d.resolvePTYForTarget(sess, "")
	if err != nil {
		t.Fatalf("resolvePTYForTarget: %v", err)
	}
	if _, err := pty.Write([]byte("exit\r")); err != nil {
		t.Fatalf("pty write: %v", err)
	}

	select {
	case resp := <-done:
		res := result(t, resp)
		if res["matched"] != true {
			t.Fatalf("wait result not matched: %v", res)
		}
	case <-time.After(12 * time.Second):
		t.Fatal("wait-for window-exit did not resolve")
	}
}

// TestWaitForWindowIdle verifies wait-for window-idle resolves after a quiet
// period with no output.
func TestWaitForWindowIdle(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "work")

	c := dialVerb(t, sp)
	start := time.Now()
	resp := c.call(t, `{"id":1,"verb":"wait-for","params":{"condition":"window-idle","session":"work","idle":250,"timeout":8000}}`)
	res := result(t, resp)
	if res["matched"] != true {
		t.Fatalf("wait result not matched: %v", res)
	}
	if elapsed := time.Since(start); elapsed < 200*time.Millisecond {
		t.Fatalf("idle resolved too fast (%v); should wait out the idle window", elapsed)
	}
}

// TestWaitForSessionExists verifies wait-for session-exists resolves when a
// session with the requested name is created after the wait begins.
func TestWaitForSessionExists(t *testing.T) {
	d, sp := startTestDaemon(t)

	c := dialVerb(t, sp)
	done := make(chan map[string]any, 1)
	go func() {
		done <- c.call(t, `{"id":1,"verb":"wait-for","params":{"condition":"session-exists","session":"later","timeout":8000}}`)
	}()

	time.Sleep(150 * time.Millisecond)
	if _, err := d.manager.CreateSession("later", &SessionConfig{}, 80, 24); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	select {
	case resp := <-done:
		res := result(t, resp)
		if res["matched"] != true || res["session"] != "later" {
			t.Fatalf("wait result = %v, want matched session later", res)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("wait-for session-exists did not resolve")
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
