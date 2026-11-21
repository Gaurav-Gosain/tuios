package session

import (
	"sync"
	"time"
)

const subscriberSlots = 4096

const queueKeep = 16

const queueTrimAfter = time.Second

type ptyQueue struct {
	mu     sync.Mutex
	buf    []ptyChunk
	head   int
	n      int
	closed bool
	wake   chan struct{}
}

func newSubscriber() *ptySubscriber {
	sub := &ptySubscriber{}
	sub.out.wake = make(chan struct{}, 1)
	return sub
}

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

func (q *ptyQueue) grow() {
	next := make([]ptyChunk, max(queueKeep, 2*len(q.buf)))
	if q.n > 0 {
		k := copy(next, q.buf[q.head:])
		copy(next[k:], q.buf[:q.head])
	}
	q.buf, q.head = next, 0
}

func (q *ptyQueue) pop() (c ptyChunk, ok, closed bool) {
	q.mu.Lock()
	if q.n == 0 {
		closed = q.closed
		q.mu.Unlock()
		return ptyChunk{}, false, closed
	}
	c = q.buf[q.head]
	q.buf[q.head] = ptyChunk{}
	q.head = (q.head + 1) & (len(q.buf) - 1)
	q.n--
	if q.n == 0 {
		q.head = 0
	}
	q.mu.Unlock()
	return c, true, false
}

func (q *ptyQueue) oversized() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.buf) > queueKeep
}

func (q *ptyQueue) trim() {
	q.mu.Lock()
	if q.n == 0 && len(q.buf) > queueKeep {
		q.buf, q.head = nil, 0
	}
	q.mu.Unlock()
}

func (q *ptyQueue) len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.n
}

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
