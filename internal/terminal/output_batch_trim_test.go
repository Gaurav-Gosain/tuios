package terminal

import (
	"runtime"
	"strconv"
	"testing"
	"time"
)

// batchTrimLines is how many numbered lines one flood chunk holds. Numbered
// from 100000, every line is eight bytes, so a chunk is 16 KiB.
const batchTrimLines = 2048

// batchTrimChunk is one 16 KiB chunk of a flood, the numbered lines from first
// on. Every call makes a fresh slice, since WriteOutputAsync takes ownership of
// it.
func batchTrimChunk(first int) []byte {
	b := make([]byte, 0, 16<<10)
	for n := first; n < first+batchTrimLines; n++ {
		b = append(b, numberedLine(n)...)
	}
	return b
}

// settledLiveHeap is liveHeap once the heap has stopped moving. Tests that ran
// before this one in the package leave garbage behind, and goroutines and
// timers that still hold memory: a daemon one of them started stays reachable
// from a timer for seconds after that test ends. A reading taken at once can
// hold memory that come off during the measurement. It takes readings a
// short while apart until they have stayed within a few KiB of each other for
// longer than the measurement that follows, for a bounded number of readings.
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

// floodPanesWithChunks floods every pane the way floodPanes does, with chunks
// of 16 KiB instead of one line each: the writer takes the first and stops on
// the lock, the rest queue behind it, and every pane is drained once all the
// writers are let go. It reports when the first writer was let go.
func floodPanesWithChunks(t *testing.T, windows []*Window, chunks int) (released time.Time) {
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
	released = time.Now()
	for _, release := range releases {
		release()
	}
	for _, w := range windows {
		drainWithin(t, w, 10*time.Second)
	}
	return released
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
	// Fifteen, so the drain marker is the sixteenth slot and the queue never
	// grows past the size it keeps.
	const chunks = 15
	const kept = 150_000
	const budget = 150_000
	// The early reading must come well inside the second the batch is kept.
	const earlyBy = 600 * time.Millisecond
	// It is taken late inside that guard, so a batch let go well before the
	// second is up fails it, not only one let go at once.
	const earlyAt = 475 * time.Millisecond

	before := settledLiveHeap(t)
	windows := make([]*Window, panes)
	for i := range windows {
		windows[i] = memoryWindow(t, "batch-"+strconv.Itoa(i), 12, 6, 1)
	}
	released := floodPanesWithChunks(t, windows, chunks)
	drained := time.Now()
	for i, w := range windows {
		w.outputQ.mu.Lock()
		slots := len(w.outputQ.buf)
		w.outputQ.mu.Unlock()
		if slots > outputQueueKeep {
			t.Fatalf("pane %d: the flood grew the queue to %d slots, want it at %d or fewer so only the batch grew",
				i, slots, outputQueueKeep)
		}
	}
	time.Sleep(time.Until(released.Add(earlyAt)))
	early := liveHeap()
	if took := time.Since(released); took > earlyBy {
		t.Fatalf("the drain and the first reading took %v, more than the %v the check needs", took, earlyBy)
	}
	time.Sleep(time.Until(drained.Add(1500 * time.Millisecond)))
	lateAt := time.Since(drained)
	late := liveHeap()
	runtime.KeepAlive(windows)

	heldEarly := (early - before) / panes
	heldLate := (late - before) / panes
	t.Logf("a pane holds %d bytes %v after the release and %d bytes %v after the drain returned",
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
	// One line of scrollback cannot show the burst, so ask for room for all of it.
	burst := chunks * batchTrimLines
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
	// The backend sets how many lines the pane keeps: the pure Go emulator
	// honours the limit above, libghostty fixes its depth at creation. Either
	// way the lines the pane holds must be one unbroken run ending at the
	// burst's last line, so a chunk lost or reordered after the give-back fails.
	if len(got) < 1024 {
		t.Fatalf("after the burst that followed the give-back the pane holds %d lines, want at least %d",
			len(got), 1024)
	}
	last := first + burst - 1
	for i, line := range got {
		if want := strconv.Itoa(last - (len(got) - 1 - i)); line != want {
			t.Fatalf("line %d of %d held after the burst that followed the give-back is %q, want %q",
				i+1, len(got), line, want)
		}
	}
}
