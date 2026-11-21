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

	var inFlight atomic.Int32
	arrived := make(chan int32, 4)
	release := make(chan struct{}, 4)
	var writeMu sync.Mutex

	go func() {
		for {
			m, err := ReadMessage(server)
			if err != nil {
				return
			}
			arrived <- inFlight.Add(1)
			go func(reqType MessageType, reqID uint64) {
				<-release
				marker := "list"
				if reqType == MsgKill {
					marker = "kill"
				}
				resp, _ := NewMessage(MsgSessionList, &SessionListPayload{
					Sessions: []SessionInfo{{Name: marker}},
				})
				resp.ReqID = reqID
				writeMu.Lock()
				_ = WriteMessage(server, resp)
				writeMu.Unlock()
				inFlight.Add(-1)
			}(m.Type, m.ReqID)
		}
	}()

	type result struct {
		name string
		err  error
	}
	results := make(chan result, 2)

	roundTrip := func(reqType MessageType, want string) {
		msg, err := NewMessage(reqType, &KillPayload{SessionName: want})
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
		if err := resp.ParsePayload(&payload); err != nil {
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

	select {
	case n := <-arrived:
		if n != 1 {
			t.Fatalf("first arrival saw in-flight=%d, want 1", n)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no request reached the daemon")
	}

	select {
	case n := <-arrived:
		t.Fatalf("two round-trips outstanding at once (in-flight=%d); responses can misroute", n)
	case <-time.After(300 * time.Millisecond):
	}

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

func newTestTUIClient(conn net.Conn) *TUIClient {
	c := NewTUIClient()
	c.conn = conn
	return c
}
