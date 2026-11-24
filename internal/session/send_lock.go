package session

import "sync"

// sendLock is the lock every write to one client connection takes
// (connState.sendMu). It hands the lock over in the order it was asked for.
//
// A sync.Mutex lets a goroutine that is already running take the lock ahead
// of one it has just woken, unless the woken one has waited for a
// millisecond. A pane that floods let go of the lock after each piece and took
// it straight back, so a quiet pane's echo waited behind several pieces in a
// row. Here an Unlock serves the oldest waiter first, and a flooding stream
// that asks again waits its turn behind it.
type sendLock struct {
	mu      sync.Mutex
	turn    sync.Cond
	next    uint64 // the ticket the next Lock takes
	serving uint64 // the ticket that holds the lock
}

// Lock waits until every Lock asked for before it has had its turn.
func (l *sendLock) Lock() {
	l.mu.Lock()
	ticket := l.next
	l.next++
	if l.turn.L == nil {
		l.turn.L = &l.mu
	}
	for ticket != l.serving {
		l.turn.Wait()
	}
	l.mu.Unlock()
}

// Unlock hands the lock to the oldest waiter.
func (l *sendLock) Unlock() {
	l.mu.Lock()
	l.serving++
	waiting := l.serving != l.next
	l.mu.Unlock()
	if waiting {
		l.turn.Broadcast()
	}
}

// Waiting is how many writers wait for the lock. The holder asks it.
func (l *sendLock) Waiting() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return max(int(l.next-l.serving)-1, 0)
}
