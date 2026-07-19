package terminal

import (
	"sync"
	"testing"
)

// TestDaemonWindowCloseUnderOutputFlood is a race-detector regression test for
// the window teardown races in window_io.go: Close() closing outputDone while
// outputWriter/renderCoalescer select on it, and the unlocked w.Terminal /
// w.Pty dereferences in the reader goroutines that panicked after Close nilled
// the fields. It opens a daemon window (which starts outputWriter and
// renderCoalescer), floods it with output from several goroutines, and closes
// it concurrently. Run with -race to detect torn field access; a passing run
// also proves Close is panic-free and idempotent under load. It is kept
// alongside the synctest version in window_goroutine_leak_test.go rather than
// replaced by it: this one runs with real goroutine scheduling and real
// parallelism, which is what makes the race detector useful, while the synctest
// version adds the goroutine-lifetime assertion that real scheduling cannot
// give deterministically.
func TestDaemonWindowCloseUnderOutputFlood(t *testing.T) {
	ptyDataChan := make(chan struct{}, 1)

	// Drain the render signal channel so renderCoalescer's non-blocking sends
	// never matter, mirroring the UI goroutine on the real path.
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
	defer close(drainDone)

	w := NewDaemonWindow("race-window-0001", "race", 0, 0, 80, 24, 0, "pty-race-0001", ptyDataChan)

	var wg sync.WaitGroup

	// Flood output from several goroutines, as the daemon readLoop does.
	const floodGoroutines = 8
	payload := []byte("hello world \x1b[31mcolored\x1b[0m output line\r\n")
	for i := 0; i < floodGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 2000; j++ {
				w.WriteOutputAsync(payload)
			}
		}()
	}

	// Close concurrently with the flood, from two goroutines to also exercise
	// the idempotent double-close guard.
	wg.Add(2)
	go func() {
		defer wg.Done()
		w.Close()
	}()
	go func() {
		defer wg.Done()
		w.Close()
	}()

	wg.Wait()

	// A post-teardown write must be a no-op, not a panic.
	w.WriteOutputAsync(payload)
	w.WriteOutput(payload)
}
