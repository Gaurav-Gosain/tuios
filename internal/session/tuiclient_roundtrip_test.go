package session

import (
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestSendAndWaitResponseSerializesSharedTypes reproduces the response-misrouting
// bug: two concurrent round-trips that await the same response type
// ({MsgSessionList, MsgError}) both register their channel under that type in
// pendingResponses, so the second overwrites the first and the daemon's replies
// route to the wrong waiter (or orphan one until its timeout).
//
// The fix keeps at most one round-trip outstanding, so a shared response type is
// never contended. The daemon here holds each reply until released, letting the
// test observe how many round-trips reach the wire concurrently: exactly one when
// serialized, two under the old type-keyed collision.
func TestSendAndWaitResponseSerializesSharedTypes(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()

	c := newTestTUIClient(client)
	c.StartReadLoop()
	defer c.Close()

	var inFlight int32
	arrived := make(chan int32, 4)
	release := make(chan struct{}, 4)
	var writeMu sync.Mutex

	// Daemon: for each request, report the concurrency level, then hold the reply
	// until the test releases it. The reply echoes the request type so each waiter
	// can prove it got its own answer.
	go func() {
		for {
			m, _, err := ReadMessageWithCodec(server)
			if err != nil {
				return
			}
			arrived <- atomic.AddInt32(&inFlight, 1)
			go func(reqType MessageType) {
				<-release
				marker := "list"
				if reqType == MsgKill {
					marker = "kill"
				}
				resp, _ := NewMessageWithCodec(MsgSessionList, &SessionListPayload{
					Sessions: []SessionInfo{{Name: marker}},
				}, DefaultCodec())
				writeMu.Lock()
				_ = WriteMessageWithCodec(server, resp, DefaultCodec())
				writeMu.Unlock()
				atomic.AddInt32(&inFlight, -1)
			}(m.Type)
		}
	}()

	type result struct {
		name string
		err  error
	}
	results := make(chan result, 2)

	roundTrip := func(reqType MessageType, want string) {
		msg, err := NewMessageWithCodec(reqType, &KillPayload{SessionName: want}, c.codec)
		if err != nil {
			results <- result{err: err}
			return
		}
		resp, err := c.sendAndWaitResponse(msg, MsgSessionList, MsgError)
		if err != nil {
			results <- result{err: err}
			return
		}
		var payload SessionListPayload
		if err := resp.ParsePayloadWithCodec(&payload, c.codec); err != nil {
			results <- result{err: err}
			return
		}
		got := ""
		if len(payload.Sessions) > 0 {
			got = payload.Sessions[0].Name
		}
		results <- result{name: got}
	}

	go roundTrip(MsgKill, "kill")
	go roundTrip(MsgList, "list")

	// First request must reach the daemon.
	select {
	case n := <-arrived:
		if n != 1 {
			t.Fatalf("first arrival saw in-flight=%d, want 1", n)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no request reached the daemon")
	}

	// A second request must NOT be outstanding while the first is unanswered: that
	// is the collision the fix prevents. Under the old code both round-trips are
	// live at once and share the MsgSessionList slot.
	select {
	case n := <-arrived:
		t.Fatalf("two round-trips outstanding at once (in-flight=%d); responses can misroute", n)
	case <-time.After(300 * time.Millisecond):
	}

	// Let the first reply through, then the second round-trip proceeds.
	release <- struct{}{}
	select {
	case <-arrived:
	case <-time.After(2 * time.Second):
		t.Fatal("second request never reached the daemon after releasing the first")
	}
	release <- struct{}{}

	got := map[string]bool{}
	for range 2 {
		select {
		case r := <-results:
			if r.err != nil {
				t.Fatalf("round-trip failed: %v", r.err)
			}
			got[r.name] = true
		case <-time.After(2 * time.Second):
			t.Fatal("a waiter never received its response")
		}
	}
	if !got["kill"] || !got["list"] {
		t.Fatalf("waiters did not each get their own response: %v", got)
	}
}
