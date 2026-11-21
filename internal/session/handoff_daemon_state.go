package session

import (
	"maps"
	"slices"
	"time"
)

type handoffEvents struct {
	BootID            string        `json:"boot_id"`
	Seq               uint64        `json:"seq"`
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
	LastTMLSurfaceSeq uint64        `json:"last_tml_surface_seq,omitempty"`
}

type handoffMailbox struct {
	Msgs              []*AgentMessage   `json:"msgs,omitempty"`
	Bytes             int               `json:"bytes,omitempty"`
	Evicted           uint64            `json:"evicted,omitempty"`
	EvictedUnread     map[string]uint64 `json:"evicted_unread,omitempty"`
	EvictedUnreadGone uint64            `json:"evicted_unread_gone,omitempty"`
}

type handoffNotice struct {
	ID         uint64    `json:"id,omitempty"`
	Session    string    `json:"session,omitempty"`
	Lease      uint64    `json:"lease,omitempty"`
	LeaseTo    uint64    `json:"lease_to,omitempty"`
	LeaseUntil time.Time `json:"lease_until,omitzero"`
}

type handoffMail struct {
	NextID   uint64                    `json:"next_id"`
	Boxes    map[string]handoffMailbox `json:"boxes,omitempty"`
	Noticed  map[string]handoffNotice  `json:"noticed,omitempty"`
	LeaseSeq uint64                    `json:"lease_seq,omitempty"`
}

func (h *eventHub) handoffState() handoffEvents {
	h.mu.Lock()
	defer h.mu.Unlock()
	ev := handoffEvents{
		BootID: h.bootID, Seq: h.seq, EvictedSeq: h.evictedSeq,
		LastOutputSeq: h.lastOutputSeq, LastActivitySeq: h.lastActivitySeq,
		LastTranscriptSeq: h.lastTranscriptSeq, LastProgressSeq: h.lastProgressSeq,
		LastFileSeq: h.lastFileSeq, LastLayoutSeq: h.lastLayoutSeq,
		LastTMLSeq: h.lastTMLSeq, LastTMLSurfaceSeq: h.lastTMLSurfaceSeq,
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
	h.lastTMLSeq, h.lastTMLSurfaceSeq = ev.LastTMLSeq, ev.LastTMLSurfaceSeq
	ring := ev.Ring
	if len(ring) > len(h.ring) {
		h.evictedSeq = ring[len(ring)-len(h.ring)-1].Seq
		ring = ring[len(ring)-len(h.ring):]
	}
	h.ringStart, h.ringLen = 0, copy(h.ring, ring)
	for i := range h.ring[:h.ringLen] {
		h.ring[i].relayed = slices.Contains(ev.RelayedSeqs, h.ring[i].Seq)
	}
}

func (b *agentBus) handoffState() handoffMail {
	b.mu.Lock()
	defer b.mu.Unlock()
	m := handoffMail{NextID: b.nextID, LeaseSeq: b.leaseSeq, Boxes: make(map[string]handoffMailbox, len(b.boxes))}
	for name, mb := range b.boxes {
		box := handoffMailbox{Bytes: mb.bytes, Evicted: mb.evicted, EvictedUnread: maps.Clone(mb.evictedUnread), EvictedUnreadGone: mb.evictedUnreadGone}
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

func (b *agentBus) restore(m handoffMail) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextID, b.leaseSeq = m.NextID, m.LeaseSeq
	for name, box := range m.Boxes {
		b.boxes[name] = &agentMailbox{msgs: box.Msgs, bytes: box.Bytes, evicted: box.Evicted, evictedUnread: box.EvictedUnread, evictedUnreadGone: box.EvictedUnreadGone, buckets: map[string]*sendBucket{}}
	}
	for inbox, n := range m.Noticed {
		if b.noticed == nil {
			b.noticed = map[string]noticeMark{}
		}
		b.noticed[inbox] = noticeMark{id: n.ID, session: n.Session, lease: n.Lease, leaseTo: n.LeaseTo, leaseUntil: n.LeaseUntil}
	}
}
