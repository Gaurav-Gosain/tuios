package session

import (
	"sync/atomic"
	"time"
)

// idleWakes holds the latches the daemon's background loops sleep on while
// nothing happens, and counts how often each loop's timer fired.
//
// A loop that has work keeps its own cadence. A loop that finds nothing to do
// stops its timer and blocks on its latch, and the latch is raised by the event
// that can give it work again: a pane printing, a pane taking input, or a
// change to a session's state. So a daemon whose panes sit at a prompt arms no
// timer at all. See docs/perf.md, "2026-10 the daemon at idle".
type idleWakes struct {
	// detect wakes agentMonitor. A program can only start or stop in a pane
	// that takes input, prints, or was just made.
	detect wakeLatch

	// stall wakes stallMonitor to work out its next deadline: a pane can only
	// start working through a change to its session's state.
	stall wakeLatch

	// cleanup wakes cleanupLoop. What it releases is left behind by output,
	// input and resizes, and what it expires is a routed request.
	cleanup wakeLatch

	// listing wakes listingNotifier: something a client's session listing
	// shows may have changed. See daemon_listing_push.go.
	listing wakeLatch
	// listingNow asks listingNotifier to look at the listing without waiting
	// out listingPushGap: a session was made or ended, which a script that
	// runs tuios kill-session or tuios new wants to see on the rail at once
	// (issue #1225). See sessionsChanged.
	listingNow wakeLatch

	// detectTicks, stallTicks, cleanupTicks and listingTicks count the timer
	// fires of agentMonitor, stallMonitor, cleanupLoop and listingNotifier.
	detectTicks  atomic.Int64
	stallTicks   atomic.Int64
	cleanupTicks atomic.Int64
	listingTicks atomic.Int64
}

func (w *idleWakes) init() {
	w.detect.init()
	w.stall.init()
	w.cleanup.init()
	w.listing.init()
	w.listingNow.init()
}

// sessionsChanged is a session made or ended. The listing push goes out after
// listingPushSettle, not after listingPushGap.
func (w *idleWakes) sessionsChanged() {
	w.listing.Set()
	w.listingNow.Set()
}

// activity is a pane printing or taking input.
func (w *idleWakes) activity() {
	w.detect.Set()
	w.cleanup.Set()
	w.listing.Set()
}

// stateChanged is a change to a session's state: a window opened or closed,
// an agent state set.
func (w *idleWakes) stateChanged() {
	w.detect.Set()
	w.stall.Set()
	w.cleanup.Set()
	w.listing.Set()
}

// setIdleWakes points the session at the daemon's latches. The daemon calls it
// when it adopts the session. A session with none raises nothing.
func (s *Session) setIdleWakes(w *idleWakes) { s.wakes.Store(w) }

// noteActivity raises the latches for a pane of this session printing or
// taking input. It is on the output path, so it costs an atomic load and, while
// the latches are up, one more per latch.
func (s *Session) noteActivity() {
	if w := s.wakes.Load(); w != nil {
		w.activity()
	}
}

// noteListingChange raises the listing latch for a change to what this
// session's listing shows outside its state: a title, a folder, a branch, the
// time it was last active.
func (s *Session) noteListingChange() {
	if w := s.wakes.Load(); w != nil {
		w.listing.Set()
	}
}

// noteIdleStateChange raises the latches for a change to this session's state.
func (s *Session) noteIdleStateChange() {
	if w := s.wakes.Load(); w != nil {
		w.stateChanged()
	}
}

// detectPending reports whether agentMonitor has a reason to look at this
// session again with nothing new happening: an agent it owns is out of the
// foreground and its misses are being counted, which takes consecutive looks.
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

// nextStallAt is when the earliest working pane of this session goes quiet
// for stall, in unix nanoseconds, and false when no pane is working. It is the
// moment applyStallHeuristic would first find the pane stalled: see stalledAt.
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
