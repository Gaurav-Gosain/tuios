package session

import (
	"sync"
	"time"
)

type pruneTimer struct {
	mu sync.Mutex
	t  *time.Timer
	at int64
}

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

func (p *pruneTimer) due() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.at
}

func (p *pruneTimer) stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.t != nil {
		p.t.Stop()
		p.t, p.at = nil, 0
	}
}
