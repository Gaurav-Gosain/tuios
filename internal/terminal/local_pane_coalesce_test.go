//go:build unix

package terminal

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// windowShows reports whether the window's emulator holds want anywhere on
// its screen.
func windowShows(w *Window, want string) bool {
	w.RLockIO()
	defer w.RUnlockIO()
	if w.Terminal == nil {
		return false
	}
	width, height := w.Terminal.Width(), w.Terminal.Height()
	for y := range height {
		var row strings.Builder
		for x := range width {
			if c := w.Terminal.CellAt(x, y); c != nil {
				row.WriteString(c.Content)
			}
		}
		if strings.Contains(row.String(), want) {
			return true
		}
	}
	return false
}

// TestLocalPaneFloodSignalsAtTheCoalescerRate pins that a local PTY pane (an
// ephemeral SSH or web session, or the fallback when the daemon cannot start)
// asks for frames through the render coalescer, as a daemon pane does. The
// reader used to signal PTYDataChan after every read, about 1 KiB on macOS,
// which composed a frame per read during a flood.
//
// It also pins the trailing edge: the output that ends the flood is followed by
// a signal, so it is drawn without waiting for more output.
//
// NEGATIVE CONTROL: measured. With the reader signalling per read the flood
// sent 4870 signals in 166 ms, where this allows 23.
func TestLocalPaneFloodSignalsAtTheCoalescerRate(t *testing.T) {
	signals := make(chan struct{}, 1)
	exitChan := make(chan string, 1)
	// The shell stays alive after the flood, so the reader's own exit signal
	// cannot stand in for the trailing edge.
	w, err := NewWindow("test-local-coal", "Test", 0, 0, 80, 24, 0, exitChan, signals,
		config.DefaultScrollbackLines,
		"/bin/sh", "-c", "sleep 0.2; yes 0123456789abcdefghij | head -c 2000000; printf 'FLOOD''DONE'; sleep 30")
	if err != nil {
		t.Skipf("cannot start a local PTY: %v", err)
	}
	defer w.Close()

	var first time.Time
	count := 0
	deadline := time.After(30 * time.Second)
	for {
		select {
		case <-signals:
			if first.IsZero() {
				first = time.Now()
			}
			count++
			if !windowShows(w, "FLOODDONE") {
				continue
			}
			elapsed := time.Since(first)
			// One signal per interval, plus the leading edge, plus one for
			// scheduling slack at either end.
			limit := int(elapsed/minCoalesceInterval) + 3
			t.Logf("%d signals over %v, limit %d", count, elapsed, limit)
			if count > limit {
				t.Errorf("a flooding local pane sent %d render signals in %v, want at most %d (one per %v)",
					count, elapsed, limit, minCoalesceInterval)
			}
			return
		case <-deadline:
			t.Fatalf("the end of the flood never reached a render signal after %d signals", count)
		}
	}
}
