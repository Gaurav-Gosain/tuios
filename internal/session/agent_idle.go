package session

import (
	"sync"
	"time"
)

// Positive idle evidence, held until it has been seen enough times to trust.
//
// A title or screen rule that says idle is reading a prompt box or a rest
// glyph, and those are on screen in the gaps of a turn too: Claude Code keeps
// its prompt box under the spinner the whole time it works, and a harness that
// redraws can show a frame with no spinner for a moment. Publishing the first
// such frame flaps the pane from working to idle and back, and every flap is a
// finished turn to anything counting them.
//
// So a pane that is working moves to idle only when the idle reading holds on
// idleConfirmCount further looks spaced at least idleConfirmInterval apart, or
// when it has held for idleConfirmCap, whichever comes first. The gate
// schedules those looks itself, because a pane at rest emits nothing that
// would trigger one. Any look that reads something other than idle cancels the
// wait. A pane that was not working is at no risk of a flap, so its idle is
// published at once.
//
// A harness that has only just been seen in a pane is also given
// agentStartupGrace before any idle reading counts: a starting TUI paints its
// frame in pieces, and a half-painted frame can look like an empty prompt box.
// These are herdr's numbers.
const (
	idleConfirmInterval = 100 * time.Millisecond
	idleConfirmCount    = 3
	idleConfirmCap      = 700 * time.Millisecond
	agentStartupGrace   = 3 * time.Second
)

// idlePending is one window's idle reading waiting to be confirmed.
type idlePending struct {
	started       time.Time
	last          time.Time
	confirmations int
}

// harnessSeen is when the idle gate first saw a harness in a window, the start
// of its startup grace.
type harnessSeen struct {
	harness string
	at      time.Time
}

// idleGate holds the per-window confirmation state. It has its own lock, like
// agentHolds, because it is consulted around ApplyAgentReport, which takes
// stateMu itself.
type idleGate struct {
	mu      sync.Mutex
	pending map[string]*idlePending
	seen    map[string]harnessSeen
	timers  map[string]*time.Timer
}

// noteHarnessSeen records the first look at harness in a window, which starts
// its startup grace. A new harness in the same window starts a new grace.
func (g *idleGate) noteHarnessSeen(windowID, harness string, now time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.seen == nil {
		g.seen = make(map[string]harnessSeen)
	}
	if h, ok := g.seen[windowID]; ok && h.harness == harness {
		return
	}
	g.seen[windowID] = harnessSeen{harness: harness, at: now}
	delete(g.pending, windowID)
}

// admit decides whether an idle reading on a window may be published now.
// current is the window's state. When it may not, recheck is how long until
// another look is worth taking, or 0 when none is.
func (g *idleGate) admit(windowID string, current AgentState, now time.Time) (publish bool, recheck time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if h, ok := g.seen[windowID]; ok {
		if end := h.at.Add(agentStartupGrace); now.Before(end) {
			delete(g.pending, windowID)
			return false, end.Sub(now)
		}
	}
	if current != AgentStateWorking {
		delete(g.pending, windowID)
		return true, 0
	}
	p, ok := g.pending[windowID]
	if !ok {
		if g.pending == nil {
			g.pending = make(map[string]*idlePending)
		}
		g.pending[windowID] = &idlePending{started: now, last: now}
		return false, idleConfirmInterval
	}
	if now.Sub(p.started) >= idleConfirmCap {
		delete(g.pending, windowID)
		return true, 0
	}
	if since := now.Sub(p.last); since >= idleConfirmInterval {
		p.confirmations++
		p.last = now
	}
	if p.confirmations >= idleConfirmCount {
		delete(g.pending, windowID)
		return true, 0
	}
	wait := idleConfirmInterval - now.Sub(p.last)
	if left := idleConfirmCap - now.Sub(p.started); left < wait {
		wait = left
	}
	return false, max(wait, time.Millisecond)
}

// cancel drops a window's pending idle reading and its scheduled look: the
// pane read as something other than idle, so the wait is over.
func (g *idleGate) cancel(windowID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.pending, windowID)
	if t := g.timers[windowID]; t != nil {
		t.Stop()
		delete(g.timers, windowID)
	}
}

// pendingFor reports whether a window has an idle reading waiting.
func (g *idleGate) pendingFor(windowID string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	_, ok := g.pending[windowID]
	return ok
}

// schedule arms the look that confirms or cancels a pending reading, replacing
// any look already armed for the window.
func (g *idleGate) schedule(windowID string, after time.Duration, look func()) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.timers == nil {
		g.timers = make(map[string]*time.Timer)
	}
	if t := g.timers[windowID]; t != nil {
		t.Stop()
	}
	g.timers[windowID] = time.AfterFunc(after, look)
}

// forget drops everything held for a window that has gone away.
func (g *idleGate) forget(windowID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.pending, windowID)
	delete(g.seen, windowID)
	if t := g.timers[windowID]; t != nil {
		t.Stop()
		delete(g.timers, windowID)
	}
}

// stop disarms every scheduled look, for a session being stopped.
func (g *idleGate) stop() {
	g.mu.Lock()
	defer g.mu.Unlock()
	for id, t := range g.timers {
		t.Stop()
		delete(g.timers, id)
	}
	clear(g.pending)
}
