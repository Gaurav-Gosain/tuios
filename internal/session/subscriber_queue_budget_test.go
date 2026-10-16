package session

import (
	"bytes"
	"fmt"
	"runtime"
	"testing"
)

// TestIdleSubscriptionMemoryBudget holds what one client's subscription to an
// idle pane costs. The queue used to be a channel of 4096 chunks made whole on
// subscribe: 229 KB per (client, pane) before the pane printed anything, and
// 12 MB of a daemon's heap at 50 idle panes with one client attached. A pane
// that has printed a prompt and gone quiet needs a few slots, so the budget is
// a small fraction of the old channel and the test fails if it comes back.
func TestIdleSubscriptionMemoryBudget(t *testing.T) {
	const panes = 50
	// The old channel was 4096 slots of 56 bytes. An idle subscription may
	// cost at most a sixteenth of it, catch-up copy included.
	const budget = 4096 * 56 / 16

	ptys := make([]*PTY, panes)
	prompt := bytes.Repeat([]byte("$ "), 100)
	for i := range ptys {
		ptys[i] = queuePTY()
		ptys[i].feedRing(prompt)
	}

	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for i, p := range ptys {
		p.Subscribe(fmt.Sprintf("client-%d", i), 0)
	}
	runtime.ReadMemStats(&after)

	perPane := (after.TotalAlloc - before.TotalAlloc) / panes
	t.Logf("one idle subscription allocates %d bytes", perPane)
	if perPane > budget {
		t.Fatalf("one idle subscription allocates %d bytes, the budget is %d", perPane, budget)
	}
	runtime.KeepAlive(ptys)
}

// TestFloodedQueueGivesBackItsPeak fills a queue to subscriberSlots with its
// ring wrapped, so every doubling copies a wrapped ring. The chunks must come
// out in the order they went in, the slot past the limit must be refused, and
// once drained and trimmed the queue must hold no more than queueKeep slots:
// a pane that flooded once does not keep the peak.
func TestFloodedQueueGivesBackItsPeak(t *testing.T) {
	sub := newSubscriber()
	q := &sub.out
	in, out := int64(0), int64(0)
	push := func() bool {
		if !q.push(ptyChunk{end: in + 1}) {
			return false
		}
		in++
		return true
	}
	pop := func() {
		c, ok, _ := q.pop()
		if !ok {
			t.Fatalf("pop found nothing with %d queued", in-out)
		}
		if out++; c.end != out {
			t.Fatalf("chunk %d came out as %d", out, c.end)
		}
	}
	// Wrap the ring before each doubling: take some, then fill.
	for q.len() < subscriberSlots {
		for range q.len() / 3 {
			pop()
		}
		for len(q.buf) == 0 || q.len() < len(q.buf) {
			push()
		}
		if q.len() < subscriberSlots {
			push()
		}
	}
	if push() {
		t.Fatalf("a queue of %d chunks took one more", subscriberSlots)
	}
	if len(q.buf) != subscriberSlots {
		t.Fatalf("the full queue has %d slots, want %d", len(q.buf), subscriberSlots)
	}
	for q.len() > 0 {
		pop()
	}
	if in != out {
		t.Fatalf("%d chunks went in and %d came out", in, out)
	}
	q.trim()
	if len(q.buf) > queueKeep {
		t.Fatalf("a drained queue holds %d slots, the most it keeps is %d", len(q.buf), queueKeep)
	}
	q.close()
	if _, ok, closed := q.pop(); ok || !closed {
		t.Fatal("a closed and drained queue did not report closed")
	}
}
