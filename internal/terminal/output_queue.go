package terminal

import (
	"sync"
	"time"
)

const outputQueueSlots = 4096

const outputQueueKeep = 16

const outputQueueTrimAfter = time.Second

type outputQueue struct {
	mu     sync.Mutex
	buf    []outputChunk
	head   int
	n      int
	closed bool
	wakeCh chan struct{}
}

func newOutputQueue() *outputQueue {
	return &outputQueue{wakeCh: make(chan struct{}, 1)}
}

func (q *outputQueue) wake() <-chan struct{} {
	return q.wakeCh
}

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

func (q *outputQueue) grow() {
	next := make([]outputChunk, max(outputQueueKeep, 2*len(q.buf)))
	if q.n > 0 {
		k := copy(next, q.buf[q.head:])
		copy(next[k:], q.buf[:q.head])
	}
	q.buf, q.head = next, 0
}

func (q *outputQueue) pop() (c outputChunk, ok, closed bool) {
	q.mu.Lock()
	if q.n == 0 {
		closed = q.closed
		q.mu.Unlock()
		return outputChunk{}, false, closed
	}
	c = q.buf[q.head]
	q.buf[q.head] = outputChunk{}
	q.head = (q.head + 1) & (len(q.buf) - 1)
	q.n--
	if q.n == 0 {
		q.head = 0
	}
	q.mu.Unlock()
	return c, true, false
}

func (q *outputQueue) oversized() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.buf) > outputQueueKeep
}

func (q *outputQueue) trim() {
	q.mu.Lock()
	if q.n == 0 && len(q.buf) > outputQueueKeep {
		q.buf, q.head = nil, 0
	}
	q.mu.Unlock()
}

func (q *outputQueue) len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.n
}

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
