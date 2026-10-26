package session

import (
	"bytes"
	"fmt"
	"runtime"
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

// settledHeap is the heap still reachable after two collections. The second
// one empties the victim cache of the pool the stream's frame buffers go back
// to, which keeps them for one collection.
func settledHeap() int64 {
	runtime.GC()
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return int64(ms.HeapAlloc)
}

// settledBaseline is settledHeap once the heap has stopped moving. Tests that ran
// before this one in the package leave garbage behind, and goroutines and
// timers that still hold memory: a daemon one of them started stays reachable
// from a timer for seconds after that test ends. A reading taken at once can
// hold megabytes that come off during the measurement. It takes readings a
// short while apart until they have stayed within a few KiB of each other for
// longer than the measurement that follows, for a bounded number of readings.
func settledBaseline(t *testing.T) int64 {
	t.Helper()
	const margin = 16 << 10
	const quiet = 3 * time.Second
	const tries = 150
	const gap = 100 * time.Millisecond
	anchor := settledHeap()
	since := time.Now()
	for range tries {
		time.Sleep(gap)
		cur := settledHeap()
		if d := cur - anchor; d < -margin || d > margin {
			anchor, since = cur, time.Now()
			continue
		}
		if time.Since(since) >= quiet {
			return cur
		}
	}
	t.Logf("the heap had not stayed still for %v within %d readings %v apart; the baseline is the last of them",
		quiet, tries, gap)
	return settledHeap()
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
func TestAFloodedStreamGivesBackItsBatchAfterASecond(t *testing.T) {
	const chunks = 16
	const chunkBytes = 16 << 10
	// The batch a flood grows is at least 200 KiB; most of it must still be
	// held right after the flood.
	const kept = 150 << 10
	const budget = 64 << 10
	// The early reading must come well inside the second the batch is kept.
	const earlyBy = 600 * time.Millisecond
	// It is taken late inside that guard, so a batch let go well before the
	// second is up fails it, not only one let go at once.
	const earlyAt = 475 * time.Millisecond

	r := newGateRig(t)
	before := settledBaseline(t)

	line := 100000
	for range chunks {
		r.pty.appendAndBroadcast(streamTrimChunk(line))
		line += 2048
	}
	_, last := readStreamBytes(t, r, chunks*chunkBytes, 10*time.Second, false)

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

	time.Sleep(time.Until(last.Add(earlyAt)))
	early := settledHeap()
	if took := time.Since(last); took > earlyBy {
		t.Fatalf("the first reading came %v after the last frame was read, more than the %v the check needs",
			took, earlyBy)
	}
	time.Sleep(time.Until(last.Add(1500 * time.Millisecond)))
	late := settledHeap()

	heldEarly := early - before
	heldLate := late - before
	t.Logf("a flooded stream holds %d bytes %v after the flood and %d bytes a second and a half later",
		heldEarly, earlyAt, heldLate)
	if heldEarly < kept {
		t.Fatalf("a flooded stream holds %d bytes shortly after the flood, want the grown batch (at least %d bytes) "+
			"kept until the stream has been idle for a second", heldEarly, kept)
	}
	if heldLate > budget {
		t.Fatalf("a flooded stream holds %d bytes of live heap 1.5s after the flood, the budget is %d",
			heldLate, budget)
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
