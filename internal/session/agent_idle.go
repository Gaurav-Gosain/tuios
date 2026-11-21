package session

import (
	"sync"
	"time"
)

const (
	idleConfirmInterval = 100 * time.Millisecond
	idleConfirmCount    = 3
	idleConfirmCap      = 700 * time.Millisecond
	agentStartupGrace   = 3 * time.Second
)

type idlePending struct {
	started       time.Time
	last          time.Time
	confirmations int
}

type harnessSeen struct {
	harness string
	at      time.Time
}

type idleGate struct {
	mu      sync.Mutex
	pending map[string]*idlePending
	seen    map[string]harnessSeen
	timers  map[string]*time.Timer
}

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

func (g *idleGate) cancel(windowID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.pending, windowID)
	if t := g.timers[windowID]; t != nil {
		t.Stop()
		delete(g.timers, windowID)
	}
}

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

func (g *idleGate) stop() {
	g.mu.Lock()
	defer g.mu.Unlock()
	for id, t := range g.timers {
		t.Stop()
		delete(g.timers, id)
	}
	clear(g.pending)
}
