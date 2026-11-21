package session

import (
	"net"
	"testing"
	"time"
)

func newFailingConnState(t *testing.T) *connState {
	t.Helper()
	server, client := net.Pipe()
	_ = server.Close()
	return &connState{
		conn:             client,
		clientID:         "test-client",
		done:             make(chan struct{}),
		ptySubscriptions: make(map[string]*ptySubscriber),
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
		ID:           "ptytest-00000001",
		subscribers:  make(map[string]*ptySubscriber),
		outputBuffer: []byte("hello world"),
		outputPos:    len("hello world"),
		outputSeq:    int64(len("hello world")),
	}

	cs := newFailingConnState(t)
	sub := pty.subscribeSub(cs.clientID, 0, false)
	cs.ptySubscriptions[pty.ID] = sub

	go d.streamPTYOutput(cs, pty, sub)

	select {
	case <-cs.done:
	case <-time.After(2 * time.Second):
		t.Fatal("streamPTYOutput did not drop the client on write failure")
	}

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
