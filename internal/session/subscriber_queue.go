package session

import (
	"sync"
	"time"
)

// subscriberSlots is the most chunks one (client, pane) stream holds. A
// stream that would take one more is gapped and rebuilt from the ring, the
// same as one past maxSubscriberQueue bytes.
const subscriberSlots = 4096

// queueKeep is the slot count a drained queue keeps. A queue grown past it by
// a backlog lets its array go once it has stayed empty for queueTrimAfter, so
// a pane that flooded once does not hold the peak for the rest of its life.
const queueKeep = 16

// queueTrimAfter is how long a queue grown past queueKeep stays empty before
// its taker trims it. Letting the array go at every drain made a pane that
// floods in bursts grow it again from queueKeep at every burst.
const queueTrimAfter = time.Second

// ptyQueue is the chunks one stream has been handed and not yet taken. It
// replaces a buffered channel of subscriberSlots chunks: that channel was
// made whole on subscribe, 56 bytes a slot and 229 KB per (client, pane),
// before the pane printed anything. This one starts empty, doubles with the
// backlog up to subscriberSlots, and gives its array back once it has stayed
// drained (trim).
//
// Any number of goroutines push; one goroutine takes. The taker calls pop
// until it reports nothing, and only then waits on wake. A push that makes
// the queue non-empty leaves a token on wake, so a taker that saw it empty
// always hears of the next chunk. A stale token costs the taker one empty
// pop.
type ptyQueue struct {
	mu     sync.Mutex
	buf    []ptyChunk // a ring; its length is zero or a power of two
	head   int
	n      int
	closed bool
	// wake holds one token when a chunk may be waiting. Made by
	// newSubscriber; a queue without one never wakes a taker.
	wake chan struct{}
}

// newSubscriber is a stream with an empty queue.
func newSubscriber() *ptySubscriber {
	sub := &ptySubscriber{}
	sub.out.wake = make(chan struct{}, 1)
	return sub
}

// push adds c behind everything queued. It reports false, and queues
// nothing, when the queue holds subscriberSlots chunks or is closed.
func (q *ptyQueue) push(c ptyChunk) bool {
	q.mu.Lock()
	if q.closed || q.n == subscriberSlots {
		q.mu.Unlock()
		return false
	}
	if q.n == len(q.buf) {
		q.grow()
	}
	q.buf[(q.head+q.n)&(len(q.buf)-1)] = c
	q.n++
	first := q.n == 1
	q.mu.Unlock()
	if first {
		q.signal()
	}
	return true
}

// grow doubles the ring, from queueKeep for an empty one, and lays its
// chunks out from index zero. Called with mu held and the ring full.
func (q *ptyQueue) grow() {
	next := make([]ptyChunk, max(queueKeep, 2*len(q.buf)))
	if q.n > 0 {
		k := copy(next, q.buf[q.head:])
		copy(next[k:], q.buf[:q.head])
	}
	q.buf, q.head = next, 0
}

// pop takes the oldest chunk. ok is false when there is none, and closed then
// says no chunk will come again, as a receive from a closed and drained
// channel does.
func (q *ptyQueue) pop() (c ptyChunk, ok, closed bool) {
	q.mu.Lock()
	if q.n == 0 {
		closed = q.closed
		q.mu.Unlock()
		return ptyChunk{}, false, closed
	}
	c = q.buf[q.head]
	// The slot would otherwise keep the chunk's bytes alive.
	q.buf[q.head] = ptyChunk{}
	q.head = (q.head + 1) & (len(q.buf) - 1)
	q.n--
	if q.n == 0 {
		q.head = 0
	}
	q.mu.Unlock()
	return c, true, false
}

// oversized reports whether the queue holds more than queueKeep slots, so
// its taker knows to trim it once it has stayed empty.
func (q *ptyQueue) oversized() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.buf) > queueKeep
}

// trim lets an empty queue's array go when it is larger than queueKeep. The
// next push starts a new one at queueKeep slots.
func (q *ptyQueue) trim() {
	q.mu.Lock()
	if q.n == 0 && len(q.buf) > queueKeep {
		q.buf, q.head = nil, 0
	}
	q.mu.Unlock()
}

// len is how many chunks are queued.
func (q *ptyQueue) len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.n
}

// close ends the queue. What is queued can still be taken; nothing more is
// queued.
func (q *ptyQueue) close() {
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
	q.signal()
}

func (q *ptyQueue) signal() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}
