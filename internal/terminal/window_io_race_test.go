package terminal

import (
	"os/exec"
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
	for range floodGoroutines {
		wg.Go(func() {
			for range 2000 {
				w.WriteOutputAsync(payload)
			}
		})
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

// TestDaemonResponseReaderRacesClose pins the ownership rule for w.Terminal:
// Close() owns the field, and the daemon response reader may only reach the
// emulator through terminalRef, which takes the same ioMu Close() writes under.
// Starting the reader concurrently with Close() is the window SwitchToSession
// opens, where the UI goroutine closes every window while the readers it just
// started are still coming up. Before the fix this failed under -race on the
// reader's unlocked field read against Close()'s write; the loop count is what
// makes it fire on the first run rather than occasionally.
func TestDaemonResponseReaderRacesClose(t *testing.T) {
	for range 200 {
		ptyDataChan := make(chan struct{}, 1)
		w := NewDaemonWindow("reader-race-0001", "reader-race", 0, 0, 80, 24, 0, "pty-reader-race", ptyDataChan)

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			w.StartDaemonResponseReader()
		}()
		go func() {
			defer wg.Done()
			w.Close()
		}()
		wg.Wait()
	}
}

// TestTerminalRefAfterClose pins the other half of the rule: once Close() has
// run the emulator is no longer observable, so a reader starting late gets nil
// rather than a live pointer or a panic.
func TestTerminalRefAfterClose(t *testing.T) {
	ptyDataChan := make(chan struct{}, 1)
	w := NewDaemonWindow("ref-after-close-01", "ref", 0, 0, 80, 24, 0, "pty-ref", ptyDataChan)

	if w.terminalRef() == nil {
		t.Fatal("terminalRef returned nil before Close")
	}

	w.Close()

	if got := w.terminalRef(); got != nil {
		t.Fatalf("terminalRef returned %p after Close, want nil", got)
	}

	// Starting the reader against a closed window must be a no-op, not a panic.
	w.StartDaemonResponseReader()
}

// TestWaitForCmdRacesClose pins the same ownership rule for w.Cmd. The
// process-monitor goroutine started in NewWindow calls waitForCmd for the whole
// life of the window and reads w.Cmd without a lock, so Close() nilling the
// field raced that read whenever Close ran before the goroutine was first
// scheduled. The field is write-once now; this fails under -race if it stops
// being.
func TestWaitForCmdRacesClose(t *testing.T) {
	for range 100 {
		cmd := exec.Command("sleep", "10")
		if err := cmd.Start(); err != nil {
			t.Skipf("cannot start helper process: %v", err)
		}
		w := &Window{ID: "cmd-race-0001", Cmd: cmd}

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			w.waitForCmd() // what the process monitor goroutine does
		}()
		go func() {
			defer wg.Done()
			w.Close()
		}()
		wg.Wait()

		if w.Cmd == nil {
			t.Fatal("Close nilled w.Cmd; the monitor goroutine reads it unlocked")
		}
	}
}
