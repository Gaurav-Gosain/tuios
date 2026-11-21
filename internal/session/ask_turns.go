package session

import (
	"context"
	"sync"
	"time"
)

type askLine struct {
	held    bool
	waiters []chan struct{}
}

type askTurns struct {
	mu    sync.Mutex
	lines map[string]*askLine
}

func (t *askTurns) take(ctx context.Context, gone <-chan struct{}, window string, timeout time.Duration) (ahead int, release func()) {
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
	case <-gone:
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	select {
	case <-ch:
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

func (t *askTurns) busy(window string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	l := t.lines[window]
	return l != nil && l.held
}
