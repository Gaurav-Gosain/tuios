package app

import (
	"sync/atomic"
	"time"
)

type frameWrittenClock struct {
	at atomic.Int64
}

var frameClockBase = time.Now()

func (c *frameWrittenClock) note() {
	c.at.Store(int64(max(time.Since(frameClockBase), 1)))
}

func (c *frameWrittenClock) time() time.Time {
	n := c.at.Load()
	if n == 0 {
		return time.Time{}
	}
	return frameClockBase.Add(time.Duration(n))
}

func (m *OS) motionBudgetFrom() time.Time {
	from := m.lastInteractionRender
	if w := m.frameWritten.time(); w.After(from) {
		from = w
	}
	return from
}
