package session

import (
	"bytes"
	"encoding/gob"
	"hash/fnv"
	"time"
)

// The session listing push.
//
// A client draws other sessions on its rail from the session listing. It used
// to ask for the listing every 3 seconds while the rail was open, and every 30
// while it was hidden, whether anything had changed or not. Each ask woke the
// daemon and the client.
//
// Now the daemon tells a client when the listing changed, and the client asks
// then. Everything the listing shows changes through an event the daemon
// already sees: output, input, a change to a session's state, a title, a
// pane's folder or branch, a client arriving or leaving, a session made or
// ended. Each raises the listing latch (idle_wakes.go). The notifier then
// builds the listing, and sends MsgSessionsChanged when it differs from the
// last one it sent. With nothing happening it arms no timer and sends nothing.
//
// The push is never more frequent than the poll was: one every
// listingPushGap at most, the first listingPushSettle after a quiet spell.

const (
	// listingPushGap is the shortest gap between two looks at the listing.
	listingPushGap = 3 * time.Second
	// listingPushSettle is how long a change waits for the ones that come with
	// it, so a burst is looked at once.
	listingPushSettle = 100 * time.Millisecond
)

// listingNotifier looks at the listing after the listing latch goes up, and
// tells the clients that read MsgSessionsChanged when it changed.
func (d *Daemon) listingNotifier() {
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()
	armed := false
	var last uint64
	var lastLook time.Time
	var buf bytes.Buffer

	for {
		select {
		case <-d.ctx.Done():
			return
		case <-d.wakes.listing.C():
			// The token can outlive the flag: a look took the flag before
			// this token was read.
			if armed || !d.wakes.listing.Up() {
				continue
			}
			at := time.Now().Add(listingPushSettle)
			if next := lastLook.Add(listingPushGap); next.After(at) {
				at = next
			}
			armed = true
			timer.Reset(time.Until(at))
		case <-timer.C:
			armed = false
			d.wakes.listingTicks.Add(1)
			d.wakes.listing.Take()
			lastLook = time.Now()
			if !d.hasListingReaders() {
				continue
			}
			buf.Reset()
			if err := gob.NewEncoder(&buf).Encode(d.listSessions()); err != nil {
				continue
			}
			h := fnv.New64a()
			_, _ = h.Write(buf.Bytes())
			if sum := h.Sum64(); sum != last {
				last = sum
				d.broadcastSessionsChanged()
			}
		}
	}
}

// listingReader reports whether cs reads MsgSessionsChanged. A client that
// has not had its attach reply yet reads its next message as that reply, so
// a push must not come first; broadcastHostsChanged keeps the same rule. The
// client refreshes its listing once it is attached.
func listingReader(cs *connState) bool {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return cs.isTUIClient && cs.attached && cs.listingPush
}

// hasListingReaders reports whether any client reads MsgSessionsChanged.
func (d *Daemon) hasListingReaders() bool {
	d.clientsMu.RLock()
	defer d.clientsMu.RUnlock()
	for _, cs := range d.clients {
		if listingReader(cs) {
			return true
		}
	}
	return false
}

// broadcastSessionsChanged sends MsgSessionsChanged to every client that
// reads it.
func (d *Daemon) broadcastSessionsChanged() {
	msg, err := NewMessage(MsgSessionsChanged, nil)
	if err != nil {
		return
	}
	d.clientsMu.RLock()
	defer d.clientsMu.RUnlock()
	for _, cs := range d.clients {
		if listingReader(cs) {
			d.queueBroadcast(cs, msg, "broadcastSessionsChanged")
		}
	}
}
