package terminal

import (
	"bytes"
	"testing"
	"time"
)

// TestWriteOutputAsyncWaitsForRoom holds the queue to maxQueuedBytes: a send
// that would take the pane past it waits for outputWriter to take some back
// off, and returns once there is room. Waiting is backpressure on the socket
// the daemon writes; the alternative, dropping, is a hole in the stream the
// client has no way to fill.
func TestWriteOutputAsyncWaitsForRoom(t *testing.T) {
	prev := maxQueuedBytes
	maxQueuedBytes = 64 << 10
	t.Cleanup(func() { maxQueuedBytes = prev })

	w := NewDaemonWindow("queue-bound", "t", 0, 0, 80, 24, 0, "pty-1", make(chan struct{}, 1), 1000)
	defer w.Close()

	w.ioMu.Lock()
	chunk := bytes.Repeat([]byte("x"), 8<<10)
	w.WriteOutputAsync(chunk)
	time.Sleep(50 * time.Millisecond)
	for w.queuedBytes.Load()+int64(len(chunk)) <= maxQueuedBytes {
		w.WriteOutputAsync(chunk)
	}

	done := make(chan struct{})
	go func() {
		w.WriteOutputAsync(chunk)
		close(done)
	}()
	select {
	case <-done:
		w.ioMu.Unlock()
		t.Fatal("a send past the bound returned instead of waiting")
	case <-time.After(100 * time.Millisecond):
	}
	if q := w.queuedBytes.Load(); q > maxQueuedBytes {
		w.ioMu.Unlock()
		t.Fatalf("the pane holds %d bytes queued, the bound is %d", q, maxQueuedBytes)
	}

	w.ioMu.Unlock()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the sender got no room after the writer was released")
	}
}

// TestWriteOutputAsyncStopsWaitingWhenTheWindowCloses: a sender blocked on a
// full queue is released by Close, so a closing pane cannot hold the daemon
// read loop.
func TestWriteOutputAsyncStopsWaitingWhenTheWindowCloses(t *testing.T) {
	prev := maxQueuedBytes
	maxQueuedBytes = 64 << 10
	t.Cleanup(func() { maxQueuedBytes = prev })

	w := NewDaemonWindow("queue-close", "t", 0, 0, 80, 24, 0, "pty-1", make(chan struct{}, 1), 1000)
	w.ioMu.Lock()
	chunk := bytes.Repeat([]byte("x"), 8<<10)
	w.WriteOutputAsync(chunk)
	time.Sleep(50 * time.Millisecond)
	for w.queuedBytes.Load()+int64(len(chunk)) <= maxQueuedBytes {
		w.WriteOutputAsync(chunk)
	}
	done := make(chan struct{})
	go func() {
		w.WriteOutputAsync(chunk)
		close(done)
	}()
	time.Sleep(50 * time.Millisecond)
	closed := make(chan struct{})
	go func() {
		w.Close()
		close(closed)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		w.ioMu.Unlock()
		t.Fatal("a sender blocked on a closed window never returned")
	}
	if q := w.queuedBytes.Load(); q+int64(len(chunk)) <= maxQueuedBytes {
		w.ioMu.Unlock()
		t.Fatalf("the queue drained to %d bytes, so the close is not what released the sender", q)
	}
	w.ioMu.Unlock()
	<-closed
}
