package app

import (
	"sync/atomic"
	"time"
)

// frameWrittenClock holds when the renderer last finished writing a frame. The
// renderer's goroutine sets it and the Update goroutine reads it.
type frameWrittenClock struct {
	// at is nanoseconds since frameClockBase, 0 for never. An offset from a
	// time.Now keeps the monotonic clock, so a wall clock change cannot move
	// the budget.
	at atomic.Int64
}

var frameClockBase = time.Now()

// note records that a frame was written now.
func (c *frameWrittenClock) note() {
	c.at.Store(int64(max(time.Since(frameClockBase), 1)))
}

// time is when a frame was last written, zero for never.
func (c *frameWrittenClock) time() time.Time {
	n := c.at.Load()
	if n == 0 {
		return time.Time{}
	}
	return frameClockBase.Add(time.Duration(n))
}

// motionBudgetFrom is when the motion frame budget (see the motion clause in
// update.go) last started: the later of the end of the last compose and the
// end of the renderer's write of a frame.
//
// A frame costs the event loop more than its compose. Bubble Tea writes a
// frame from its own goroutine, under a lock, and the loop takes the same lock
// to store the view of every message it handles. While a large frame is
// parsed, diffed and written, the loop stops, and motion queues up behind it.
// On a 400x100 screen on a loaded machine the write takes as long as the
// compose. Counted from the end of the compose alone, the budget had often run
// out by the time the loop came back, so the first queued event composed the
// next frame although it was already stale. The loop then took one or two
// events per compose and write, the queue grew for as long as the pointer
// moved, and hover trailed the pointer by seconds (issue #1058). Counted from
// the end of the write, the queued events drain without a frame first.
//
// A client whose writer does not report its writes (see FrameWriter) counts
// from the compose alone, as before.
func (m *OS) motionBudgetFrom() time.Time {
	from := m.lastInteractionRender
	if w := m.frameWritten.time(); w.After(from) {
		from = w
	}
	return from
}
