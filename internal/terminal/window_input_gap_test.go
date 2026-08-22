package terminal

import (
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// repaintPayload is a full-screen truecolor repaint, the shape DOOM-fire and
// every other frame-at-a-time TUI produces. It matters that this is a repaint
// rather than scrolling lines: a repaint is what makes the client's compose
// expensive, and the compose is what input ends up queued behind.
func repaintPayload(cols, rows int) []byte {
	b := make([]byte, 0, cols*rows*24)
	b = append(b, "\x1b[H"...)
	for y := range rows {
		for x := range cols {
			b = append(b, "\x1b[48;2;"...)
			b = strconv.AppendInt(b, int64((x*7+y*13)%256), 10)
			b = append(b, ';')
			b = strconv.AppendInt(b, int64((x*3+y*5)%256), 10)
			b = append(b, ";0m"...)
			b = append(b, "\xe2\x96\x80"...)
		}
		if y < rows-1 {
			b = append(b, "\r\n"...)
		}
	}
	return b
}

// TestFloodedPaneLeavesRoomForInput is the regression test for keystrokes
// queueing behind a text flood.
//
// The client composes frames on the same goroutine that carries keystrokes, so
// a keypress can only be handled in a gap between frames. The coalescer used to
// signal every 8ms regardless of what a frame cost; once a frame cost more than
// that, a signal was always already waiting when the goroutine came back around
// and there was no gap left. With DOOM-fire at 158x40 a key took 18-28ms p50
// and up to 55ms to reach the pty, against 1ms through tmux.
//
// This models that goroutine rather than the whole client: one loop that
// serves either a render signal or a keystroke, where serving a render signal
// costs a fixed simulated compose. That is the contention in its smallest
// honest form, and it makes the test independent of how fast this machine
// happens to compose a real frame.
func TestFloodedPaneLeavesRoomForInput(t *testing.T) {
	if raceEnabled {
		t.Skip("wall-clock budget is meaningless under race instrumentation")
	}

	const (
		composeCost = 20 * time.Millisecond
		// The key cadence must not divide the paced render period, or every
		// key lands at the same phase of the frame and the sample says more
		// about the harmonic than about the gap.
		keyEvery = 17 * time.Millisecond
		keys     = 40
	)

	ptyDataChan := make(chan struct{}, 1)
	w := NewDaemonWindow("flood-window-01", "flood", 0, 0, 158, 40, 0, "pty-flood", ptyDataChan)
	t.Cleanup(w.Close)

	// The flood: a pane repainting as fast as the output path will take it.
	floodDone := make(chan struct{})
	var offered atomic.Int64
	var floodWG sync.WaitGroup
	payload := repaintPayload(158, 40)
	floodWG.Go(func() {
		for {
			select {
			case <-floodDone:
				return
			default:
				w.WriteOutputAsync(payload)
				offered.Add(1)
			}
		}
	})
	defer func() {
		close(floodDone)
		floodWG.Wait()
	}()

	// The UI goroutine: it serves render signals and keystrokes from the same
	// select, and a render signal occupies it for a whole compose.
	type keyEvent struct{ sent time.Time }
	keyChan := make(chan keyEvent)
	stopUI := make(chan struct{})
	waits := make(chan time.Duration, keys)
	var uiWG sync.WaitGroup
	uiWG.Go(func() {
		for {
			select {
			case <-stopUI:
				return
			case k := <-keyChan:
				waits <- time.Since(k.sent)
			case <-ptyDataChan:
				time.Sleep(composeCost)
				w.ChargeRenderCost(composeCost)
			}
		}
	})

	// Let the flood reach steady state so the first key is not measuring an
	// idle window.
	time.Sleep(300 * time.Millisecond)

	// Absolute scheduling: a key that blocks must not push the next one out,
	// or the send cadence drifts into lockstep with the frame cadence.
	base := time.Now()
	for i := range keys {
		if d := time.Until(base.Add(time.Duration(i) * keyEvery)); d > 0 {
			time.Sleep(d)
		}
		keyChan <- keyEvent{sent: time.Now()}
	}
	close(stopUI)
	uiWG.Wait()
	close(waits)

	if offered.Load() == 0 {
		t.Fatal("flood produced no output, so this pass proves nothing")
	}

	got := make([]time.Duration, 0, keys)
	for d := range waits {
		got = append(got, d)
	}
	if len(got) != keys {
		t.Fatalf("got %d key measurements, want %d", len(got), keys)
	}
	slices.Sort(got)

	p50 := got[len(got)/2]
	p95 := got[len(got)*95/100]

	// A key that lands in a real gap is served immediately. Requiring a share
	// of them is the load-robust half of this assertion: without pacing the
	// render signal is always already waiting and this fraction is zero, and no
	// amount of machine noise turns that into a quarter.
	var quick int
	for _, d := range got {
		if d < 2*time.Millisecond {
			quick++
		}
	}
	if quick*4 < len(got) {
		t.Errorf("only %d/%d keys were served within 2ms; the flood is leaving no gap for input (p50 %v, p95 %v)",
			quick, len(got), p50, p95)
	}

	// And the tail has to stay bounded by roughly one compose, not the several
	// it took when every key had to win a coin toss against the next frame.
	if p95 > 2*composeCost {
		t.Errorf("p95 key wait %v exceeds %v (p50 %v)", p95, 2*composeCost, p50)
	}
	t.Logf("key wait under flood: p50 %v p95 %v max %v, %d/%d within 2ms, %d repaints offered",
		p50, p95, got[len(got)-1], quick, len(got), offered.Load())
}
