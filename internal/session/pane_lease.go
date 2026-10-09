package session

import "time"

// leaseInterval is the least time between two resizes leases make on one
// pane.
const leaseInterval = 100 * time.Millisecond

// A per-pane size lease.
//
// A session has one size, and under window_size = smallest a phone that
// attached would shrink every pane on the desktop to fit a 45-column screen.
// stream-pane does not attach, so it never changes the session size, and by
// itself it does not change the pane either: the phone shows the pane at the
// pane's own size.
//
// A lease is the one thing that does. While a stream holds one, that pane's
// PTY is held at most at the leased size, and every resize the session's
// clients ask for is clamped to it. The size they ask for is still recorded
// (PTY.askedW, askedH), so when the last lease on the pane ends, the pane
// takes that size back. Nothing else changes: not the session size, not any
// other pane. A desktop client draws the pane's smaller grid inside the same
// rectangle.
//
// A lease never makes a pane larger than the clients asked for. Each
// dimension is the smaller of the two, so a lease larger than the pane is a
// no-op, and a pane with two leases takes the smallest of each dimension.
//
// A lease resizes the pane at most once every leaseInterval. A lease frame
// costs the client nine bytes and no reply, and every resize it makes signals
// the program in the pane, logs a line, records a resize mark that only the
// pane's output ages out, and is broadcast to every client drawing the pane.
// A client that sent them in a loop resized a desktop pane hundreds of times
// a second. Leases that come faster are recorded at once and applied together
// when the interval ends, so the pane always ends at the newest of them.

// paneLease is one holder's leased size.
type paneLease struct {
	width, height int
}

// leasedSizeLocked is the size the pane takes when its clients ask for width
// by height: each dimension clamped to the smallest lease held. streamMu is
// held.
func (p *PTY) leasedSizeLocked(width, height int) (int, int) {
	for _, l := range p.leases {
		if width > 0 && l.width < width {
			width = l.width
		}
		if height > 0 && l.height < height {
			height = l.height
		}
	}
	return width, height
}

// SetLease holds the pane at most at width by height for holder, or with a
// zero size releases holder's lease. The pane is resized to what the leases
// now allow, or back to the size its clients last asked for: at once, or
// when leaseInterval has passed since the last resize a lease made.
func (p *PTY) SetLease(holder string, width, height int) error {
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
		p.leases[holder] = paneLease{width: width, height: height}
	}
	if wait := leaseInterval - time.Since(p.leaseAt); wait > 0 {
		if p.leaseTimer == nil {
			p.leaseTimer = time.AfterFunc(wait, p.applyLeases)
		}
		return nil
	}
	return p.applyLeasesLocked()
}

// applyLeases is the resize SetLease put off.
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

// applyLeasesLocked resizes the pane to the size its clients asked for,
// clamped to the leases held now. streamMu is held.
func (p *PTY) applyLeasesLocked() error {
	p.leaseAt = time.Now()
	askedW, askedH := p.askedW, p.askedH
	if askedW <= 0 || askedH <= 0 {
		// No client has asked for a size since the pane was made, so the
		// size it was made at is the one asked for.
		p.terminalMu.RLock()
		askedW, askedH = p.width, p.height
		p.terminalMu.RUnlock()
		p.askedW, p.askedH = askedW, askedH
	}
	w, h := p.leasedSizeLocked(askedW, askedH)
	return p.resizeStreamLocked(w, h)
}
