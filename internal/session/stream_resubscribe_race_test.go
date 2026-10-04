package session

import (
	"bytes"
	"net"
	"testing"
	"time"
)

// TestStaleStreamLeavesTheNewSubscriptionAlone is the regression test for a
// pane that stopped updating after its workspace was hidden and shown quickly.
//
// Hiding a pane unsubscribes it, and the stream goroutine ends once it has
// drained its closed channel. When that goroutine was blocked writing to a
// slow client, the client had already shown the pane again by the time it
// ended, and its cleanup released the subscription by client ID: the new
// subscriber, and the new connState entry. The new goroutine's channel was
// closed under it and the pane's output went nowhere.
//
// The order is forced rather than raced: the old goroutine is held in its
// write by a pipe nobody reads until the new subscription exists.
func TestStaleStreamLeavesTheNewSubscriptionAlone(t *testing.T) {
	d := NewDaemon(&DaemonConfig{})
	server, client := net.Pipe()
	cs := &connState{
		conn:             server,
		clientID:         "race-client",
		done:             make(chan struct{}),
		ptySubscriptions: make(map[string]*ptySubscriber),
		ptyResume:        make(map[string]int64),
	}
	pty := &PTY{
		ID:           "ptytest-00000005",
		subscribers:  make(map[string]*ptySubscriber),
		outputBuffer: make([]byte, 64*1024),
	}

	// The first subscription, as handleSubscribePTY makes it.
	old := pty.subscribeSub(cs.clientID, 0, false)
	cs.ptySubscriptions[pty.ID] = old
	oldDone := make(chan struct{})
	go func() {
		defer close(oldDone)
		d.streamPTYOutput(cs, pty, old)
	}()

	// Output the old goroutine takes and then blocks writing, because nothing
	// reads the pipe yet.
	pty.appendAndBroadcast([]byte("before the hide"))
	deadline := time.Now().Add(5 * time.Second)
	for len(old.ch) > 0 || old.queued.Load() > 0 {
		if time.Now().After(deadline) {
			t.Fatal("the old stream never took its output")
		}
		time.Sleep(time.Millisecond)
	}

	// Hide: what handleUnsubscribePTY does.
	cs.mu.Lock()
	delete(cs.ptySubscriptions, pty.ID)
	cs.mu.Unlock()
	resume := pty.Unsubscribe(cs.clientID)

	// Show again, while the old goroutine is still in its write.
	cs.mu.Lock()
	cs.ptySubscriptions[pty.ID] = nil
	cs.mu.Unlock()
	next := pty.subscribeSub(cs.clientID, resume, false)
	cs.mu.Lock()
	cs.ptySubscriptions[pty.ID] = next
	cs.mu.Unlock()
	nextDone := make(chan struct{})
	go func() {
		defer close(nextDone)
		d.streamPTYOutput(cs, pty, next)
	}()
	t.Cleanup(func() {
		cs.drop()
		_ = client.Close()
		d.cancel()
		<-nextDone
	})

	// Unblock the old goroutine: it finishes its write, finds its channel
	// closed and runs its cleanup.
	received := make(chan []byte, 16)
	go func() {
		for {
			msg, err := ReadMessage(client)
			if err != nil {
				return
			}
			if msg.Type == MsgPTYOutput {
				_, data, _ := ParseBinaryPTYMessage(msg.Payload)
				received <- bytes.Clone(data)
			}
		}
	}()
	select {
	case <-oldDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the old stream did not end after its channel was closed")
	}

	if got := pty.subscriberFor(cs.clientID); got != next {
		t.Fatalf("the old stream's cleanup removed the new subscriber (have %p, want %p)", got, next)
	}
	cs.mu.Lock()
	entry, ok := cs.ptySubscriptions[pty.ID]
	cs.mu.Unlock()
	if !ok || entry != next {
		t.Fatal("the old stream's cleanup removed the new connState entry")
	}

	// And the new stream still delivers.
	pty.appendAndBroadcast([]byte("after the show"))
	var got []byte
	timeout := time.After(5 * time.Second)
	for !bytes.Contains(got, []byte("after the show")) {
		select {
		case data := <-received:
			got = append(got, data...)
		case <-timeout:
			t.Fatalf("the pane stopped updating: the client got %q after the show", got)
		}
	}
}
