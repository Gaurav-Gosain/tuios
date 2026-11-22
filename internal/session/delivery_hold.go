package session

import (
	"sync"
	"sync/atomic"
	"time"
)

// The delivery hold keeps a prompt the daemon types into a pane whole.
//
// submitPastes writes a prompt as a paste and then, up to
// promptSubmitMaxWait later, its submit key. Any other input that reached
// the pane in that gap (a person's keys, send-text, another delivery) went
// between the two: it joined the prompt, and the one Enter sent both. So
// the delivery holds the pane's input from before its first write until
// after its submit key, and every other write waits for it in PTY.Write.
//
// A hold ends on release or deliveryHoldMax after it began, whichever is
// first, so a delivery that stalls cannot stop the person from typing. A
// write that waited that long goes in. Replies to the program's terminal
// queries do not go through PTY.Write and never wait.

// deliveryHoldMax bounds how long a hold keeps other input out. A delivery
// waits at most promptSubmitMaxWait for each paste it writes, and a prompt
// is rarely more than three pastes.
const deliveryHoldMax = 2 * time.Second

// deliveryHold is a pane's hold. The zero value holds nothing.
type deliveryHold struct {
	// on is true while a hold is held, so a write into a pane with no
	// hold costs one atomic load.
	on atomic.Bool
	mu sync.Mutex
	// done is closed when the hold is released, nil when none is held.
	done chan struct{}
	// until is when the hold ends if it is not released.
	until time.Time
}

// current is the live hold's channel and end, or nil when none is held or
// the one held has run out.
func (h *deliveryHold) current() (chan struct{}, time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.done == nil || !time.Now().Before(h.until) {
		return nil, time.Time{}
	}
	return h.done, h.until
}

// wait returns once no live hold is held.
func (h *deliveryHold) wait() {
	if !h.on.Load() {
		return
	}
	for {
		done, until := h.current()
		if done == nil {
			return
		}
		timer := time.NewTimer(time.Until(until))
		select {
		case <-done:
		case <-timer.C:
		}
		timer.Stop()
	}
}

// take waits for any live hold to end, takes the hold, and returns its
// release.
func (h *deliveryHold) take() func() {
	for {
		h.wait()
		h.mu.Lock()
		if h.done != nil && time.Now().Before(h.until) {
			// Another delivery took it first.
			h.mu.Unlock()
			continue
		}
		done := make(chan struct{})
		h.done, h.until = done, time.Now().Add(deliveryHoldMax)
		h.on.Store(true)
		h.mu.Unlock()
		return func() {
			h.mu.Lock()
			if h.done == done {
				h.done = nil
				h.on.Store(false)
			}
			h.mu.Unlock()
			close(done)
		}
	}
}

// deliveryHolder is a pane that can hold its input for a delivery. *PTY is
// one, and a test pane that records bytes need not be.
type deliveryHolder interface {
	// holdForDelivery takes the pane's hold and returns the pane to write the
	// delivery through, which does not wait for the hold, and the hold's
	// release.
	holdForDelivery() (promptPane, func())
}

// heldPTY is a PTY as the delivery that holds its input writes to it.
type heldPTY struct{ *PTY }

// Write writes past the hold.
func (h heldPTY) Write(data []byte) (int, error) { return h.writeNow(data) }

func (p *PTY) holdForDelivery() (promptPane, func()) {
	return heldPTY{p}, p.deliveryHold.take()
}
