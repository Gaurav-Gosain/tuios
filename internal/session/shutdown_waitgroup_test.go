package session

import (
	"fmt"
	"net"
	"testing"
	"time"
)

// TestSubscribeDuringStopRegistersNoGoroutine is a race regression. A
// subscribe writes its ack and only then starts the event streamer on d.wg.
// When a client read the ack and stopped the daemon at once, that wg.Add ran
// concurrently with shutdown's wg.Wait, which -race reports and the WaitGroup
// rules forbid. Each round starts a streamer while Stop runs, with nothing
// ordering the two. The streamer blocks writing its preface to a pipe nobody
// reads, so it is still counted when Wait starts, which is the case -race
// checks. Run it under -race: without the race detector it proves nothing.
func TestSubscribeDuringStopRegistersNoGoroutine(t *testing.T) {
	for i := range 20 {
		t.Run(fmt.Sprintf("round%d", i), func(t *testing.T) {
			d, _ := startTestDaemon(t)

			conn, peer := net.Pipe()
			t.Cleanup(func() { _ = conn.Close(); _ = peer.Close() })
			cs := &connState{clientID: "race", conn: conn, done: make(chan struct{})}
			sub, _, err := d.events.subscribeFrom(eventFilter{}, 0, nil)
			if err != nil {
				t.Fatalf("subscribe: %v", err)
			}
			sub.preface = []streamEvent{{Type: EventGap}}
			cs.pendingStream = sub

			stopped := make(chan struct{})
			go func() {
				d.Stop()
				close(stopped)
			}()
			d.startPendingStream(cs)

			// Unblock a streamer that did start, so Stop does not sit out its
			// five second timeout.
			time.Sleep(20 * time.Millisecond)
			_ = peer.Close()
			<-stopped
		})
	}
}
