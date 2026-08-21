package app

import "sync"

// windowExitQueue parks the window exits that did not fit in WindowExitChan.
type windowExitQueue struct {
	mu       sync.Mutex
	pending  []string
	draining bool
	// stop unparks the drain goroutine when the client is going away, so a
	// disconnect that coincides with an exit flood does not strand it holding
	// the whole OS. Non-nil only while a drain is running.
	stop chan struct{}
	// closed marks the model as gone, so a late exit from the read loop is
	// discarded rather than queued behind a drain that has already been told to
	// stop.
	closed bool
}

// queueWindowExit reports a pane exit to the Update loop without blocking the
// caller.
//
// Both callers are OnPTYClosed handlers, which the daemon client runs on its
// read loop: the one goroutine that also carries pane output and every
// round-trip response. A send that blocks there does not delay one window's
// teardown, it stops the client outright.
//
// A larger WindowExitChan would only move the number, since killing a session
// or shutting the daemon down closes every pane at once and a user may have any
// number of them. Dropping the overflow is worse than the hang it avoids: a
// daemon-backed window never sets ProcessExited, so the maintenance tick's exit
// sweep cannot find it, and a dropped exit is a dead pane that stays on screen
// until the client restarts. So the overflow is parked and delivered in order by
// one short-lived goroutine, which may block because nothing waits on it.
func (m *OS) queueWindowExit(windowID string) {
	if m.WindowExitChan == nil {
		return
	}
	q := &m.windowExits
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	if !q.draining {
		select {
		case m.WindowExitChan <- windowID:
			return
		default:
		}
		q.draining = true
		q.stop = make(chan struct{})
		go m.drainWindowExits(q.stop)
	}
	// Once a drain is running everything goes behind it, so exits reach Update
	// in the order the panes died.
	q.pending = append(q.pending, windowID)
}

func (m *OS) drainWindowExits(stop chan struct{}) {
	q := &m.windowExits
	for {
		q.mu.Lock()
		if len(q.pending) == 0 {
			q.draining = false
			q.stop = nil
			q.mu.Unlock()
			return
		}
		windowID := q.pending[0]
		q.pending = q.pending[1:]
		q.mu.Unlock()

		select {
		case m.WindowExitChan <- windowID:
		case <-stop:
			q.mu.Lock()
			q.draining = false
			q.pending = nil
			q.mu.Unlock()
			return
		}
	}
}

// stopWindowExitDrain releases a parked drain goroutine. Called from Cleanup,
// where the model that owns WindowExitChan stops being read.
func (m *OS) stopWindowExitDrain() {
	q := &m.windowExits
	q.mu.Lock()
	q.closed = true
	q.pending = nil
	if q.stop != nil {
		close(q.stop)
		q.stop = nil
	}
	q.mu.Unlock()
}
