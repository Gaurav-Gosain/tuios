package session

import (
	"bufio"
	"encoding/json"
	"net"
	"testing"
	"time"
)

// TestEventHubTwoSubscribersSameSequence verifies that two subscribers see the
// same monotonic sequence number for each published event.
func TestEventHubTwoSubscribersSameSequence(t *testing.T) {
	h := newEventHub()
	a := h.subscribe(eventFilter{}, 64)
	b := h.subscribe(eventFilter{}, 64)

	const n = 10
	for i := 0; i < n; i++ {
		h.publish(streamEvent{Type: EventOutput})
	}

	for i := 0; i < n; i++ {
		ea := <-a.ch
		eb := <-b.ch
		if ea.Seq != eb.Seq {
			t.Fatalf("event %d: subscriber seqs differ: a=%d b=%d", i, ea.Seq, eb.Seq)
		}
		if ea.Seq != uint64(i+1) {
			t.Fatalf("event %d: seq = %d, want %d", i, ea.Seq, i+1)
		}
	}
}

// TestEventHubDropsWhenQueueFull verifies the slow-subscriber policy: once the
// bounded queue is full, further events are dropped and counted rather than
// blocking the publisher, and the surviving events are the earliest ones.
func TestEventHubDropsWhenQueueFull(t *testing.T) {
	h := newEventHub()
	sub := h.subscribe(eventFilter{}, 4)

	for i := 0; i < 10; i++ {
		h.publish(streamEvent{Type: EventOutput})
	}

	// 4 buffered, 6 dropped.
	if got := sub.dropped.Load(); got != 6 {
		t.Fatalf("dropped = %d, want 6", got)
	}
	first := <-sub.ch
	if first.Seq != 1 {
		t.Fatalf("first surviving event seq = %d, want 1 (earliest kept)", first.Seq)
	}
}

// TestEventHubFilters verifies session/type filtering: a subscriber only receives
// events matching its filter, and non-matching events are neither delivered nor
// counted as drops.
func TestEventHubFilters(t *testing.T) {
	h := newEventHub()
	sub := h.subscribe(eventFilter{session: "work", types: map[string]bool{EventBell: true}}, 16)

	h.publish(streamEvent{Type: EventOutput, Session: "work"}) // wrong type
	h.publish(streamEvent{Type: EventBell, Session: "other"})  // wrong session
	h.publish(streamEvent{Type: EventBell, Session: "work"})   // match

	ev := <-sub.ch
	if ev.Type != EventBell || ev.Session != "work" {
		t.Fatalf("got %+v, want a work/bell event", ev)
	}
	if sub.dropped.Load() != 0 {
		t.Fatalf("filtered-out events must not count as drops, got %d", sub.dropped.Load())
	}
	select {
	case extra := <-sub.ch:
		t.Fatalf("unexpected extra event: %+v", extra)
	default:
	}
}

// TestStreamEventsEmitsGapMarker verifies the streamer surfaces dropped events as
// a gap marker written just before the next surviving event. Events are published
// before the streamer starts (modeling an infinitely slow reader during the
// burst) so drops accumulate deterministically through the real hub.
func TestStreamEventsEmitsGapMarker(t *testing.T) {
	d := NewDaemon(&DaemonConfig{})
	server, client := net.Pipe()
	t.Cleanup(func() { _ = server.Close(); _ = client.Close() })

	cs := &connState{
		conn:             client,
		clientID:         "gap-client",
		done:             make(chan struct{}),
		codec:            DefaultCodec(),
		ptySubscriptions: make(map[string]struct{}),
	}

	sub := d.events.subscribe(eventFilter{}, 2)
	// Burst 5 events with no streamer draining: seq 1,2 buffer; 3,4,5 drop.
	for i := 0; i < 5; i++ {
		d.events.publish(streamEvent{Type: EventOutput})
	}
	if got := sub.dropped.Load(); got != 3 {
		t.Fatalf("dropped = %d, want 3", got)
	}

	go d.streamEvents(cs, sub)
	t.Cleanup(func() { close(cs.done) })

	r := bufio.NewReader(server)
	read := func() streamEvent {
		t.Helper()
		_ = server.SetReadDeadline(time.Now().Add(3 * time.Second))
		line, err := r.ReadBytes('\n')
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		var ev streamEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			t.Fatalf("decode %q: %v", string(line), err)
		}
		return ev
	}

	// The gap marker precedes the next surviving event.
	if gap := read(); gap.Type != EventGap || gap.Dropped != 3 {
		t.Fatalf("first line = %+v, want gap with dropped=3", gap)
	}
	if e1 := read(); e1.Seq != 1 {
		t.Fatalf("second line seq = %d, want 1", e1.Seq)
	}
	if e2 := read(); e2.Seq != 2 {
		t.Fatalf("third line seq = %d, want 2", e2.Seq)
	}
}
