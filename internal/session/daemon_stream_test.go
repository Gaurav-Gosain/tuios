package session

import (
	"net"
	"testing"
	"time"
)

// newFailingConnState returns a connState whose connection always fails writes,
// simulating a slow/stuck client, plus the peer end (already closed).
func newFailingConnState(t *testing.T) *connState {
	t.Helper()
	server, client := net.Pipe()
	// Closing the read side makes every write on `client` fail immediately
	// instead of blocking, standing in for a client that never drains.
	_ = server.Close()
	return &connState{
		conn:             client,
		clientID:         "test-client",
		done:             make(chan struct{}),
		codec:            DefaultCodec(),
		ptySubscriptions: make(map[string]struct{}),
	}
}

// TestSendMessageDropsClientOnWriteError verifies that a failed send tears the
// client down (closes done) rather than leaving a desynced connection healthy.
func TestSendMessageDropsClientOnWriteError(t *testing.T) {
	d := NewDaemon(&DaemonConfig{})
	cs := newFailingConnState(t)

	if err := d.sendMessage(cs, MsgPong, nil); err == nil {
		t.Fatal("expected sendMessage to return a write error")
	}

	select {
	case <-cs.done:
		// Good: client dropped.
	default:
		t.Fatal("sendMessage did not drop the client (done not closed) on write error")
	}
}

// TestStreamPTYOutputDropsOnWriteError verifies that when streaming to a stuck
// client fails, streamPTYOutput tears the whole client down and leaves the
// connState coherent: the subscription is removed and the PTY subscriber gone.
func TestStreamPTYOutputDropsOnWriteError(t *testing.T) {
	d := NewDaemon(&DaemonConfig{})

	pty := &PTY{
		ID:          "ptytest-00000001",
		subscribers: make(map[string]chan []byte),
		// Pre-seed buffered output so Subscribe immediately delivers a chunk that
		// streamPTYOutput will try (and fail) to write.
		outputBuffer: []byte("hello world"),
		outputPos:    len("hello world"),
	}

	cs := newFailingConnState(t)
	cs.ptySubscriptions[pty.ID] = struct{}{}

	go d.streamPTYOutput(cs, pty)

	select {
	case <-cs.done:
		// Good: client dropped on write failure.
	case <-time.After(2 * time.Second):
		t.Fatal("streamPTYOutput did not drop the client on write failure")
	}

	// The deferred cleanup (unsubscribe + drop subscription) runs after the
	// goroutine returns, so poll until the connState is coherent.
	deadline := time.Now().Add(2 * time.Second)
	for {
		cs.mu.Lock()
		_, stillSubscribed := cs.ptySubscriptions[pty.ID]
		cs.mu.Unlock()

		pty.subscribersMu.RLock()
		_, stillBroadcasting := pty.subscribers[cs.clientID]
		pty.subscribersMu.RUnlock()

		if !stillSubscribed && !stillBroadcasting {
			break
		}
		if time.Now().After(deadline) {
			if stillSubscribed {
				t.Fatal("ptyID was not removed from ptySubscriptions on exit")
			}
			t.Fatal("PTY subscriber was not removed on streamPTYOutput exit")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
