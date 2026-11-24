package session

import (
	"sync"
	"sync/atomic"
)

// sendLock is the lock every write to one client connection takes
// (connState.sendMu).
//
// With nobody waiting it is taken and let go with one atomic operation each,
// as a sync.Mutex is. Under contention it hands the lock over in turn, and a
// small write (LockSmall: an echo, a reply) goes before the pieces of a
// flooding stream. A sync.Mutex let the running goroutine take the lock ahead
// of the one it had just woken, so a flooding stream that let go after a
// piece took it straight back, and a quiet pane's echo waited behind several
// pieces in a row. Strict turns alone made the echo queue behind every
// flooding stream that waited.
type sendLock struct {
	// state is 1 while the lock is held, plus 2 for each waiter.
	state atomic.Int64
	// small and bulk are the waiters, oldest first, under mu. A waiter's
	// channel is closed when the lock is handed to it.
	mu    sync.Mutex
	small []chan struct{}
	bulk  []chan struct{}
}

// Lock takes the lock for a bulk write.
func (l *sendLock) Lock() { l.lock(false) }

// LockSmall takes the lock for a small write, which goes before the bulk
// writers that wait.
func (l *sendLock) LockSmall() { l.lock(true) }

func (l *sendLock) lock(small bool) {
	if l.state.CompareAndSwap(0, 1) {
		return
	}
	l.mu.Lock()
	// Counted before the check, so an Unlock from now on takes the slow
	// path and finds this waiter.
	if s := l.state.Add(2); s&1 == 0 {
		// Let go between the failed swap and the count, with nobody queued:
		// take it.
		l.state.Add(1 - 2)
		l.mu.Unlock()
		return
	}
	ch := make(chan struct{})
	if small {
		l.small = append(l.small, ch)
	} else {
		l.bulk = append(l.bulk, ch)
	}
	l.mu.Unlock()
	<-ch
}

// Unlock hands the lock to the oldest small waiter, else the oldest bulk
// one, else lets it go.
func (l *sendLock) Unlock() {
	if l.state.CompareAndSwap(1, 0) {
		return
	}
	l.mu.Lock()
	var next chan struct{}
	switch {
	case len(l.small) > 0:
		next, l.small = l.small[0], l.small[1:]
	case len(l.bulk) > 0:
		next, l.bulk = l.bulk[0], l.bulk[1:]
	}
	if next == nil {
		l.state.Add(-1)
		l.mu.Unlock()
		return
	}
	// The lock stays held: it passes to next.
	l.state.Add(-2)
	l.mu.Unlock()
	close(next)
}

// Waiting is how many writers wait for the lock.
func (l *sendLock) Waiting() int { return int(l.state.Load() >> 1) }
