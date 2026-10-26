package session

import (
	"slices"
	"time"
)

// What the daemon holds only in memory, carried across a live handoff so the
// processes that kept running see one daemon: the key every pane's
// TUIOS_PANE_TOKEN is signed with, the event stream's boot id, numbering and
// replay ring, and the agents' mailboxes.

// handoffEvents is an eventHub's state.
type handoffEvents struct {
	BootID string `json:"boot_id"`
	Seq    uint64 `json:"seq"`
	// Ring is the replay ring, oldest first. RelayedSeqs are the seqs of
	// its events relayed from a linked host: the flag is unexported, so the
	// ring's JSON leaves it out.
	Ring              []streamEvent `json:"ring,omitempty"`
	RelayedSeqs       []uint64      `json:"relayed_seqs,omitempty"`
	EvictedSeq        uint64        `json:"evicted_seq,omitempty"`
	LastOutputSeq     uint64        `json:"last_output_seq,omitempty"`
	LastActivitySeq   uint64        `json:"last_activity_seq,omitempty"`
	LastTranscriptSeq uint64        `json:"last_transcript_seq,omitempty"`
	LastProgressSeq   uint64        `json:"last_progress_seq,omitempty"`
	LastFileSeq       uint64        `json:"last_file_seq,omitempty"`
	LastLayoutSeq     uint64        `json:"last_layout_seq,omitempty"`
	LastTMLSeq        uint64        `json:"last_tml_seq,omitempty"`
}

// handoffMailbox is one session's agent mailbox.
type handoffMailbox struct {
	Msgs    []*AgentMessage `json:"msgs,omitempty"`
	Bytes   int             `json:"bytes,omitempty"`
	Evicted uint64          `json:"evicted,omitempty"`
}

// handoffNotice is one pane's unread mail notice mark, with its lease, so
// the new image neither announces the mail again nor refuses the commit of a
// hook that took its notice from the old one.
type handoffNotice struct {
	ID         uint64    `json:"id,omitempty"`
	Session    string    `json:"session,omitempty"`
	Lease      uint64    `json:"lease,omitempty"`
	LeaseTo    uint64    `json:"lease_to,omitempty"`
	LeaseUntil time.Time `json:"lease_until,omitzero"`
}

// handoffMail is the agentBus's state. The send buckets and the open asks
// are not carried: an ask in flight ends with the old image's connections.
type handoffMail struct {
	NextID   uint64                    `json:"next_id"`
	Boxes    map[string]handoffMailbox `json:"boxes,omitempty"`
	Noticed  map[string]handoffNotice  `json:"noticed,omitempty"`
	LeaseSeq uint64                    `json:"lease_seq,omitempty"`
}

// handoffState is the hub's state.
func (h *eventHub) handoffState() handoffEvents {
	h.mu.Lock()
	defer h.mu.Unlock()
	ev := handoffEvents{
		BootID: h.bootID, Seq: h.seq, EvictedSeq: h.evictedSeq,
		LastOutputSeq: h.lastOutputSeq, LastActivitySeq: h.lastActivitySeq,
		LastTranscriptSeq: h.lastTranscriptSeq, LastProgressSeq: h.lastProgressSeq,
		LastFileSeq: h.lastFileSeq, LastLayoutSeq: h.lastLayoutSeq,
		LastTMLSeq: h.lastTMLSeq,
	}
	for i := range h.ringLen {
		e := h.ring[(h.ringStart+i)%len(h.ring)]
		ev.Ring = append(ev.Ring, e)
		if e.relayed {
			ev.RelayedSeqs = append(ev.RelayedSeqs, e.Seq)
		}
	}
	return ev
}

// restore makes a hub with no subscribers continue the carried one: the same
// boot id, and numbering and replay that go on from where it stopped.
func (h *eventHub) restore(ev handoffEvents) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if ev.BootID != "" {
		h.bootID = ev.BootID
	}
	h.seq, h.evictedSeq = ev.Seq, ev.EvictedSeq
	h.lastOutputSeq, h.lastActivitySeq = ev.LastOutputSeq, ev.LastActivitySeq
	h.lastTranscriptSeq, h.lastProgressSeq = ev.LastTranscriptSeq, ev.LastProgressSeq
	h.lastFileSeq, h.lastLayoutSeq = ev.LastFileSeq, ev.LastLayoutSeq
	h.lastTMLSeq = ev.LastTMLSeq
	ring := ev.Ring
	if len(ring) > len(h.ring) {
		// A smaller ring than the old image's: the oldest go, as they would
		// have there.
		h.evictedSeq = ring[len(ring)-len(h.ring)-1].Seq
		ring = ring[len(ring)-len(h.ring):]
	}
	h.ringStart, h.ringLen = 0, copy(h.ring, ring)
	for i := range h.ring[:h.ringLen] {
		h.ring[i].relayed = slices.Contains(ev.RelayedSeqs, h.ring[i].Seq)
	}
}

// handoffState is the bus's mailboxes.
func (b *agentBus) handoffState() handoffMail {
	b.mu.Lock()
	defer b.mu.Unlock()
	m := handoffMail{NextID: b.nextID, LeaseSeq: b.leaseSeq, Boxes: make(map[string]handoffMailbox, len(b.boxes))}
	for name, mb := range b.boxes {
		box := handoffMailbox{Bytes: mb.bytes, Evicted: mb.evicted}
		for _, msg := range mb.msgs {
			c := *msg
			c.Attachments = slices.Clone(msg.Attachments)
			box.Msgs = append(box.Msgs, &c)
		}
		m.Boxes[name] = box
	}
	if len(b.noticed) > 0 {
		m.Noticed = make(map[string]handoffNotice, len(b.noticed))
		for inbox, n := range b.noticed {
			m.Noticed[inbox] = handoffNotice{ID: n.id, Session: n.session, Lease: n.lease, LeaseTo: n.leaseTo, LeaseUntil: n.leaseUntil}
		}
	}
	return m
}

// restore puts carried mailboxes into an empty bus.
func (b *agentBus) restore(m handoffMail) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextID, b.leaseSeq = m.NextID, m.LeaseSeq
	for name, box := range m.Boxes {
		b.boxes[name] = &agentMailbox{msgs: box.Msgs, bytes: box.Bytes, evicted: box.Evicted, buckets: map[string]*sendBucket{}}
	}
	for inbox, n := range m.Noticed {
		if b.noticed == nil {
			b.noticed = map[string]noticeMark{}
		}
		b.noticed[inbox] = noticeMark{id: n.ID, session: n.Session, lease: n.Lease, leaseTo: n.LeaseTo, leaseUntil: n.LeaseUntil}
	}
}
