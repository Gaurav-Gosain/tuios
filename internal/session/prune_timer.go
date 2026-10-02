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

// stop cancels a pending prune, for a session that is stopping.
func (p *pruneTimer) stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.t != nil {
		p.t.Stop()
		p.t, p.at = nil, 0
	}
}
