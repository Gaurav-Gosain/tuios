package session

import (
	"bufio"
	"encoding/json"
	"net"
	"testing"
	"time"
)

// TestEventHubDropsWhenQueueFull verifies the slow-subscriber policy: once the
// bounded queue is full, further events are dropped and counted rather than
// blocking the publisher, and the surviving events are the earliest ones.
func TestEventHubDropsWhenQueueFull(t *testing.T) {
	h := newEventHub()
	sub := h.subscribe(eventFilter{}, 4)

	for range 10 {
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
		ptySubscriptions: make(map[string]struct{}),
	}

	sub := d.events.subscribe(eventFilter{}, 2)
	// Burst 5 events with no streamer draining: seq 1,2 buffer; 3,4,5 drop.
	for range 5 {
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
