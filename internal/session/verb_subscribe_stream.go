//go:build !slim

package session

import (
	"encoding/json"
	"time"
)

// verbSubscribe opens a long-lived event stream on this connection. It registers
// a hub subscription with the requested filter, returns a subscribed ack (with
// the current sequence baseline and the boot id), and hands the subscription to
// the dispatch loop which starts the streamer after the ack is written. Only a
// connection that issued this verb ever receives events.
//
// An omitted session means every session: the filter matches on session only
// when one is named. That differs from most verbs, where an omitted session
// means the most recently active one.
//
// With after_seq the stream first replays the retained events after that seq,
// preceded by a gap marker when the replay cannot be exact (see replayLocked).
func (d *Daemon) verbSubscribe(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string   `json:"session"`
		Window  string   `json:"window"`
		Types   []string `json:"types"`
		Queue   int      `json:"queue"`
		// AfterSeq is a pointer so after_seq 0 ("everything since this daemon
		// started") is distinguishable from not resuming at all.
		AfterSeq *uint64 `json:"after_seq"`
		BootID   string  `json:"boot_id"`
		// Hosts adds the events this daemon relays from its linked hosts.
		Hosts bool `json:"hosts"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.BootID != "" && p.AfterSeq == nil {
		return nil, invalidParam("boot_id", "boot_id only means something with after_seq: pass the seq the stream last delivered under that boot id")
	}

	cs.mu.Lock()
	if cs.streaming {
		cs.mu.Unlock()
		return nil, hintedVerbError(ErrVerbInvalidRequest, "connection is already subscribed", &VerbHint{
			Verb:   "unsubscribe",
			Detail: "One event stream per connection. Call unsubscribe first, or open a second connection for the second filter.",
		})
	}
	cs.mu.Unlock()

	filter := eventFilter{session: p.Session, window: p.Window, hosts: p.Hosts}
	// A live session is followed through a rename. A name no session has yet
	// stays a plain name, so a subscriber can wait for it to be made.
	if p.Session != "" {
		if live := d.manager.GetSession(p.Session); live != nil {
			filter.sess = live
		}
	}
	if len(p.Types) > 0 {
		filter.types = make(map[string]bool, len(p.Types))
		for _, t := range p.Types {
			filter.types[t] = true
		}
	}

	var from *resumePoint
	if p.AfterSeq != nil {
		from = &resumePoint{afterSeq: *p.AfterSeq, bootID: p.BootID}
	}
	sub, baseline, err := d.events.subscribeFrom(filter, p.Queue, from)
	if err != nil {
		return nil, hintedVerbError(ErrVerbInvalidParams, err.Error(), &VerbHint{
			Param:  "after_seq",
			Detail: "Pass the seq of the last event the stream delivered. A seq this daemon has not reached yet cannot have been delivered under this boot id.",
		})
	}

	cs.mu.Lock()
	cs.eventSub = sub
	cs.pendingStream = sub
	cs.streaming = true
	cs.mu.Unlock()

	ack := map[string]any{"type": EventSubscribed, "seq": baseline, "boot_id": d.events.bootIdentity()}
	if from != nil {
		replayed := 0
		for _, ev := range sub.preface {
			if ev.Type != EventGap && d.eventInScope(cs, ev) {
				replayed++
			}
		}
		ack["replayed"] = replayed
	}
	return ack, nil
}

// verbUnsubscribe closes this connection's event stream. The streamer observes
// the stop signal, clears the connection's stream state, and unsubscribes from
// the hub.
func (d *Daemon) verbUnsubscribe(cs *connState, _ json.RawMessage) (any, *verbError) {
	cs.mu.Lock()
	sub := cs.eventSub
	cs.mu.Unlock()
	if sub == nil {
		return nil, hintedVerbError(ErrVerbInvalidRequest, "connection is not subscribed", &VerbHint{
			Verb:   "subscribe",
			Detail: "There is no event stream on this connection to close.",
		})
	}
	// Signal the streamer to exit; it clears cs.eventSub/streaming and removes the
	// hub subscription on its way out.
	sub.close()
	return map[string]any{"type": "unsubscribed"}, nil
}

// startPendingStream launches the event streamer for a subscription handed over
// by the subscribe handler, once its ack has been written. It is a no-op for
// every other verb.
func (d *Daemon) startPendingStream(cs *connState) {
	cs.mu.Lock()
	sub := cs.pendingStream
	cs.pendingStream = nil
	cs.mu.Unlock()
	if sub == nil {
		return
	}
	// A daemon that is stopping refuses the streamer. Its connection is
	// closing, so the subscription only has to be released.
	if !d.goTracked(func() { d.streamEvents(cs, sub) }) {
		d.events.unsubscribe(sub)
	}
}

// streamEvents pushes events from a subscription to the connection until the
// connection closes, the daemon shuts down, or the subscription is stopped. A
// full subscriber queue drops events at publish time; the streamer surfaces those
// drops as a gap marker written just before the next surviving event, so a slow
// subscriber never blocks the daemon (the daemon_stream.go discipline).
func (d *Daemon) streamEvents(cs *connState, sub *eventSub) {
	defer d.events.unsubscribe(sub)
	defer func() {
		cs.mu.Lock()
		if cs.eventSub == sub {
			cs.eventSub = nil
			cs.streaming = false
		}
		cs.mu.Unlock()
	}()

	// A resumed subscription first writes what it missed. Live events that
	// arrive meanwhile wait in sub.ch, and all of them are newer than anything
	// in the preface.
	for _, ev := range sub.preface {
		if !d.eventInScope(cs, ev) {
			continue
		}
		if err := d.writeEventLine(cs, ev); err != nil {
			cs.drop()
			return
		}
	}
	sub.preface = nil

	for {
		select {
		case <-cs.done:
			return
		case <-d.ctx.Done():
			return
		case <-sub.stop:
			return
		case ev := <-sub.ch:
			// A restricted connection's stream carries only the sessions it
			// reaches. The check runs here, on the streamer's goroutine, and
			// not in the hub's filter, because the hub publishes with a
			// session's state lock held and the check reads session state.
			if !d.eventInScope(cs, ev) {
				continue
			}
			if dropped := sub.dropped.Swap(0); dropped > 0 {
				if err := d.writeEventLine(cs, streamEvent{Type: EventGap, Dropped: dropped, Reason: GapOverflow, BootID: d.events.bootIdentity()}); err != nil {
					cs.drop()
					return
				}
			}
			if err := d.writeEventLine(cs, ev); err != nil {
				cs.drop()
				return
			}
		}
	}
}

// writeEventLine serializes ev as one newline-terminated JSON line and writes it
// under the connection's send mutex with a write deadline.
func (d *Daemon) writeEventLine(cs *connState, ev streamEvent) error {
	data, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	cs.sendMu.Lock()
	defer cs.sendMu.Unlock()
	_ = cs.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, werr := cs.conn.Write(data)
	return werr
}
