package terminal

import (
	"runtime"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"tuios.dev/vt"
)

const batchTrimLines = 2048

func batchTrimChunk(first int) []byte {
	b := make([]byte, 0, 16<<10)
	for n := first; n < first+batchTrimLines; n++ {
		b = append(b, numberedLine(n)...)
	}
	return b
}

func settledLiveHeap(t *testing.T) int64 {
	t.Helper()
	const margin = 16 << 10
	const quiet = 3 * time.Second
	const tries = 150
	const gap = 100 * time.Millisecond
	anchor := liveHeap()
	since := time.Now()
	for range tries {
		time.Sleep(gap)
		cur := liveHeap()
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
	return liveHeap()
}

func floodPanesWithChunks(t *testing.T, windows []*Window, chunks int) (idle time.Time) {
	t.Helper()
	releases := make([]func(), len(windows))
	for i, w := range windows {
		releases[i] = stallWriter(t, w)
		w.WriteOutputAsync(batchTrimChunk(100000))
	}
	time.Sleep(200 * time.Millisecond)
	for _, w := range windows {
		for c := 1; c < chunks; c++ {
			w.WriteOutputAsync(batchTrimChunk(100000 + c*batchTrimLines))
		}
	}
	for _, release := range releases {
		release()
	}
	drainedAt := make([]time.Time, len(windows))
	var drains sync.WaitGroup
	for i, w := range windows {
		drains.Go(func() {
			w.DrainPendingOutput()
			drainedAt[i] = time.Now()
		})
	}
	done := make(chan struct{})
	go func() {
		drains.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("DrainPendingOutput had not returned after 10s")
	}
	return slices.MinFunc(drainedAt, time.Time.Compare)
}

// TestFloodedDaemonPanesGiveBackTheirBatchAfterASecond holds what a pane costs
// once a flood of large chunks is over. Fifteen chunks of 16 KiB leave the
// output queue at its smallest size but grow the writer's batch to most of
// 256 KiB. Right after the drain the batch is still held, since a pane that
// floods in bursts would otherwise grow it again at every burst, and once the
// pane has stayed idle for a second it is given back. A burst after that still
// reaches the screen.
func TestFloodedDaemonPanesGiveBackTheirBatchAfterASecond(t *testing.T) {
	const panes = 6
	const chunks = 15
	const kept = 150_000
	const budget = 150_000
	const earlyBy = 800 * time.Millisecond
	const earlyAt = 475 * time.Millisecond
	const floods = 5

	burst := chunks * batchTrimLines
	depth := 1
	if vt.Backend == "ghostty" {
		depth = 2 * burst
	}
	before := settledLiveHeap(t)
	windows := make([]*Window, panes)
	for i := range windows {
		windows[i] = memoryWindow(t, "batch-"+strconv.Itoa(i), 12, 6, depth)
	}
	var idle, drained time.Time
	var early int64
	for flood := 1; ; flood++ {
		idle = floodPanesWithChunks(t, windows, chunks)
		drained = time.Now()
		for i, w := range windows {
			w.outputQ.mu.Lock()
			slots := len(w.outputQ.buf)
			w.outputQ.mu.Unlock()
			if slots > outputQueueKeep {
				t.Fatalf("pane %d: the flood grew the queue to %d slots, want it at %d or fewer so only the batch grew",
					i, slots, outputQueueKeep)
			}
		}
		time.Sleep(time.Until(idle.Add(earlyAt)))
		early = liveHeap()
		took := time.Since(idle)
		if took <= earlyBy {
			break
		}
		if flood == floods {
			t.Fatalf("the first reading came %v after the first pane drained in all %d floods, more than the %v the check needs",
				took, floods, earlyBy)
		}
		t.Logf("flood %d: the first reading came %v after the first pane drained, so this flood decides nothing", flood, took)
		time.Sleep(1500 * time.Millisecond)
	}
	time.Sleep(time.Until(drained.Add(1500 * time.Millisecond)))
	lateAt := time.Since(drained)
	late := liveHeap()
	runtime.KeepAlive(windows)

	heldEarly := (early - before) / panes
	heldLate := (late - before) / panes
	t.Logf("a pane holds %d bytes %v after the first pane drained and %d bytes %v after the last one did",
		heldEarly, earlyAt, heldLate, lateAt)
	if heldEarly < kept {
		t.Fatalf("a pane holds %d bytes shortly after the drain, want the grown batch (at least %d bytes) "+
			"kept until the pane has been idle for a second", heldEarly, kept)
	}
	if heldLate > budget {
		t.Fatalf("a pane holds %d bytes of live heap %v after the drain returned (the reading is due 1.5s after it), the budget is %d",
			heldLate, lateAt, budget)
	}

	w := windows[0]
	w.LockIO()
	w.SetScrollbackMaxLines(burst + 16)
	w.UnlockIO()
	first := 100000 + chunks*batchTrimLines
	for c := range chunks {
		w.WriteOutputAsync(batchTrimChunk(first + c*batchTrimLines))
	}
	drainWithin(t, w, 10*time.Second)
	want := strconv.Itoa(first + chunks*batchTrimLines - 1)
	got := paneLines(w)
	if len(got) == 0 || got[len(got)-1] != want {
		t.Fatalf("after the burst that followed the give-back the pane ends %q, want its last line %q",
			got[max(0, len(got)-7):], want)
	}
	if len(got) < burst {
		t.Fatalf("after the burst that followed the give-back the pane holds %d lines, want all %d of the burst",
			len(got), burst)
	}
	last := first + burst - 1
	for i, line := range got {
		if want := strconv.Itoa(last - (len(got) - 1 - i)); line != want {
			t.Fatalf("line %d of %d held after the burst that followed the give-back is %q, want %q",
				i+1, len(got), line, want)
		}
	}
}
