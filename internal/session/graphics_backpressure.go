package session

import (
	"time"
)

// A terminal that cannot keep up with a program stops reading from it, and the
// program waits in write() until the terminal catches up. The daemon reads a
// pane for several clients at once, so it cannot simply stop: the slowest
// client would then set the pace for all of them, and a client that stopped
// reading would freeze the program for everyone.
//
// A pane that streams kitty graphics is paced to its fastest client instead.
// Each client that is behind skips frames (kitty_frames.go), so no client's
// queue grows. Before each read of such a pane, the daemon waits while every
// client that paces the pane is behind. The pane's buffer fills and the
// program waits in write() until one client catches up. The fastest client
// then gets every frame, and the program runs at that client's rate.
//
// A wait ends after maxGraphicsHold. The pane is then read freely until some
// client catches up, so a client that takes nothing at all costs the program
// one maxGraphicsHold, not one per read. Read-only viewers and clients on other
// machines never hold the pane. Every pane that does not stream graphics is
// read as before.
//
// A pane counts as streaming graphics for graphicsStreamWindow after it last
// wrote a kitty graphics command other than a query. Many programs send one
// query when they start, to learn whether the terminal draws images, and a
// text flood after it is read like any text.

var (
	// graphicsStreamWindow is how long a pane counts as streaming graphics
	// after its last kitty graphics command.
	graphicsStreamWindow = 5 * time.Second
	// maxGraphicsHold bounds one wait for a client to catch up.
	maxGraphicsHold = 250 * time.Millisecond
	// graphicsHoldPoll is how often a held pane checks its clients again
	// when no stream goroutine has woken it.
	graphicsHoldPoll = 5 * time.Millisecond
)

// streamsGraphics reports whether the pane wrote a kitty graphics command
// within graphicsStreamWindow.
func (p *PTY) streamsGraphics() bool {
	at := p.graphicsAt.Load()
	return at != 0 && time.Since(time.Unix(0, at)) < graphicsStreamWindow
}

// behind reports whether a client has not taken what the pane already gave
// it: a whole frame, half its byte bound or half its slots.
func (sub *ptySubscriber) behind() bool {
	return sub.gapped.Load() ||
		sub.framesWaiting.Load() > 0 ||
		sub.queued.Load() > maxSubscriberQueue/2 ||
		len(sub.ch) > cap(sub.ch)/2
}

// everyClientBehind reports whether at least one client paces the pane and
// every such client is behind.
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

// wakePacer tells a held pane to check its clients again.
func (p *PTY) wakePacer() {
	if !p.holding.Load() {
		return
	}
	select {
	case p.paceWake <- struct{}{}:
	default:
	}
}

// holdForSlowSubscribers waits, before the pane is read again, while a pane
// that streams graphics has every client behind. It returns at once for any
// other pane. It takes no lock across the wait, so subscribing, unsubscribing
// and resizing go on while a pane is held. Only readOutput calls it.
func (p *PTY) holdForSlowSubscribers() {
	if !p.streamsGraphics() || !p.everyClientBehind() {
		p.holdSpent = false
		return
	}
	if p.holdSpent {
		// This wait already ran out. Read on until a client catches up.
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

// pacingReportEvery is how often a pane that held its program, skipped frames
// or cut a client's stream says so in the daemon log.
const pacingReportEvery = 2 * time.Second

// reportPacing writes one log line for what pacing did since the last line, at
// most once every pacingReportEvery. Only readOutput calls it.
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

// pacingCounts is what reportPacing last reported.
type pacingCounts struct {
	holds, ranOut, skipped, cut int64
	held                        time.Duration
}
