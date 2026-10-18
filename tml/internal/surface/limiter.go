package surface

import (
	"sync"
	"time"
)

// Rates a host applies per surface or per pane.
const (
	PatchesPerSecond = 100             // patches per surface
	ToastInterval    = 2 * time.Second // one toast per pane per interval
	SurfacesPerPane  = 8               // open surfaces per pane
)

// Limiter is a token bucket: it allows Rate events a second, with bursts up
// to Burst. It is safe for use by several goroutines.
type Limiter struct {
	Rate  float64
	Burst float64

	mu     sync.Mutex
	tokens float64
	last   time.Time
	primed bool
}

// NewLimiter returns a Limiter. NewLimiter(PatchesPerSecond, PatchesPerSecond)
// is the patch limit of one surface. NewLimiter(1/ToastInterval.Seconds(), 1)
// is the toast limit of one pane.
func NewLimiter(rate, burst float64) *Limiter {
	return &Limiter{Rate: rate, Burst: burst}
}

// Allow reports whether one event may happen at now, and takes a token if so.
func (l *Limiter) Allow(now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.primed {
		l.tokens, l.last, l.primed = l.Burst, now, true
	}
	if dt := now.Sub(l.last).Seconds(); dt > 0 {
		l.tokens = min(l.Burst, l.tokens+dt*l.Rate)
		l.last = now
	}
	if l.tokens < 1 {
		return false
	}
	l.tokens--
	return true
}
