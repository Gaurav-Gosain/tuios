package session

import (
	"time"
)

var (
	graphicsStreamWindow = 5 * time.Second
	maxGraphicsHold      = 250 * time.Millisecond
	graphicsHoldPoll     = 5 * time.Millisecond
)

func (p *PTY) streamsGraphics() bool {
	at := p.graphicsAt.Load()
	return at != 0 && time.Since(time.Unix(0, at)) < graphicsStreamWindow
}

func (sub *ptySubscriber) behind() bool {
	return sub.gapped.Load() ||
		sub.framesWaiting.Load() > 0 ||
		sub.queued.Load() > maxSubscriberQueue/2 ||
		sub.out.len() > subscriberSlots/2
}

func (p *PTY) everyClientBehind() bool {
	p.subscribersMu.RLock()
	defer p.subscribersMu.RUnlock()
	pacers := 0
	for _, sub := range p.subscribers {
		if sub.noPace.Load() {
			continue
		}
		if !sub.behind() {
			return false
		}
		pacers++
	}
	return pacers > 0
}

// SetPacing says whether a client may hold this pane while it is behind. A
// read-only viewer and a client on another machine may not.
func (p *PTY) SetPacing(clientID string, paces bool) {
	p.subscribersMu.RLock()
	defer p.subscribersMu.RUnlock()
	if sub := p.subscribers[clientID]; sub != nil {
		sub.noPace.Store(!paces)
	}
	p.wakePacer()
}

func (p *PTY) wakePacer() {
	if !p.holding.Load() {
		return
	}
	select {
	case p.paceWake <- struct{}{}:
	default:
	}
}

func (p *PTY) holdForSlowSubscribers() {
	if !p.streamsGraphics() || !p.everyClientBehind() {
		p.holdSpent = false
		return
	}
	if p.holdSpent {
		return
	}
	p.holding.Store(true)
	defer p.holding.Store(false)
	select {
	case <-p.paceWake:
	default:
	}
	start := time.Now()
	defer func() {
		p.holds.Add(1)
		p.heldNanos.Add(int64(time.Since(start)))
	}()
	limit := time.NewTimer(maxGraphicsHold)
	defer limit.Stop()
	tick := time.NewTicker(graphicsHoldPoll)
	defer tick.Stop()
	for p.everyClientBehind() {
		select {
		case <-p.ctx.Done():
			return
		case <-limit.C:
			p.holdSpent = true
			p.holdsRanOut.Add(1)
			debugLog("[DEBUG] PTY %s: no client caught up in %s, reading on", p.ID[:8], maxGraphicsHold)
			return
		case <-p.paceWake:
		case <-tick.C:
		}
	}
}

const pacingReportEvery = 2 * time.Second

func (p *PTY) reportPacing() {
	if time.Since(p.pacingReportAt) < pacingReportEvery {
		return
	}
	holds, held := p.holds.Load(), time.Duration(p.heldNanos.Load())
	ranOut, skipped, cut := p.holdsRanOut.Load(), p.framesSkipped.Load(), p.streamsCut.Load()
	last := p.pacingReported
	if holds == last.holds && skipped == last.skipped && cut == last.cut {
		return
	}
	p.pacingReportAt = time.Now()
	p.pacingReported = pacingCounts{holds: holds, held: held, ranOut: ranOut, skipped: skipped, cut: cut}
	LogBasic("PTY %s output pacing: held %d times for %s, %d ran out, %d frames skipped, %d streams cut",
		shortID(p.ID), holds-last.holds, (held - last.held).Round(time.Millisecond),
		ranOut-last.ranOut, skipped-last.skipped, cut-last.cut)
}

type pacingCounts struct {
	holds, ranOut, skipped, cut int64
	held                        time.Duration
}
