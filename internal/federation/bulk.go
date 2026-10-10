package federation

import (
	"encoding/binary"
	"sync"
	"time"
)

// Bulk streams: file bytes that share a link with people's typing.
//
// A link is one ssh pipe, and every pane on the machine rides it. Before bulk
// streams, a stream wrote frames of up to a megabyte under one write lock, in
// arrival order. A copy of a large file filled the pipe with megabyte frames,
// and every keystroke and every echo on that machine waited behind them: at
// 10 Mbit/s one frame is 0.8 s, and ssh's own channel window keeps two more
// megabytes in flight behind it.
//
// Three things together keep a transfer out of the way:
//
//   - A bulk stream writes frames of at most bulkFramePayload, so a frame
//     from another stream waits for at most one of them on this side.
//   - The write lock has two lanes. A writer from an ordinary stream goes
//     ahead of every bulk writer that is waiting, so the order on the pipe is
//     "interactive first", and bulk writers take turns among themselves.
//   - A bulk stream has a window: the sender has at most window bytes that
//     the far reader has not yet read. The reader says how far it has read in
//     credit frames. Without a window the bytes a scheduler holds back on this
//     side still sit in ssh's buffers and the kernel's, which the scheduler
//     cannot reorder, and the latency comes back. The window follows the link:
//     twice the bytes the link carries in its shortest round trip, so the
//     queue the transfer builds is about one round trip long, whatever the
//     link's speed.
//
// Compatibility. Only a stream opened with StreamOpen.Bulk is a bulk stream,
// and only a peer that understands credit frames opens one. The side that
// accepts a bulk stream answers with a credit of zero before anything else,
// which tells the opener the accepter takes part. An opener that never hears
// it, from a proxy that predates bulk streams, sends no credit frame (a peer
// that predates them would read one as a protocol error and drop the link) and
// writes without a window.

const (
	// bulkFramePayload bounds one data frame of a bulk stream. It is how long
	// a keystroke can wait behind a transfer on this side of the pipe: 64 KiB
	// is 50 ms at 10 Mbit/s and nothing on a local network.
	bulkFramePayload = 64 << 10

	// bulkWindowMin and bulkWindowMax bound a bulk stream's window. The
	// floor keeps a fast link from being starved by a timer's resolution;
	// the ceiling is the receive buffer, so a reader that falls behind costs
	// its own stream and never the link's shared read loop.
	bulkWindowMin = 256 << 10
	bulkWindowMax = 16 << 20

	// bulkBufferFrames is a bulk stream's receive buffer in frames. A peer
	// that keeps to the window never fills it: the buffer holds the whole
	// window in frames, so a reader that falls behind costs its own stream
	// and never the link's shared read loop. The assertion below keeps the
	// two in step if either constant changes.
	bulkBufferFrames = bulkWindowMax / bulkFramePayload

	// bulkCreditEvery is how many bytes a reader takes before it says so.
	bulkCreditEvery = 64 << 10

	// bulkFilterSpan is how long a round trip or a delivery rate stays the
	// best one the window is sized from. A link that slows down is followed
	// within this span.
	bulkFilterSpan = 10 * time.Second
)

// The receive buffer must hold at least the whole window, or a writer that
// keeps to the window could still block the shared read loop.
const _ = uint(bulkBufferFrames*bulkFramePayload - bulkWindowMax)

// prioLock is the mux's write lock with two lanes. An urgent writer goes ahead
// of every bulk writer that is waiting; bulk writers queue behind any urgent
// one. Writers in one lane are not ordered.
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

// window is the sending half of a bulk stream's flow control.
type window struct {
	mu   sync.Mutex
	cond *sync.Cond
	// on is set once the far side said it takes part. Until then the
	// stream writes without a window.
	on       bool
	closed   bool
	sent     uint64
	credited uint64
	size     uint64

	// marks are the ends of the chunks in flight: where each ended, when it
	// was written and how much had been credited then. A credit past a mark
	// gives one round trip and one delivery rate.
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

// enable turns the window on: the far side takes part.
func (w *window) enable() {
	w.mu.Lock()
	w.on = true
	w.mu.Unlock()
	w.cond.Broadcast()
}

// close wakes any writer waiting for room, for good.
func (w *window) close() {
	w.mu.Lock()
	w.closed = true
	w.mu.Unlock()
	w.cond.Broadcast()
}

// reserve waits until n more bytes fit in the window, then counts them as
// sent. It returns false when the stream ended while it waited.
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
	if w.on {
		w.marks = append(w.marks, mark{end: w.sent, at: time.Now(), credited: w.credited})
	}
	return true
}

// credit takes the far reader's word that it has read upTo bytes, and sizes
// the window from what that says about the link.
func (w *window) credit(upTo uint64) {
	now := time.Now()
	w.mu.Lock()
	if upTo > w.sent {
		// The far side cannot have read what was never sent. A peer that
		// says so is wrong, and believing it would open the window wide.
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

// Size is the window now, for tests and the transfer's own report.
func (w *window) Size() uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.size
}

// encodeCredit is a credit frame's payload: how many bytes the reader has
// read, in all.
func encodeCredit(n uint64) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], n)
	return b[:]
}

// decodeCredit reads a credit payload. Anything that is not eight bytes is no
// credit at all.
func decodeCredit(p []byte) (uint64, bool) {
	if len(p) != 8 {
		return 0, false
	}
	return binary.BigEndian.Uint64(p), true
}
