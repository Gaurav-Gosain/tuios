package session

import (
	"context"
	"sync"
	"time"
)

// Turns for ask-agent: one ask types at an agent at a time, and the others
// wait in line, first come first served.
//
// An ask types a question and then reads what the pane prints until the agent
// has dealt with it. Two asks at one agent at once would type their questions
// over each other, and each would read the other's answer as its own. Before
// turns, the second ask also waited only ready_timeout (30 seconds) for the
// first ask's turn to end, and failed with not_ready when the agent took
// longer, which pushed agents back to queue-prompt.
//
// A turn is held from before the ask waits for the agent to be at rest until
// the ask has its answer. A waiting ask holds no turn and types nothing. The
// queue's delivery (agent_queue.go) also leaves a pane alone while an ask holds
// its turn, so a queued prompt does not land inside an ask's answer.
//
// Turns are memory only and per window id. Nothing is kept for a pane nobody
// is asking.

// askLine is one pane's line: whether a turn is held, and who waits.
type askLine struct {
	held    bool
	waiters []chan struct{}
}

// askTurns holds every pane's line. The zero value is ready.
type askTurns struct {
	mu    sync.Mutex
	lines map[string]*askLine
}

// take waits for window's turn, for at most timeout or until ctx ends. It
// returns how many asks were ahead when it came, and a release func, or nil
// when the wait ended without a turn. Every turn taken must be released once.
func (t *askTurns) take(ctx context.Context, window string, timeout time.Duration) (ahead int, release func()) {
	t.mu.Lock()
	if t.lines == nil {
		t.lines = map[string]*askLine{}
	}
	l := t.lines[window]
	if l == nil {
		l = &askLine{}
		t.lines[window] = l
	}
	if !l.held {
		l.held = true
		t.mu.Unlock()
		return 0, t.releaser(window)
	}
	ahead = len(l.waiters) + 1
	ch := make(chan struct{})
	l.waiters = append(l.waiters, ch)
	t.mu.Unlock()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ch:
		return ahead, t.releaser(window)
	case <-timer.C:
	case <-ctx.Done():
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	select {
	case <-ch:
		// The turn came as the wait ended. It is passed on, since nobody
		// will use it.
		t.passLocked(window, l)
		return ahead, nil
	default:
	}
	for i, w := range l.waiters {
		if w == ch {
			l.waiters = append(l.waiters[:i], l.waiters[i+1:]...)
			break
		}
	}
	return ahead, nil
}

// releaser is the func that ends a turn, safe to call more than once.
func (t *askTurns) releaser(window string) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			t.mu.Lock()
			defer t.mu.Unlock()
			if l := t.lines[window]; l != nil {
				t.passLocked(window, l)
			}
		})
	}
}

// passLocked gives the turn to the next ask in line, or ends it when nobody
// waits. It holds mu.
func (t *askTurns) passLocked(window string, l *askLine) {
	if len(l.waiters) > 0 {
		next := l.waiters[0]
		l.waiters = l.waiters[1:]
		close(next)
		return
	}
	l.held = false
	delete(t.lines, window)
}

// busy reports whether an ask holds window's turn or waits for it.
func (t *askTurns) busy(window string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.lines[window] != nil
}

// waiting is how many asks wait for window's turn, not counting the one that
// holds it.
func (t *askTurns) waiting(window string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	if l := t.lines[window]; l != nil {
		return len(l.waiters)
	}
	return 0
}
