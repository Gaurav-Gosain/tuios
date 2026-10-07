package session

import (
	"time"
)

// Bounding the memory the daemon gives to what clients send.
//
// The binary protocol frames each message with a length the sender writes, up
// to maxFrameBytes. Any process that can reach a socket can send a header, so
// the daemon holds four limits against it:
//
//   - A frame body is read into memory that grows as its bytes arrive, never
//     allocated whole from the length in its header. A sender that announces
//     16 MiB and sends 1 KiB costs about 4 KiB. See readFramePayload.
//   - Each message type has the largest frame it can need (daemonFrameLimit).
//     A frame over its limit is skipped unread.
//   - Past largeFrame, the memory a frame takes is charged to readBudget, one
//     budget the whole daemon shares. A frame that cannot get its share
//     within readBudgetWait is skipped unread and refused, so many large
//     frames at once wait or fail and never grow memory past the budget.
//   - A frame body must arrive within frameBodyDeadline, so a sender that
//     stalls cannot hold its share of the budget.
//
// And the daemon serves at most maxConnections connections at once, so idle
// connections cannot grow without end either. See admitConnection.
//
// The budget has the shape of the JSON verb line budget in verb_lines.go
// (chunks of 64 KiB in a channel, the same total and the same wait), so the
// two can share one: whichever of them lands second points its budget at
// readBudget.

const (
	// largeFrame is the payload size past which a frame is charged to the
	// budget. Smaller frames are what a client sends for every key and
	// resize, and the connection cap bounds what they can hold.
	largeFrame = 64 << 10

	// firstFrameChunk is the most memory a frame body is given before any of
	// it has arrived.
	firstFrameChunk = 4 << 10

	// frameGrowStep is the most a frame's buffer grows by at once. Up to it,
	// the buffer doubles, so a large frame is copied a few times and not
	// once per chunk.
	frameGrowStep = 4 << 20

	// frameCopies is how many times its size a large frame takes in memory,
	// counted against the budget: the payload, and what the gob decoder
	// copies out of it.
	frameCopies = 2

	readBudgetBytes = 64 << 20
	readBudgetChunk = 64 << 10
	readBudgetWait  = 2 * time.Second

	// frameBodyDeadline is how long the daemon waits for the rest of a frame
	// once its length has arrived. 16 MiB in this time is about 560 KiB/s.
	frameBodyDeadline = 30 * time.Second

	// maxConnections is the most connections the daemon serves at once, over
	// its socket and its link sockets together. One TUI client takes a few;
	// a command takes one for as long as it runs. An idle connection costs
	// about 30 KB, so this many cost about 30 MB.
	maxConnections = 1024

	// connReadBuffer is the read buffer each connection holds for its life.
	// Small frames, which are most of them, are read through it; a larger
	// read goes past it to the connection.
	connReadBuffer = 4 << 10
)

// memBudget is memory the daemon shares among the reads that need a lot of
// it, in chunks of readBudgetChunk bytes.
type memBudget chan struct{}

func newMemBudget() memBudget { return make(memBudget, readBudgetBytes/readBudgetChunk) }

// acquire takes n bytes of the budget, rounded up to a chunk, waiting until
// deadline. It returns the chunks it took, or 0 and false when the budget did
// not free in time, having given back what it took.
func (b memBudget) acquire(n int, deadline time.Time) (int, bool) {
	chunks := (n + readBudgetChunk - 1) / readBudgetChunk
	if chunks == 0 {
		return 0, true
	}
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	for i := range chunks {
		select {
		case b <- struct{}{}:
		case <-timer.C:
			b.release(i)
			return 0, false
		}
	}
	return chunks, true
}

// release gives back chunks.
func (b memBudget) release(chunks int) {
	for range chunks {
		<-b
	}
}

// readBudget is the daemon's budget for large reads.
func (d *Daemon) readBudget() memBudget {
	d.readBudgetOnce.Do(func() { d.readBudgetCh = newMemBudget() })
	return d.readBudgetCh
}
