package terminal

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// liveHeap is the heap still reachable after two collections. The second one
// sweeps what the first one's finalizers let go.
func liveHeap() int64 {
	runtime.GC()
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return int64(ms.HeapAlloc)
}

// memoryWindow is a daemon pane closed when the test ends.
func memoryWindow(t *testing.T, id string, width, height, scrollback int) *Window {
	t.Helper()
	w := NewDaemonWindow(id, "pane", 0, 0, width, height, 0, "pty-"+id, nil, scrollback)
	if w == nil {
		t.Fatal("NewDaemonWindow returned nil")
	}
	t.Cleanup(w.Close)
	return w
}

// stallWriter holds the window's I/O lock, which outputWriter takes for every
// write, so the writer stops on the first chunk it takes and everything sent
// after that stays queued. The returned release lets it go; it may be called
// more than once, and it runs before the window's Close when the test ends, so
// a test that fails while stalled does not leave Close waiting on the lock.
func stallWriter(t *testing.T, w *Window) (release func()) {
	t.Helper()
	w.LockIO()
	var once sync.Once
	release = func() { once.Do(w.UnlockIO) }
	t.Cleanup(release)
	return release
}

// numberedLine is chunk n of a flood: one line holding its number.
func numberedLine(n int) []byte {
	return fmt.Appendf(nil, "%d\r\n", n)
}

// sendLines queues the numbered lines from..to, one chunk each.
func sendLines(w *Window, from, to int) {
	for n := from; n <= to; n++ {
		w.WriteOutputAsync(numberedLine(n))
	}
}

// drainWithin runs DrainPendingOutput and fails the test if it has not
// returned within limit, so a drain that never returns fails rather than
// hangs.
func drainWithin(t *testing.T, w *Window, limit time.Duration) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		w.DrainPendingOutput()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(limit):
		t.Fatalf("DrainPendingOutput had not returned after %v", limit)
	}
}

