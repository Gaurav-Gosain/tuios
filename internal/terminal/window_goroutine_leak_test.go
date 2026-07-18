package terminal

import (
	"testing"
	"testing/synctest"
)

// TestDaemonWindowOpenCloseLeaksNoGoroutines asserts that opening and closing a
// daemon window leaves nothing running.
//
// NewDaemonWindow starts two goroutines - outputWriter and renderCoalescer -
// and renderCoalescer owns an 8ms time.Ticker. Both select on w.outputDone,
// and Close() is the only thing that closes it. Leaking either one leaks the
// ticker, the Window, and the whole vt.Emulator with its scrollback, per
// window, for the life of the process. That has been a real bug here, and the
// failure mode (slow memory growth, a background goroutine still ticking for a
// pane the user closed) is invisible to every functional test.
//
// synctest is what makes this a real assertion rather than a heuristic. Inside
// a bubble, the test fails if any goroutine started in the bubble is still
// alive when the bubble's root function returns - no goroutine-count sampling,
// no sleeping and hoping, no parsing of runtime stack dumps. It also makes the
// ticker's fake time advance only when everything is durably blocked, so a
// coalescer that survived Close would keep the bubble alive and be reported.
//
// Verified to fail on broken code: commenting out the `close(w.outputDone)` in
// Close() makes this test fail with
// "panic: deadlock: main bubble goroutine has exited but blocked goroutines
// remain", with a stack naming Window.outputWriter as the survivor.
func TestDaemonWindowOpenCloseLeaksNoGoroutines(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ptyDataChan := make(chan struct{}, 1)

		w := NewDaemonWindow("leak-window-0001", "leak", 0, 0, 80, 24, 0, "pty-leak-0001", ptyDataChan)

		// Drive real work through both goroutines before teardown, so the test
		// covers the interesting case (a window that was actually used) rather
		// than one whose goroutines never left their first select.
		w.WriteOutputAsync([]byte("hello \x1b[32mworld\x1b[0m\r\n"))

		// Let outputWriter drain the queue and renderCoalescer fire at least
		// one tick. synctest.Wait blocks until every other bubble goroutine is
		// durably blocked; fake time then advances to the pending tick.
		synctest.Wait()

		w.Close()

		// Post-close writes must be dropped, not queued into a channel whose
		// reader is gone.
		w.WriteOutputAsync([]byte("after close"))
		w.WriteOutput([]byte("after close"))

		// Close is idempotent; a second call must not double-close outputDone.
		w.Close()

		// Returning from here ends the bubble. If outputWriter or
		// renderCoalescer is still alive, synctest fails the test.
	})
}

// TestDaemonWindowCloseIsIdempotentUnderLoad is the synctest counterpart to
// TestDaemonWindowCloseUnderOutputFlood: same teardown race, but the bubble
// also proves no goroutine survives the double Close.
func TestDaemonWindowCloseIsIdempotentUnderLoad(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ptyDataChan := make(chan struct{}, 1)
		w := NewDaemonWindow("leak-window-0002", "leak", 0, 0, 80, 24, 0, "pty-leak-0002", ptyDataChan)

		payload := []byte("line of \x1b[31mcolored\x1b[0m output\r\n")
		for range 64 {
			w.WriteOutputAsync(payload)
		}
		synctest.Wait()

		closed := make(chan struct{}, 2)
		for range 2 {
			go func() {
				w.Close()
				closed <- struct{}{}
			}()
		}
		<-closed
		<-closed
	})
}
