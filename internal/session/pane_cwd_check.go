package session

import (
	"sync"
	"time"
)

const cwdCheckInterval = time.Second

type paneCwdCheck struct {
	mu       sync.Mutex
	at       time.Time
	trailing bool
	seen     string
}

func (s *Session) noteCwdOnOutput(p *PTY) {
	if p == nil {
		return
	}
	if p.place.announced.Load() && p.place.Elsewhere() == "" {
		if _, remote := p.pty.(*remotePane); !remote {
			return
		}
	}
	c := &p.cwdCheck
	c.mu.Lock()
	wait := cwdCheckInterval - time.Since(c.at)
	if wait > 0 {
		if !c.trailing {
			c.trailing = true
			time.AfterFunc(wait, func() {
				c.mu.Lock()
				c.trailing = false
				c.at = time.Now()
				c.mu.Unlock()
				s.checkPaneCwd(p)
			})
		}
		c.mu.Unlock()
		return
	}
	c.at = time.Now()
	c.mu.Unlock()
	s.checkPaneCwd(p)
}

func (s *Session) checkPaneCwd(p *PTY) {
	if rp, ok := p.pty.(*remotePane); ok {
		rp.askCwd()
		return
	}
	if p.IsExited() {
		return
	}
	if p.place.Elsewhere() != "" && shellAtPrompt(p) && p.place.setElsewhere("") {
		s.publishLiveFactsSoon()
	}
	if p.place.announced.Load() {
		return
	}
	cwd, ok := p.ProcessCwd()
	if !ok || cwd == "" {
		return
	}
	c := &p.cwdCheck
	c.mu.Lock()
	if c.seen == "" {
		c.seen = p.place.Cwd()
	}
	moved := c.seen != cwd
	c.seen = cwd
	c.mu.Unlock()
	if moved {
		s.publishLiveFactsSoon()
	}
}

const liveFactsPushGap = 100 * time.Millisecond

var liveFactsEpoch = time.Now()

func (s *Session) publishLiveFactsSoon() {
	if !s.liveFactsPushQueued.CompareAndSwap(false, true) {
		return
	}
	go func() {
		if last := s.liveFactsPushedAt.Load(); last != 0 {
			if wait := liveFactsPushGap - (time.Since(liveFactsEpoch) - time.Duration(last)); wait > 0 {
				time.Sleep(wait)
			}
		}
		s.liveFactsPushQueued.Store(false)
		s.liveFactsPushedAt.Store(int64(max(time.Since(liveFactsEpoch), 1)))
		s.PublishLiveFacts()
	}()
}
