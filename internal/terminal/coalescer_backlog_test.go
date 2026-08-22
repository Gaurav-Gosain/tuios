package terminal

import (
	"testing"
	"time"
)

// backlogWindow builds a daemon window with a drained data channel.
func backlogWindow(t *testing.T, id string) *Window {
	t.Helper()
	ptyData := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-ptyData:
			case <-done:
				return
			}
		}
	}()
	t.Cleanup(func() { close(done) })

	w := NewDaemonWindow(id, "pane", 0, 0, 80, 24, 0, "pty-"+id, ptyData)
	if w == nil {
		t.Fatal("NewDaemonWindow returned nil")
	}
	t.Cleanup(w.Close)
	return w
}

// TestCoalescerPacesDownAPaneThatIsBehind pins the rule that a pane whose
// emulator is far behind is drawn at the catch-up interval rather than at the
// rate its last frame's cost would buy it.
//
// The point of the rule is that those frames are already spent: the bytes
// queued behind them will overwrite what they would show, and composing one
// holds the pane's read lock for the length of a compose, which is time the
// pane's own output writer spends waiting instead of catching up.
func TestCoalescerPacesDownAPaneThatIsBehind(t *testing.T) {
	w := backlogWindow(t, "coal-backlog")

	// A cheap frame, so cost alone would put the pane at the floor.
	w.ChargeRenderCost(time.Millisecond)
	if got := w.coalesceInterval(); got != minCoalesceInterval {
		t.Fatalf("a caught-up pane with a cheap frame paced at %v, want the %v floor", got, minCoalesceInterval)
	}

	w.queuedBytes.Store(catchUpBacklog - 1)
	if got := w.coalesceInterval(); got != minCoalesceInterval {
		t.Errorf("a pane one byte under the backlog paced at %v, want the %v floor still", got, minCoalesceInterval)
	}

	w.queuedBytes.Store(catchUpBacklog)
	if got := w.coalesceInterval(); got != catchUpCoalesceInterval {
		t.Errorf("a pane at the backlog paced at %v, want %v", got, catchUpCoalesceInterval)
	}

	// Being behind outranks an expensive frame in the other direction too: the
	// ceiling is not the answer here, the catch-up interval is.
	w.ChargeRenderCost(time.Second)
	if got := w.coalesceInterval(); got != catchUpCoalesceInterval {
		t.Errorf("a pane both behind and expensive paced at %v, want %v", got, catchUpCoalesceInterval)
	}

	// And it lets go once the pane has caught up, so a pane is not left at 4fps
	// by a burst it has already worked through.
	w.queuedBytes.Store(0)
	if got := w.coalesceInterval(); got != maxCoalesceInterval {
		t.Errorf("a caught-up pane with an expensive frame paced at %v, want the %v ceiling", got, maxCoalesceInterval)
	}
}

// TestQueuedBytesTracksWhatIsWaitingForTheEmulator pins the counter the rule
// reads. It has to fall back to zero once the writer has worked through the
// queue, because a counter that only ever climbs would leave every pane paced
// at the catch-up interval for the rest of its life.
func TestQueuedBytesTracksWhatIsWaitingForTheEmulator(t *testing.T) {
	w := backlogWindow(t, "coal-queued")

	const chunk = 4096
	for range 32 {
		w.WriteOutputAsync(make([]byte, chunk))
	}

	deadline := time.Now().Add(5 * time.Second)
	for w.queuedBytes.Load() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("queuedBytes settled at %d, want 0 once the writer drained the queue", w.queuedBytes.Load())
		}
		time.Sleep(time.Millisecond)
	}
}

// TestPacedCoalescerStillEmitsWhileBehind is the tail guarantee at the new,
// longest interval. The coalescer's rate limit must never swallow the last
// signal of a burst: a pane paced right down while it catches up and then left
// showing a frame from before the catch-up would be worse than the busy screen
// this pacing exists to quieten.
func TestPacedCoalescerStillEmitsWhileBehind(t *testing.T) {
	ptyData := make(chan struct{}, 1)
	w := NewDaemonWindow("coal-behind", "pane", 0, 0, 80, 24, 0, "pty-coal-behind", ptyData)
	if w == nil {
		t.Fatal("NewDaemonWindow returned nil")
	}
	t.Cleanup(w.Close)

	// Hold the pane over the backlog for the whole burst, so every interval it
	// arms is the catch-up one.
	w.queuedBytes.Store(catchUpBacklog * 4)

	var signals []time.Time
	done := make(chan struct{})
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for {
			select {
			case <-ptyData:
				signals = append(signals, time.Now())
			case <-done:
				return
			}
		}
	}()

	deadline := time.Now().Add(600 * time.Millisecond)
	for time.Now().Before(deadline) {
		w.noteOutput()
		time.Sleep(time.Millisecond)
	}
	lastNote := time.Now()

	time.Sleep(catchUpCoalesceInterval + 400*time.Millisecond)
	close(done)
	<-drained

	if len(signals) == 0 {
		t.Fatal("a pane paced down while behind raised no render signals at all")
	}
	if newest := signals[len(signals)-1]; !newest.After(lastNote) {
		t.Errorf("last render signal came %v before the final output; the pane is left showing a stale frame",
			lastNote.Sub(newest))
	}
}
