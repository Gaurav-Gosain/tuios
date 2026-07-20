package terminal

import (
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// burstPayload is line-heavy on purpose. The dominant cost of writing PTY
// output into the emulator is scrolling the cell buffer once per line, not
// parsing the bytes, so a payload of long lines would understate by a wide
// margin how long a real burst holds the I/O lock.
func burstPayload() []byte {
	var b []byte
	for i := 0; i < 64; i++ {
		b = append(b, fmt.Sprintf("/usr/share/doc/package-%04d/README.md %d\r\n", i, i*7919)...)
	}
	return b
}

// startBurst floods a window's output path the way the daemon read loop does
// for a pane running something like a recursive find. It returns a stop
// function and a counter of how many payloads were offered.
func startBurst(w *Window) (stop func(), offered *atomic.Int64) {
	done := make(chan struct{})
	var count atomic.Int64
	var wg sync.WaitGroup
	payload := burstPayload()
	wg.Go(func() {
		for {
			select {
			case <-done:
				return
			default:
				w.WriteOutputAsync(payload)
				count.Add(1)
			}
		}
	})
	return func() {
		close(done)
		wg.Wait()
	}, &count
}

// newBurstTestWindow builds a daemon-mode window with its output writer running
// and the render signal drained, which is the shape the compositor sees.
func newBurstTestWindow(t *testing.T, id string) *Window {
	t.Helper()
	ptyDataChan := make(chan struct{}, 1)
	drainDone := make(chan struct{})
	go func() {
		for {
			select {
			case <-ptyDataChan:
			case <-drainDone:
				return
			}
		}
	}()
	t.Cleanup(func() { close(drainDone) })

	w := NewDaemonWindow(id, "burst", 0, 0, 80, 24, 0, "pty-"+id, ptyDataChan)
	t.Cleanup(w.Close)
	return w
}

// TestBackgroundBurstDoesNotStarveCompositor is the regression test for the
// echo collapse: with a sustained burst in a background pane, keystrokes in the
// focused pane took tens of seconds to appear and the backlog grew without
// bound.
//
// The input path was never the problem. The compositor was. It must take the
// read side of every window's ioMu on every frame, and outputWriter held the
// exclusive side across the whole pending batch. Parsing a 256KiB batch of
// line-heavy output costs tens of milliseconds, sync.RWMutex parks readers
// behind a queued writer, and a bursting pane always has more data waiting, so
// the writer re-queued immediately and the renderer never got in. Frames
// stopped for every pane at once, which is why an echo in an unrelated pane
// never arrived even though the keystroke reached its PTY promptly.
//
// So this measures the property that actually broke: how long the compositor
// waits to read a pane that is bursting. It asserts a bound rather than a
// speedup because the failure was unbounded, and it samples at frame cadence
// because that is when the real renderer asks.
func TestBackgroundBurstDoesNotStarveCompositor(t *testing.T) {
	if raceEnabled {
		t.Skip("wall-clock budget is meaningless under race instrumentation")
	}
	w := newBurstTestWindow(t, "burst-window-0001")

	stop, offered := startBurst(w)
	defer stop()

	// Let the burst reach steady state so the first samples are not measuring
	// an idle window.
	time.Sleep(200 * time.Millisecond)

	const frames = 120
	waits := make([]time.Duration, 0, frames)
	for i := 0; i < frames; i++ {
		start := time.Now()
		w.RLockIO()
		// The real render path reads the cell buffer here. Touching the
		// emulator keeps the critical section honest without being slow.
		if w.Terminal != nil {
			_ = w.Terminal.CursorPosition()
		}
		w.RUnlockIO()
		waits = append(waits, time.Since(start))

		// Ask again at roughly 60fps, as the renderer does.
		time.Sleep(16 * time.Millisecond)
	}

	if offered.Load() == 0 {
		t.Fatal("burst produced no output, so this pass proves nothing")
	}

	var total time.Duration
	for _, d := range waits {
		total += d
	}
	mean := total / time.Duration(len(waits))
	sort.Slice(waits, func(i, j int) bool { return waits[i] < waits[j] })
	p95 := waits[len(waits)*95/100]

	t.Logf("compositor wait on a bursting pane: mean %v, p50 %v, p95 %v, worst %v over %d frames",
		mean, waits[len(waits)/2], p95, waits[len(waits)-1], len(waits))

	// Bounds sit between the two measured behaviours with room on both sides.
	// On the code that had the defect this loop measured mean 73ms and p95
	// 77ms, and the compositor spent 8.8 of the 10 seconds it ran waiting for
	// one background pane. With the lock hold bounded it measures mean 1.2ms
	// and p95 4ms. The limits below are an order of magnitude above the healthy
	// figures, so a loaded machine does not flake them, and comfortably under
	// the broken ones, so the defect cannot come back unnoticed.
	const (
		maxMean = 20 * time.Millisecond
		maxP95  = 30 * time.Millisecond
	)
	if mean > maxMean || p95 > maxP95 {
		t.Errorf("compositor starved by a background burst: mean wait %v (limit %v), p95 %v (limit %v)",
			mean, maxMean, p95, maxP95)
	}
}

// TestChunkedVTWriteMatchesSingleWrite guards the risk the chunking introduces.
//
// outputWriter now splits a batch at fixed byte offsets, which can fall in the
// middle of an escape sequence or a grapheme cluster. The emulator is built to
// take that (a PTY read boundary already falls anywhere), but the fix depends
// on it, so it is worth pinning: the same bytes must produce the same screen
// whether they arrive whole or in chunks.
func TestChunkedVTWriteMatchesSingleWrite(t *testing.T) {
	// Escapes, wide runes, combining marks and wrapping, so the split lands
	// mid-sequence and mid-cluster many times over.
	var stream []byte
	for i := 0; i < 400; i++ {
		stream = append(stream, fmt.Sprintf(
			"\x1b[3%dm line %04d é́ 你好 \U0001f600 padding-to-force-wrap-%d\x1b[0m\r\n",
			i%8, i, i)...)
	}

	whole := vt.NewEmulator(80, 24)
	defer whole.Close()
	if _, err := whole.Write(stream); err != nil {
		t.Fatalf("whole write: %v", err)
	}

	chunked := vt.NewEmulator(80, 24)
	defer chunked.Close()
	// A chunk size that is not a divisor of anything in the stream, so the
	// boundaries land in awkward places rather than lining up with newlines.
	const chunk = 97
	for off := 0; off < len(stream); off += chunk {
		end := min(off+chunk, len(stream))
		if _, err := chunked.Write(stream[off:end]); err != nil {
			t.Fatalf("chunked write at %d: %v", off, err)
		}
	}

	if got, want := chunked.Render(), whole.Render(); got != want {
		t.Errorf("chunked writes rendered differently from one write:\n got %q\nwant %q", got, want)
	}
}

// TestBackgroundBurstDoesNotDropInput is the other half of the requirement.
// Shedding a bursting pane's intermediate frames is correct, because nobody can
// read them. Shedding a keystroke never is. This sends input throughout a
// sustained burst and requires every byte to arrive, in order, promptly.
func TestBackgroundBurstDoesNotDropInput(t *testing.T) {
	burstWin := newBurstTestWindow(t, "burst-window-0002")

	var mu sync.Mutex
	var received []byte
	inputWin := newBurstTestWindow(t, "input-window-0003")
	inputWin.DaemonWriteFunc = func(b []byte) error {
		mu.Lock()
		defer mu.Unlock()
		received = append(received, b...)
		return nil
	}

	stop, offered := startBurst(burstWin)
	defer stop()
	time.Sleep(200 * time.Millisecond)

	const keystrokes = 60
	var sent []byte
	var slowest time.Duration
	for i := 0; i < keystrokes; i++ {
		key := []byte{byte('a' + i%26)}
		start := time.Now()
		if err := inputWin.SendInput(key); err != nil {
			t.Fatalf("keystroke %d rejected: %v", i, err)
		}
		if d := time.Since(start); d > slowest {
			slowest = d
		}
		sent = append(sent, key...)
		time.Sleep(5 * time.Millisecond)
	}

	if offered.Load() == 0 {
		t.Fatal("burst produced no output, so this pass proves nothing")
	}

	mu.Lock()
	got := string(received)
	mu.Unlock()
	if got != string(sent) {
		t.Errorf("input lost or reordered under burst:\n got %q\nwant %q", got, sent)
	}
	if slowest > 250*time.Millisecond {
		t.Errorf("slowest keystroke took %v to be accepted under a background burst", slowest)
	}
}
