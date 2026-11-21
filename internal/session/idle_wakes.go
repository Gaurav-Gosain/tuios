package session

import (
	"sync/atomic"
	"time"
)

type idleWakes struct {
	detect wakeLatch

	stall wakeLatch

	silent wakeLatch

	cleanup wakeLatch

	listing    wakeLatch
	listingNow wakeLatch

	detectTicks  atomic.Int64
	stallTicks   atomic.Int64
	cleanupTicks atomic.Int64
	listingTicks atomic.Int64
	silentTicks  atomic.Int64
}

func (w *idleWakes) init() {
	w.detect.init()
	w.stall.init()
	w.silent.init()
	w.cleanup.init()
	w.listing.init()
	w.listingNow.init()
}

func (w *idleWakes) sessionsChanged() {
	w.listing.Set()
	w.listingNow.Set()
}

func (w *idleWakes) activity() {
	w.detect.Set()
	w.cleanup.Set()
	w.listing.Set()
}

func (w *idleWakes) stateChanged() {
	w.detect.Set()
	w.stall.Set()
	w.silent.Set()
	w.cleanup.Set()
	w.listing.Set()
}

func (s *Session) setIdleWakes(w *idleWakes) { s.wakes.Store(w) }

func (s *Session) noteActivity() {
	if w := s.wakes.Load(); w != nil {
		w.activity()
	}
}

func (s *Session) noteListingChange() {
	if w := s.wakes.Load(); w != nil {
		w.listing.Set()
	}
}

func (s *Session) noteIdleStateChange() {
	if w := s.wakes.Load(); w != nil {
		w.stateChanged()
	}
}

func (s *Session) detectPending() bool {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	for _, c := range s.agentClaims {
		if c.misses > 0 {
			return true
		}
	}
	return false
}

func (s *Session) nextStallAt(stall time.Duration, lastOutput func(ptyID string) int64) (int64, bool) {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	var next int64
	found := false
	for i := range s.state.Windows {
		w := &s.state.Windows[i]
		if w.AgentState != AgentStateWorking {
			continue
		}
		at := w.AgentStateAt
		if w.PTYID != "" {
			at = max(at, lastOutput(w.PTYID))
		}
		at += int64(stall)
		if !found || at < next {
			next, found = at, true
		}
	}
	return next, found
}
