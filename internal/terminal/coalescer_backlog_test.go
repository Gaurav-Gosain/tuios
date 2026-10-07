package terminal

import (
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
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

	w := NewDaemonWindow(id, "pane", 0, 0, 80, 24, 0, "pty-"+id, ptyData, config.DefaultScrollbackLines)
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
	if got := w.coalesceInterval(); got != w.catchUpCoalesceInterval() {
		t.Errorf("a pane at the backlog paced at %v, want %v", got, w.catchUpCoalesceInterval())
	}

	// An expensive frame keeps its own, longer interval while the pane is
	// behind: the catch-up interval is a floor, so it never draws a pane
	// that is behind faster than what its frames cost allows.
	w.ChargeRenderCost(time.Second)
	if got := w.coalesceInterval(); got != maxCoalesceInterval {
		t.Errorf("a pane both behind and expensive paced at %v, want the %v ceiling", got, maxCoalesceInterval)
	}

	// And it lets go once the pane has caught up, so a pane is not left at 4fps
	// by a burst it has already worked through.
	w.queuedBytes.Store(0)
	if got := w.coalesceInterval(); got != maxCoalesceInterval {
		t.Errorf("a caught-up pane with an expensive frame paced at %v, want the %v ceiling", got, maxCoalesceInterval)
	}
}

// TestPacedCoalescerStillEmitsWhileBehind is the tail guarantee at the new,
// longest interval. The coalescer's rate limit must never swallow the last
// signal of a burst: a pane paced right down while it catches up and then left
// showing a frame from before the catch-up would be worse than the busy screen
// this pacing exists to quieten.
func TestPacedCoalescerStillEmitsWhileBehind(t *testing.T) {
	ptyData := make(chan struct{}, 1)
	w := NewDaemonWindow("coal-behind", "pane", 0, 0, 80, 24, 0, "pty-coal-behind", ptyData, config.DefaultScrollbackLines)
	if w == nil {
		t.Fatal("NewDaemonWindow returned nil")
	}
	t.Cleanup(w.Close)

	// Hold the pane over the backlog for the whole burst, so every interval it
	// arms is the catch-up one.
	w.queuedBytes.Store(catchUpBacklog * 4)

	// The signals as they arrive, each with its time.
	signals := make(chan time.Time, 1024)
	done := make(chan struct{})
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for {
			select {
			case <-ptyData:
				signals <- time.Now()
			case <-done:
				return
			}
		}
	}()
	defer func() {
		close(done)
		<-drained
	}()

	// The burst. lastNote is taken before the final noteOutput, not after
	// the sleep that follows it: a signal for that output can land inside
	// the sleep, and timed against the end of the sleep it looked early
	// although it drew the final output (the CI failure saw one 4.5 us
	// before such a lastNote).
	var lastNote time.Time
	deadline := time.Now().Add(600 * time.Millisecond)
	for time.Now().Before(deadline) {
		lastNote = time.Now()
		w.noteOutput()
		time.Sleep(time.Millisecond)
	}

	// Wait for a signal after the final output, for at most one catch-up
	// interval and a margin. Each signal is its own condition: the wait ends
	// on the one that matters, not on a fixed sleep.
	timeout := time.After(w.catchUpCoalesceInterval() + 400*time.Millisecond)
	seen := 0
	for {
		select {
		case at := <-signals:
			seen++
			if at.After(lastNote) {
				return
			}
		case <-timeout:
			if seen == 0 {
				t.Fatal("a pane paced down while behind raised no render signals at all")
			}
			t.Fatalf("no render signal came after the final output; the pane is left showing a stale frame")
		}
	}
}
