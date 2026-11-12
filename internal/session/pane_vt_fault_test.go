package session

import (
	"testing"
	"time"
)

// TestFaultNoticesReachTheStreamInFaultOrder is a race regression. Two
// emulator faults close together, on a program's echo and on its next line,
// each put a reset and a notice into the pane's stream. They used to start a
// goroutine each, and the two raced for streamMu: when the third fault's
// "failed again" won, the second fault's reset landed after it, and a pane
// the daemon had stopped feeding showed the notice of one it still fed.
// TestEmulatorPanicKeepsTheDaemonAndOtherPanes failed this way in CI.
//
// The ways it can fail, written before the queue:
//   - the notices reach the stream out of order;
//   - a notice is dropped when it is queued while another goes in;
//   - the goroutine that puts them in is not started again once the queue
//     ran empty, and a later fault has no notice;
//   - queueing waits on streamMu, which vtWriter must never do: the reader
//     holds streamMu while it waits for vtWriter to take a chunk.
func TestFaultNoticesReachTheStreamInFaultOrder(t *testing.T) {
	p := &PTY{ctx: t.Context(), vtWriteChan: make(chan vtChunk, 8)}
	next := func() string {
		t.Helper()
		select {
		case c := <-p.vtWriteChan:
			if !c.notice {
				t.Fatalf("chunk %q is not marked as a notice", c.data)
			}
			return string(c.data)
		case <-time.After(10 * time.Second):
			t.Fatal("a queued notice never reached the emulator's channel")
			return ""
		}
	}

	for range 200 {
		// The reader holds streamMu, as it does while vtWriter faults.
		p.streamMu.Lock()
		queued := make(chan struct{})
		go func() {
			p.queueFaultNotice([]byte("first"))
			p.queueFaultNotice([]byte("second"))
			close(queued)
		}()
		select {
		case <-queued:
		case <-time.After(10 * time.Second):
			t.Fatal("queueing a notice waited on streamMu")
		}
		p.streamMu.Unlock()
		if a, b := next(), next(); a != "first" || b != "second" {
			t.Fatalf("the notices reached the stream as %q then %q, want first then second", a, b)
		}
	}

	// The queue ran empty, so the next notice needs a goroutine of its own.
	waitFor(t, "the notice queue runs empty", func() bool {
		p.faultNotices.mu.Lock()
		defer p.faultNotices.mu.Unlock()
		return !p.faultNotices.draining
	})
	p.queueFaultNotice([]byte("third"))
	if got := next(); got != "third" {
		t.Fatalf("a notice queued after the queue ran empty reached the stream as %q", got)
	}
}
