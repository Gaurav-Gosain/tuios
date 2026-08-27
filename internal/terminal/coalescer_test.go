package terminal

import (
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// The render coalescer has two jobs, and they pull in opposite directions:
// show a quiet pane's output at once, and do not ask the compositor to draw a
// flooding pane more than 120 times a second. The latency work made the first
// one true, so these pin the second one so it cannot be lost later.

// TestCoalescerEmitsQuietPaneImmediately is the property the leading edge
// exists for. A pane that has been silent has nothing to coalesce with, so its
// first output has no reason to wait for anything.
//
// The bound is loose on purpose. What is being asserted is "not a tick period",
// not a particular scheduler latency, and a tight bound here would fail on a
// loaded machine while telling nobody anything.
func TestCoalescerEmitsQuietPaneImmediately(t *testing.T) {
	ptyData := make(chan struct{}, 1)
	w := NewDaemonWindow("coal-lead", "pane", 0, 0, 80, 24, 0, "pty-coal-lead", ptyData, config.DefaultScrollbackLines)
	if w == nil {
		t.Fatal("NewDaemonWindow returned nil")
	}
	t.Cleanup(w.Close)

	// Long enough that any interval the coalescer runs at has elapsed, so this
	// is the quiet-pane case rather than the tail of a burst.
	time.Sleep(30 * time.Millisecond)
	select {
	case <-ptyData:
	default:
	}

	start := time.Now()
	w.WriteOutputAsync([]byte("x"))
	select {
	case <-ptyData:
	case <-time.After(2 * time.Second):
		t.Fatal("a quiet pane's output never raised a render signal")
	}
	if waited := time.Since(start); waited > 4*time.Millisecond {
		t.Errorf("quiet pane waited %v for its render signal; the leading edge is gone "+
			"and every echoed keystroke is paying a tick period again", waited)
	}
}

// TestCoalescerCapsAFloodingPane is the property the coalescer exists for. A
// pane writing continuously must not raise a render signal per write, or the
// compositor is asked to draw partial frames as fast as the guest can produce
// them.
func TestCoalescerCapsAFloodingPane(t *testing.T) {
	ptyData := make(chan struct{}, 1)
	w := NewDaemonWindow("coal-cap", "pane", 0, 0, 80, 24, 0, "pty-coal-cap", ptyData, config.DefaultScrollbackLines)
	if w == nil {
		t.Fatal("NewDaemonWindow returned nil")
	}
	t.Cleanup(w.Close)

	const (
		flood  = 200 * time.Millisecond
		period = 8 * time.Millisecond
	)

	done := make(chan struct{})
	go func() {
		defer close(done)
		deadline := time.Now().Add(flood)
		for time.Now().Before(deadline) {
			w.WriteOutputAsync([]byte("flood "))
			time.Sleep(200 * time.Microsecond)
		}
	}()

	signals := 0
	stop := time.After(flood)
	for {
		select {
		case <-ptyData:
			signals++
			continue
		case <-stop:
		}
		break
	}
	<-done

	// The writer above offers roughly 1000 writes. A cap at one signal per
	// period allows about 25 over this window; the allowance is generous
	// because a slow scheduler can stretch the window, and the failure being
	// guarded against is hundreds, not twenty-six.
	if maxSignals := int(flood/period) + 10; signals > maxSignals {
		t.Errorf("flooding pane raised %d render signals in %v, cap allows %d: "+
			"the rate limit is gone and the compositor is drawing partial frames",
			signals, flood, maxSignals)
	}
	if signals == 0 {
		t.Error("flooding pane raised no render signals at all")
	}
}
