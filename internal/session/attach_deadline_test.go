package session

import (
	"net"
	"testing"
	"time"
)

// TestAttachDeadlineBoundsTheReply is a daemon that says hello and then
// stalls: it reads the attach and never answers. A client waiting for a new
// daemon sets AttachDeadline to the end of its wait, and the attach must give
// up by then, not after the 30 seconds an attach is otherwise given. The e2e
// suite cannot make a real daemon stall between its hello and its attach
// reply, so this is checked at the wire.
func TestAttachDeadlineBoundsTheReply(t *testing.T) {
	clientEnd, daemonEnd := net.Pipe()
	t.Cleanup(func() {
		_ = clientEnd.Close()
		_ = daemonEnd.Close()
	})
	// The stalled daemon: it takes the attach and says nothing.
	go func() {
		for {
			if _, err := ReadMessage(daemonEnd); err != nil {
				return
			}
		}
	}()

	c := newTestTUIClient(clientEnd)
	c.AttachDeadline = time.Now().Add(300 * time.Millisecond)
	done := make(chan error, 1)
	start := time.Now()
	go func() {
		_, err := c.AttachSession("work", false, 80, 24)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("the attach to a daemon that never answered succeeded")
		}
		if took := time.Since(start); took > 2*time.Second {
			t.Fatalf("the attach gave up after %s, past its deadline", took)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the attach to a stalled daemon was still waiting 5 s later, past its 300 ms deadline")
	}
	if !c.AttachDeadline.IsZero() {
		t.Error("AttachDeadline was kept, so a later attach on this client would get it too")
	}
}