// paneLines is the text of the pane's scrollback and then its screen, one
// string per line, with trailing blanks dropped from each line and blank lines
// dropped from the end.
func paneLines(w *Window) []string {
	w.RLockIO()
	defer w.RUnlockIO()
	term := w.Terminal
	if term == nil {
		return nil
	}
	var lines []string
	for i := range term.ScrollbackLen() {
		var row strings.Builder
		for _, cell := range term.ScrollbackLine(i) {
			row.WriteString(cell.Content)
		}
		lines = append(lines, strings.TrimRight(row.String(), " "))
	}
	for y := range term.Height() {
		var row strings.Builder
		for x := range term.Width() {
			if cell := term.CellAt(x, y); cell != nil {
				row.WriteString(cell.Content)
			}
		}
		lines = append(lines, strings.TrimRight(row.String(), " "))
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// queueBoundLines is how many numbered lines a stalled pane keeps: the one the
// writer took before it stalled and the 4096 the queue holds behind it.
const queueBoundLines = 4097

// TestIdleDaemonPanesReserveNoOutputQueue holds what a daemon pane that never
// prints costs the client. The pane's output queue used to be a channel of
// 4096 chunks made whole at construction, about 229 KB per pane before the
// pane printed anything.
func TestIdleDaemonPanesReserveNoOutputQueue(t *testing.T) {
	const panes = 50
	const budget = 120_000

	before := liveHeap()
	windows := make([]*Window, panes)
	for i := range windows {
		windows[i] = memoryWindow(t, "idle-"+strconv.Itoa(i), 120, 36, 1000)
	}
	after := liveHeap()
	runtime.KeepAlive(windows)

	perPane := (after - before) / panes
	t.Logf("an idle daemon pane holds %d bytes of live heap", perPane)
	if perPane > budget {
		t.Fatalf("an idle daemon pane holds %d bytes of live heap, the budget is %d", perPane, budget)
	}
}

// TestAStalledPaneKeepsItsOutputInOrderUpToTheBound holds the drop point of a
// pane's output queue. With the writer stalled on the first chunk, the queue
// takes 4096 more and refuses the rest, so the pane shows exactly the first
// 4097 lines, in the order they were sent.
func TestAStalledPaneKeepsItsOutputInOrderUpToTheBound(t *testing.T) {
	const sent = 6000
	w := memoryWindow(t, "bound-order", 120, 36, sent)

	release := stallWriter(t, w)
	sendLines(w, 1, 1)
	// The writer takes the first chunk and stops on the lock.
	time.Sleep(200 * time.Millisecond)
	sendLines(w, 2, sent)
	release()
	drainWithin(t, w, 10*time.Second)

	got := paneLines(w)
	if len(got) != queueBoundLines {
		t.Fatalf("the pane holds %d lines, want the %d the bound keeps", len(got), queueBoundLines)
	}
	for i, line := range got {
		if want := strconv.Itoa(i + 1); line != want {
			t.Fatalf("line %d of the pane is %q, want %q", i+1, line, want)
		}
	}
}

// TestDrainPendingOutputWaitsForRoomInAFullQueue holds the drain that comes
// before a pane is primed. Behind a full queue its marker is not dropped: the
// drain waits until there is room, then until everything queued in front of it
// has been applied, so no accepted line is lost from the history.
func TestDrainPendingOutputWaitsForRoomInAFullQueue(t *testing.T) {
	const sent = 6000
	w := memoryWindow(t, "bound-drain", 120, 36, sent)

	release := stallWriter(t, w)
	sendLines(w, 1, 1)
	time.Sleep(200 * time.Millisecond)
	sendLines(w, 2, sent)

	done := make(chan struct{})
	go func() {
		w.DrainPendingOutput()
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("DrainPendingOutput returned while the writer was stalled behind a full queue")
	case <-time.After(200 * time.Millisecond):
	}
	release()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("DrainPendingOutput had not returned 10s after the writer was released")
	}

	present := make(map[string]bool)
	for _, line := range paneLines(w) {
		present[line] = true
	}
	for n := 1; n <= queueBoundLines; n++ {
		if !present[strconv.Itoa(n)] {
			t.Fatalf("line %d was accepted by the queue and is missing once the drain returned", n)
		}
	}
}

// TestFloodedDaemonPanesGiveBackTheirQueue holds what a pane costs once a
// flood that filled its output queue is over. The queue may grow to its bound
// while the backlog lasts, but after it has stayed empty for a second it lets
// the array go, so a pane that flooded once does not keep the peak.
func TestFloodedDaemonPanesGiveBackTheirQueue(t *testing.T) {
	const panes = 20
	const flood = 5000
	const budget = 150_000

	before := liveHeap()
	windows := make([]*Window, panes)
	releases := make([]func(), panes)
	for i := range windows {
		windows[i] = memoryWindow(t, "flood-"+strconv.Itoa(i), 12, 6, 1)
		releases[i] = stallWriter(t, windows[i])
		sendLines(windows[i], 1, 1)
	}
	// Every writer takes its first chunk and stops on the lock.
	time.Sleep(200 * time.Millisecond)
	for _, w := range windows {
		sendLines(w, 2, flood+1)
	}
	for i, w := range windows {
		releases[i]()
		drainWithin(t, w, 10*time.Second)
	}
	time.Sleep(1500 * time.Millisecond)
	after := liveHeap()
	runtime.KeepAlive(windows)

	perPane := (after - before) / panes
	t.Logf("a daemon pane holds %d bytes of live heap after a flood that filled its queue", perPane)
	if perPane > budget {
		t.Fatalf("a daemon pane holds %d bytes of live heap after a flood that filled its queue, the budget is %d",
			perPane, budget)
	}
}

// TestDrainPendingOutputWaitsForTheWriter holds the drain when the queue has
// room. Its marker goes in at once, but the drain still returns only once the
// writer has applied everything queued in front of it, so while the writer is
// stalled it does not return, and once it returns every line is there.
func TestDrainPendingOutputWaitsForTheWriter(t *testing.T) {
	const sent = 100
	w := memoryWindow(t, "drain-room", 120, 36, sent)

	release := stallWriter(t, w)
	sendLines(w, 1, 1)
	time.Sleep(200 * time.Millisecond)
	sendLines(w, 2, sent)

	done := make(chan struct{})
	go func() {
		w.DrainPendingOutput()
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("DrainPendingOutput returned while the writer was stalled with lines queued in front of it")
	case <-time.After(200 * time.Millisecond):
	}
	release()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("DrainPendingOutput had not returned 10s after the writer was released")
	}

	present := make(map[string]bool)
	for _, line := range paneLines(w) {
		present[line] = true
	}
	for n := 1; n <= sent; n++ {
		if !present[strconv.Itoa(n)] {
			t.Fatalf("line %d was queued in front of the drain and is missing once it returned", n)
		}
	}
}

