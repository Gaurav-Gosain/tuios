package session

import "time"

const leaseInterval = 100 * time.Millisecond

type paneLease struct {
	width, height int
	fit           bool
}

func (p *PTY) leasedSizeLocked(width, height int) (int, int) {
	detached := p.noClients.Load()
	if detached {
		fw, fh := 0, 0
		for _, l := range p.leases {
			if l.fit {
				fw, fh = max(fw, l.width), max(fh, l.height)
			}
		}
		if fw > 0 && fh > 0 {
			width, height = fw, fh
		}
	}
	for _, l := range p.leases {
		if l.fit && detached {
			continue
		}
		if width > 0 && l.width < width {
			width = l.width
		}
		if height > 0 && l.height < height {
			height = l.height
		}
	}
	return width, height
}

func (p *PTY) setNoClients(none bool) {
	p.streamMu.Lock()
	defer p.streamMu.Unlock()
	if p.noClients.Swap(none) == none {
		return
	}
	fit := false
	for _, l := range p.leases {
		fit = fit || l.fit
	}
	if !fit || p.ctx.Err() != nil {
		return
	}
	if err := p.applyLeasesLocked(); err != nil {
		LogBasic("PTY %s: a viewer lease could not resize the pane: %v", shortID(p.ID), err)
	}
}

// SetLease holds the pane at most at width by height for holder, or with a
// zero size releases holder's lease. The pane is resized to what the leases
// now allow, or back to the size its clients last asked for: at once, or
// when leaseInterval has passed since the last resize a lease made.
func (p *PTY) SetLease(holder string, width, height int) error {
	return p.SetViewerLease(holder, width, height, false)
}

// SetViewerLease is SetLease with fit: a fit lease sizes the pane while no
// client shows the session. See paneLease.
func (p *PTY) SetViewerLease(holder string, width, height int, fit bool) error {
	p.streamMu.Lock()
	defer p.streamMu.Unlock()
	if width <= 0 || height <= 0 {
		if _, ok := p.leases[holder]; !ok {
			return nil
		}
		delete(p.leases, holder)
	} else {
		if p.leases == nil {
			p.leases = make(map[string]paneLease)
		}
		p.leases[holder] = paneLease{width: width, height: height, fit: fit}
	}
	if wait := leaseInterval - time.Since(p.leaseAt); wait > 0 {
		if p.leaseTimer == nil {
			p.leaseTimer = time.AfterFunc(wait, p.applyLeases)
		}
		return nil
	}
	return p.applyLeasesLocked()
}

func (p *PTY) applyLeases() {
	p.streamMu.Lock()
	defer p.streamMu.Unlock()
	p.leaseTimer = nil
	if p.ctx.Err() != nil {
		return
	}
	if err := p.applyLeasesLocked(); err != nil {
		LogBasic("PTY %s: a lease could not resize the pane: %v", shortID(p.ID), err)
	}
}

func (p *PTY) applyLeasesLocked() error {
	p.leaseAt = time.Now()
	askedW, askedH := p.askedW, p.askedH
	if askedW <= 0 || askedH <= 0 {
		p.terminalMu.RLock()
		askedW, askedH = p.width, p.height
		p.terminalMu.RUnlock()
		p.askedW, p.askedH = askedW, askedH
	}
	w, h := p.leasedSizeLocked(askedW, askedH)
	return p.resizeStreamLocked(w, h)
}
