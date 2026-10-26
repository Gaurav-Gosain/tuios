package terminal

import (
	"sync"
	"time"
)

// outputQueueSlots is the most chunks one pane holds for its emulator.
// maxQueuedBytes bounds the bytes; the slots only bound the count.
const outputQueueSlots = 4096

// outputQueueKeep is the slot count a drained queue keeps. A queue grown past
// it by a backlog lets its array go once it has stayed empty for
// outputQueueTrimAfter, so a pane that flooded once does not hold the peak for
// the rest of its life.
const outputQueueKeep = 16

// outputQueueTrimAfter is how long a queue grown past outputQueueKeep stays
// empty before its writer trims it. Letting the array go at every drain made a
// pane that floods in bursts grow it again from outputQueueKeep at every burst.
const outputQueueTrimAfter = time.Second

// outputQueue is the daemon output a pane has been handed and its writer has
// not yet applied. It replaces a buffered channel of outputQueueSlots chunks:
// that channel was made whole with the pane, 56 bytes a slot and 229 KB per
// pane, before the pane printed anything. This one starts empty, doubles with
// the backlog up to outputQueueSlots, and gives its array back once it has
// stayed drained (trim).
//
// Any number of goroutines push; one goroutine takes. The taker calls pop until
// it reports nothing, and only then waits on wake. A push that makes the queue
// non-empty leaves a token on wake, so a taker that saw it empty always hears
// of the next chunk. A stale token costs the taker one empty pop.
type outputQueue struct {
	mu     sync.Mutex
	buf    []outputChunk // a ring; its length is zero or a power of two
	head   int
	n      int
	closed bool
	// wakeCh holds one token when a chunk may be waiting. Made by
	// newOutputQueue; a queue without one never wakes a taker.
	wakeCh chan struct{}
}

// newOutputQueue is an empty queue.
func newOutputQueue() *outputQueue {
	return &outputQueue{wakeCh: make(chan struct{}, 1)}
}

// wake is the channel the taker waits on after pop found the queue empty.
func (q *outputQueue) wake() <-chan struct{} {
	return q.wakeCh
}

// push adds c behind everything queued. It reports false, and queues nothing,
// when the queue holds outputQueueSlots chunks or is closed.
func (q *outputQueue) push(c outputChunk) bool {
	q.mu.Lock()
	if q.closed || q.n == outputQueueSlots {
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

// grow doubles the ring, from outputQueueKeep for an empty one, and lays its
// chunks out from index zero. Called with mu held and the ring full.
func (q *outputQueue) grow() {
	next := make([]outputChunk, max(outputQueueKeep, 2*len(q.buf)))
	if q.n > 0 {
		k := copy(next, q.buf[q.head:])
		copy(next[k:], q.buf[:q.head])
	}
	q.buf, q.head = next, 0
}

// pop takes the oldest chunk. ok is false when there is none, and closed then
// says no chunk will come again, as a receive from a closed and drained
// channel does.
func (q *outputQueue) pop() (c outputChunk, ok, closed bool) {
	q.mu.Lock()
	if q.n == 0 {
		closed = q.closed
		q.mu.Unlock()
		return outputChunk{}, false, closed
	}
	c = q.buf[q.head]
	// The slot would otherwise keep the chunk's bytes alive.
	q.buf[q.head] = outputChunk{}
	q.head = (q.head + 1) & (len(q.buf) - 1)
	q.n--
	if q.n == 0 {
		q.head = 0
	}
	q.mu.Unlock()
	return c, true, false
}

// oversized reports whether the queue holds more than outputQueueKeep slots,
// so its writer knows to trim it once it has stayed empty.
func (q *outputQueue) oversized() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.buf) > outputQueueKeep
}

// trim lets an empty queue's array go when it is larger than outputQueueKeep.
// The next push starts a new one at outputQueueKeep slots.
func (q *outputQueue) trim() {
	q.mu.Lock()
	if q.n == 0 && len(q.buf) > outputQueueKeep {
		q.buf, q.head = nil, 0
	}
	q.mu.Unlock()
}

// len is how many chunks are queued.
func (q *outputQueue) len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.n
}

// close ends the queue. What is queued can still be taken; nothing more is
// queued.
func (q *outputQueue) close() {
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
	q.signal()
}

func (q *outputQueue) signal() {
	select {
	case q.wakeCh <- struct{}{}:
	default:
	}
}