// floodPanes fills the output queue of every pane to its bound the way
// TestFloodedDaemonPanesGiveBackTheirQueue does, releases every writer, and
// waits for every pane to drain. It reports when the first writer was let go.
func floodPanes(t *testing.T, windows []*Window, flood int) (released time.Time) {
	t.Helper()
	releases := make([]func(), len(windows))
	for i, w := range windows {
		releases[i] = stallWriter(t, w)
		sendLines(w, 1, 1)
	}
	time.Sleep(200 * time.Millisecond)
	for _, w := range windows {
		sendLines(w, 2, flood+1)
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

// TestFloodedDaemonPanesKeepTheirQueueForASecondAfterEveryFlood holds when a
// pane gives back the queue a flood grew. Right after the drain the grown
// array is still held, since a pane that floods in bursts would otherwise grow
// it again at every burst, and once it has stayed empty for a second it is
// given back. The second flood checks that the same holds after the first
// give-back, not only once in a pane's life.
func TestFloodedDaemonPanesKeepTheirQueueForASecondAfterEveryFlood(t *testing.T) {
	const panes = 10
	const flood = 5000
	const budget = 150_000
	// A queue grown to its bound is 4096 slots of about 56 bytes; half of
	// that is well clear of the noise between two readings.
	const kept = 100_000
	// The early reading must come well inside the second the queue is kept.
	const earlyBy = 600 * time.Millisecond

	before := liveHeap()
	windows := make([]*Window, panes)
	for i := range windows {
		windows[i] = memoryWindow(t, "reflood-"+strconv.Itoa(i), 12, 6, 1)
	}
	for round := 1; round <= 2; round++ {
		released := floodPanes(t, windows, flood)
		early := liveHeap()
		if took := time.Since(released); took > earlyBy {
			t.Fatalf("flood %d: the drain and the first reading took %v, more than the %v the check needs",
				round, took, earlyBy)
		}
		time.Sleep(1500 * time.Millisecond)
		late := liveHeap()
		runtime.KeepAlive(windows)

		heldEarly := (early - before) / panes
		heldLate := (late - before) / panes
		t.Logf("flood %d: a pane holds %d bytes right after the drain and %d bytes a second and a half later",
			round, heldEarly, heldLate)
		if heldEarly-heldLate < kept {
			t.Fatalf("flood %d: a pane held %d bytes right after the drain and %d bytes 1.5s later, "+
				"want the grown queue (at least %d bytes) kept until it has been empty for a second",
				round, heldEarly, heldLate, kept)
		}
		if heldLate > budget {
			t.Fatalf("flood %d: a pane holds %d bytes of live heap 1.5s after the drain, the budget is %d",
				round, heldLate, budget)
		}
	}
}

// TestAWindowWithoutAnOutputQueueIgnoresQueuedOutput holds the windows not
// built by NewDaemonWindow, such as a zero Window. They have no output queue,
// and the calls that queue output, a resize or a drain marker do nothing for
// them: no panic and no wait.
func TestAWindowWithoutAnOutputQueueIgnoresQueuedOutput(t *testing.T) {
	calls := []struct {
		name string
		call func(w *Window)
	}{
		{"WriteOutputAsync", func(w *Window) { w.WriteOutputAsync(numberedLine(1)) }},
		{"ResizeFromStream", func(w *Window) { w.ResizeFromStream(80, 24) }},
		{"DrainPendingOutput", func(w *Window) { w.DrainPendingOutput() }},
	}
	for _, c := range calls {
		t.Run(c.name, func(t *testing.T) {
			w := &Window{}
			done := make(chan any, 1)
			go func() {
				defer func() { done <- recover() }()
				c.call(w)
			}()
			select {
			case r := <-done:
				if r != nil {
					t.Fatalf("%s on a window without an output queue panicked: %v", c.name, r)
				}
			case <-time.After(2 * time.Second):
				t.Fatalf("%s on a window without an output queue had not returned after 2s", c.name)
			}
		})
	}
}
