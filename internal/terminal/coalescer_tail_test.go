package terminal

import (
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestPacedCoalescerStillEmitsAfterTheFloodStops guards the failure mode that
// would be worse than the one the pacing fixes.
//
// Pacing makes a flooding pane wait longer between render signals, and a rate
// limit that swallows the last signal of a burst leaves the pane showing
// whatever it had one interval ago, forever, with no further output to shake it
// loose. The coalescer's tail is what prevents that: an interval that has
// already emitted arms a timer, and that timer emits when it expires. Pacing
// only changes how long that interval is, so this asserts the guarantee holds
// at the ceiling, where the wait is longest.
func TestPacedCoalescerStillEmitsAfterTheFloodStops(t *testing.T) {
	ptyData := make(chan struct{}, 1)
	w := NewDaemonWindow("coal-tail", "pane", 0, 0, 80, 24, 0, "pty-coal-tail", ptyData, config.DefaultScrollbackLines)
	if w == nil {
		t.Fatal("NewDaemonWindow returned nil")
	}
	t.Cleanup(w.Close)

	// A frame this expensive pins the interval to its ceiling, which is the
	// longest a trailing frame can ever be made to wait.
	w.ChargeRenderCost(time.Second)
	if got := w.coalesceInterval(); got != maxCoalesceInterval {
		t.Fatalf("coalesceInterval() = %v, want the %v ceiling", got, maxCoalesceInterval)
	}

	// Drain signals the way the UI goroutine does, recording when each arrived.
	var mu sync.Mutex
	var signals []time.Time
	drainDone := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			select {
			case <-ptyData:
				mu.Lock()
				signals = append(signals, time.Now())
				mu.Unlock()
			case <-drainDone:
				return
			}
		}
	})

	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		w.WriteOutputAsync([]byte("flood "))
		time.Sleep(time.Millisecond)
	}
	lastWrite := time.Now()

	// The trailing signal has to land within one interval of the last write,
	// plus room for the output writer to get to that final chunk.
	time.Sleep(maxCoalesceInterval + 400*time.Millisecond)
	close(drainDone)
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(signals) == 0 {
		t.Fatal("flooding pane raised no render signals at all")
	}
	newest := signals[len(signals)-1]
	if !newest.After(lastWrite) {
		t.Errorf("last render signal came %v before the final write; the pane is left showing a stale frame",
			lastWrite.Sub(newest))
	}
}
