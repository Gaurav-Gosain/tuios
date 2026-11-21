package federation

import (
	"encoding/binary"
	"sync"
	"time"
)

const (
	bulkFramePayload = 64 << 10

	bulkWindowMin = 256 << 10
	bulkWindowMax = 16 << 20

	bulkBufferFrames = bulkWindowMax / bulkFramePayload

	bulkCreditEvery = 64 << 10

	bulkMarkEvery = 16 << 10

	bulkFilterSpan = 10 * time.Second
)

const _ = uint(bulkBufferFrames*bulkFramePayload - bulkWindowMax)

type prioLock struct {
	mu     sync.Mutex
	cond   *sync.Cond
	busy   bool
	urgent int
}

func newPrioLock() *prioLock {
	l := &prioLock{}
	l.cond = sync.NewCond(&l.mu)
	return l
}

func (l *prioLock) lock(urgent bool) {
	l.mu.Lock()
	if urgent {
		l.urgent++
		for l.busy {
			l.cond.Wait()
		}
		l.urgent--
	} else {
		for l.busy || l.urgent > 0 {
			l.cond.Wait()
		}
	}
	l.busy = true
	l.mu.Unlock()
}

func (l *prioLock) unlock() {
	l.mu.Lock()
	l.busy = false
	l.mu.Unlock()
	l.cond.Broadcast()
}

type window struct {
	mu       sync.Mutex
	cond     *sync.Cond
	on       bool
	closed   bool
	sent     uint64
	credited uint64
	size     uint64

	marks []mark

	minRTT  time.Duration
	minAt   time.Time
	maxRate float64
	rateAt  time.Time
	samples int
}

type mark struct {
	end      uint64
	at       time.Time
	credited uint64
}

func newWindow() *window {
	w := &window{size: bulkWindowMin}
	w.cond = sync.NewCond(&w.mu)
	return w
}

func (w *window) enable() {
	w.mu.Lock()
	w.on = true
	w.mu.Unlock()
	w.cond.Broadcast()
}

func (w *window) close() {
	w.mu.Lock()
	w.closed = true
	w.mu.Unlock()
	w.cond.Broadcast()
}

func (w *window) reserve(n int) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	for w.on && !w.closed && w.sent-w.credited+uint64(n) > w.size && w.sent > w.credited {
		w.cond.Wait()
	}
	if w.closed {
		return false
	}
	w.sent += uint64(n)
	if w.on && (len(w.marks) == 0 || w.sent-w.marks[len(w.marks)-1].end >= bulkMarkEvery) {
		w.marks = append(w.marks, mark{end: w.sent, at: time.Now(), credited: w.credited})
	}
	return true
}

func (w *window) credit(upTo uint64) {
	now := time.Now()
	w.mu.Lock()
	if upTo > w.sent {
		upTo = w.sent
	}
	if upTo > w.credited {
		w.credited = upTo
	}
	var last *mark
	i := 0
	for ; i < len(w.marks) && w.marks[i].end <= w.credited; i++ {
		last = &w.marks[i]
	}
	if last != nil {
		rtt := now.Sub(last.at)
		if rtt <= 0 {
			rtt = time.Microsecond
		}
		rate := float64(w.credited-last.credited) / rtt.Seconds()
		if w.minRTT == 0 || rtt < w.minRTT || now.Sub(w.minAt) > bulkFilterSpan {
			w.minRTT, w.minAt = rtt, now
		}
		if rate > w.maxRate || now.Sub(w.rateAt) > bulkFilterSpan {
			w.maxRate, w.rateAt = rate, now
		}
		w.samples++
		bdp := w.maxRate * w.minRTT.Seconds()
		w.size = uint64(min(max(2*bdp, bulkWindowMin), bulkWindowMax))
	}
	w.marks = w.marks[i:]
	w.mu.Unlock()
	w.cond.Broadcast()
}

func (w *window) Size() uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.size
}

func encodeCredit(n uint64) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], n)
	return b[:]
}

func decodeCredit(p []byte) (uint64, bool) {
	if len(p) != 8 {
		return 0, false
	}
	return binary.BigEndian.Uint64(p), true
}
