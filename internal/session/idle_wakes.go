package session

import "sync/atomic"

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

	// detectTicks counts agentMonitor's timer fires.
	detectTicks atomic.Int64
}

func (w *idleWakes) init() {
	w.detect.init()
}

// activity is a pane printing or taking input.
func (w *idleWakes) activity() {
	w.detect.Set()
}

// stateChanged is a change to a session's state: a window opened or closed,
// an agent state set.
func (w *idleWakes) stateChanged() {
	w.detect.Set()
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
