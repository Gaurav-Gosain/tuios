package session

import (
	"bytes"
	"encoding/gob"
	"hash/fnv"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
)

const (
	listingPushGap    = 3 * time.Second
	listingPushSettle = 100 * time.Millisecond
)

func (d *Daemon) listingNotifier() {
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()
	armed := false
	var armedAt time.Time
	var last uint64
	var lastLook time.Time
	var buf bytes.Buffer

	for {
		select {
		case <-d.ctx.Done():
			return
		case <-d.wakes.listing.C():
			if armed || !d.wakes.listing.Up() {
				continue
			}
			at := time.Now().Add(listingPushSettle)
			if next := lastLook.Add(listingPushGap); next.After(at) {
				at = next
			}
			armed, armedAt = true, at
			timer.Reset(time.Until(at))
		case <-d.wakes.listingNow.C():
			if !d.wakes.listingNow.Take() {
				continue
			}
			at := time.Now().Add(listingPushSettle)
			if armed && !armedAt.After(at) {
				continue
			}
			armed, armedAt = true, at
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
			if err := gob.NewEncoder(&buf).Encode(listingForPush(d.listSessions())); err != nil {
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

func listingForPush(sessions []SessionInfo) []SessionInfo {
	for i := range sessions {
		for j := range sessions[i].Windows {
			w := &sessions[i].Windows[j]
			w.Title = harness.TrimTitleMarks(w.Title)
		}
	}
	return sessions
}

func listingReader(cs *connState) bool {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return cs.isTUIClient && cs.attached && cs.listingPush
}

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
