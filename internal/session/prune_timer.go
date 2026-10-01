package session

import (
	"sync"
	"time"
)

// pruneTimer is a one-shot that runs a prune by a deadline. A deadline later
// than the one pending changes nothing, and a sooner one moves the timer. It
// is armed only while there is something to prune, so an idle session holds
// no timer. The zero value is ready to use.
type pruneTimer struct {
	mu sync.Mutex
	t  *time.Timer
	at int64 // unix nanoseconds, 0 when nothing is pending
}

// arm makes sure run runs by at, unix nanoseconds. 0 arms nothing.
func (p *pruneTimer) arm(at int64, run func()) {
	if at == 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.t != nil && p.at != 0 && p.at <= at {
		return
	}
	if p.t != nil {
		p.t.Stop()
	}
	p.at = at
	p.t = time.AfterFunc(max(time.Until(time.Unix(0, at)), 0), func() {
		p.mu.Lock()
		p.t, p.at = nil, 0
		p.mu.Unlock()
		run()
	})
}

// due is the pending deadline, 0 for none.
func (p *pruneTimer) due() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.at
}

// stop cancels a pending prune, for a session that is stopping.
func (p *pruneTimer) stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.t != nil {
		p.t.Stop()
		p.t, p.at = nil, 0
	}
}
