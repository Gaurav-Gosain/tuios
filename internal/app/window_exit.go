package app

import (
	"sync"

	"github.com/Gaurav-Gosain/tuios/internal/crashexit"
)

type windowExitQueue struct {
	mu       sync.Mutex
	pending  []string
	draining bool
	stop     chan struct{}
	closed   bool
}

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
	q.pending = append(q.pending, windowID)
}

func (m *OS) drainWindowExits(stop chan struct{}) {
	defer crashexit.Recover()
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
