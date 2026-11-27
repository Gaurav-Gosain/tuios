package session

import (
	"bytes"
	"fmt"
	"testing"
	"time"
)

// streamTrimChunk is one 16 KiB chunk of a flood: 2048 numbered lines of eight
// bytes each, starting at line first. Every call makes a fresh slice, since the
// queue holds the chunk as it is.
func streamTrimChunk(first int) []byte {
	const lines = 2048
	b := make([]byte, 0, 16<<10)
	for n := first; n < first+lines; n++ {
		b = fmt.Appendf(b, "%06d\r\n", n)
	}
	return b
}

// readStreamBytes reads frames from the rig until n bytes of output have come,
// and returns them with the time the last frame was read. It fails the test if
// they have not all come within limit.
func readStreamBytes(t *testing.T, r *gateRig, n int, limit time.Duration, keep bool) (got []byte, last time.Time) {
	t.Helper()
	deadline := time.NewTimer(limit)
	defer deadline.Stop()
	total := 0
	for total < n {
		select {
		case f := <-r.frames:
			if f.typ != MsgPTYOutput {
				continue
			}
			total += len(f.data)
			last = f.at
			if keep {
				got = append(got, f.data...)
			}
		case <-deadline.C:
			t.Fatalf("the client read %d of the %d bytes sent within %v", total, n, limit)
		}
	}
	return got, last
}

// TestAFloodedStreamGivesBackItsBatchAfterASecond holds what one stream costs
// the daemon once a flood of large chunks is over. A flood of sixteen 16 KiB
// chunks leaves the queue at its smallest size but grows the stream's batch to
// most of 256 KiB. Right after the flood the batch is still held, since a pane
// that floods in bursts would otherwise grow it again at every burst, and once
// the stream has stayed idle for a second it is given back. A second burst
// after that still reaches the client whole and in order.
//
// The flood is made to batch on purpose. The test holds the client's send
// lock, queues one chunk and waits until the stream waits for the lock with
// it. The other fifteen chunks then queue behind, and once the lock is let go
// the stream takes them as one batch. How far the batch grows so depends only
// on what was queued, not on how fast the client reads: an earlier version
// flooded freely, and on a loaded runner the reader kept up well enough that
// the batch grew to only 136 KiB.
//
// It reads what it means: the batch's capacity, and how long the stream kept
// it idle, both as the stream records them on its queue (noteBatch). No
// reading depends on when the test runs, so a loaded runner cannot fail it.
// The heap is not read: two collections on a loaded runner took longer than
// the window an earlier version of this test had.
//
// Negative controls: a batch let go as soon as the stream is idle, and one let
// go after 500 ms, each fail the check of how long it was kept.
func TestAFloodedStreamGivesBackItsBatchAfterASecond(t *testing.T) {
	const chunks = 16
	const chunkBytes = 16 << 10
	// The chunks queued behind the held lock go out as one batch, so the
	// batch grows to at least their size.
	const kept = (chunks - 1) * chunkBytes

	r := newGateRig(t)
	line := 100000
	r.cs.sendMu.Lock()
	r.pty.appendAndBroadcast(streamTrimChunk(line))
	line += 2048
	waitStart := time.Now()
	for r.cs.sendMu.Waiting() == 0 {
		if time.Since(waitStart) > 10*time.Second {
			r.cs.sendMu.Unlock()
			t.Fatal("the stream never asked for the send lock with the first chunk")
		}
		time.Sleep(time.Millisecond)
	}
	for range chunks - 1 {
		r.pty.appendAndBroadcast(streamTrimChunk(line))
		line += 2048
	}
	r.cs.sendMu.Unlock()
	readStreamBytes(t, r, chunks*chunkBytes, 10*time.Second, false)

	r.pty.subscribersMu.RLock()
	sub := r.pty.subscribers["gate-client"]
	r.pty.subscribersMu.RUnlock()
	if sub == nil {
		t.Fatal("the stream has no subscriber after the flood")
	}
	sub.out.mu.Lock()
	slots := len(sub.out.buf)
	sub.out.mu.Unlock()
	if slots > queueKeep {
		t.Fatalf("the flood grew the queue to %d slots, want it at %d or fewer so only the batch grew", slots, queueKeep)
	}
	// The stream records the batch's capacity before it sends the batch, so
	// the last one is recorded once its bytes have been read.
	if held, _ := sub.out.heldBatch(); held < kept {
		t.Fatalf("the stream holds a batch of %d bytes right after the flood, want the batch of the %d queued chunks "+
			"(at least %d bytes) kept until the stream has been idle for a second", held, chunks-1, kept)
	}

	// The batch is given back: wait for it, with room for a loaded runner.
	var held int
	var idle time.Duration
	deadline := time.Now().Add(10 * time.Second)
	for {
		if held, idle = sub.out.heldBatch(); held <= streamBatchKeep {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the stream still holds a batch of %d bytes 10s after the flood, want at most %d", held, streamBatchKeep)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Logf("the stream let its batch go after %v idle", idle)
	if idle < queueTrimAfter {
		t.Fatalf("the stream let its batch go after %v idle, want it kept for %v", idle, queueTrimAfter)
	}
	sub.out.mu.Lock()
	slots = len(sub.out.buf)
	sub.out.mu.Unlock()
	if slots > queueKeep {
		t.Fatalf("the queue holds %d slots after the give-back, want at most %d", slots, queueKeep)
	}

	var want []byte
	for range chunks {
		c := streamTrimChunk(line)
		line += 2048
		want = append(want, c...)
		r.pty.appendAndBroadcast(c)
	}
	got, _ := readStreamBytes(t, r, len(want), 10*time.Second, true)
	if !bytes.Equal(got, want) {
		at := 0
		for at < min(len(got), len(want)) && got[at] == want[at] {
			at++
		}
		t.Fatalf("the burst after the give-back reached the client as %d bytes differing from the %d sent at byte %d",
			len(got), len(want), at)
	}
}
