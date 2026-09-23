package federation

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"
)

// TestStreamOpenReachesTheProxy covers the one statement a hub makes about a
// relayed connection: whether the process that asked for it may act as the
// person. It rides in the open frame, which only the hub's mux writes, and the
// far proxy's dial is told it, so that proxy can pick the socket that lets an
// attach verify a reply from human. A stream opened without it, which is every
// stream from an older hub, reads as not vouched.
func TestStreamOpenReachesTheProxy(t *testing.T) {
	stub := startStubDaemon(t, helloOK("far-1", 0))
	var mu sync.Mutex
	var seen []StreamOpen
	dialer := func(_ context.Context, _ Host) (Transport, error) {
		hub, remote := duplexPipe(t)
		go func() {
			_ = ServeProxyFor(remote, remote, func(open StreamOpen) (net.Conn, error) {
				mu.Lock()
				seen = append(seen, open)
				mu.Unlock()
				return stub.dial()
			})
		}()
		return hub, nil
	}
	m := managerFor(t, testOptions(dialer), Host{Name: "build", Addr: "unused"})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if r := m.Reports(ctx)[0]; r.Status != StatusUp {
		t.Fatalf("status is %q (%s), want up", r.Status, r.Reason)
	}

	last := func() StreamOpen {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			mu.Lock()
			n := len(seen)
			var got StreamOpen
			if n > 0 {
				got = seen[n-1]
			}
			mu.Unlock()
			if n > 0 {
				return got
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("the proxy never dialed for the stream")
		return StreamOpen{}
	}
	reset := func() {
		mu.Lock()
		seen = nil
		mu.Unlock()
	}

	reset()
	conn, err := m.OpenConnectionAs(ctx, "build", StreamOpen{Human: true})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if got := last(); !got.Human {
		t.Error("a stream the hub vouched for reached the proxy unvouched")
	}
	_ = conn.Close()

	reset()
	conn, err = m.OpenConnection(ctx, "build")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if got := last(); got.Human {
		t.Error("a stream opened without a statement reached the proxy vouched")
	}
	_ = conn.Close()
}

// TestStreamOpenPayload pins the wire form: the zero value is the empty
// payload every older peer sends, and anything unreadable decodes to it.
func TestStreamOpenPayload(t *testing.T) {
	if b := (StreamOpen{}).encode(); b != nil {
		t.Errorf("the zero value encodes to %q, want no payload", b)
	}
	if got := decodeStreamOpen(StreamOpen{Human: true}.encode()); !got.Human {
		t.Error("human did not survive a round trip")
	}
	for name, payload := range map[string][]byte{
		"empty":    nil,
		"garbage":  []byte("not json"),
		"oversize": append([]byte(`{"human":true,"pad":"`), make([]byte, maxStreamOpenPayload)...),
	} {
		if got := decodeStreamOpen(payload); got.Human {
			t.Errorf("%s payload decoded as vouched", name)
		}
	}
}
