package app

import (
	"testing"
	"time"
)

// Deliberately NOT ported to testing/synctest, despite the deadline-based
// failure below looking like exactly what synctest is for. A goroutine blocked
// on a sync.Mutex or sync.RWMutex is not "durably blocked" as synctest defines
// it: only channel operations, select, time.Sleep, WaitGroup.Wait and
// Cond.Wait qualify. A lock-reentry deadlock inside a bubble therefore does not
// trip synctest's deadlock detector; the bubble simply stops making progress
// and the test binary hangs until the package timeout, with a worse diagnostic
// than the explicit deadline here gives.
//
// synctest is the right tool for the sibling tests that assert on goroutine
// lifetime and on lock state sampled with TryLock (see
// internal/terminal/window_goroutine_leak_test.go and
// window_lock_discipline_test.go). It is the wrong tool for a mutex deadlock.
func TestRenderTerminalDoesNotReenterIOLock(t *testing.T) {
	win := newTestWindow(t, "reentry", 80, 24)
	win.WriteOutput([]byte("hello from the shell\r\n"))

	m := newTestOS(win)
	m.Mode = TerminalMode

	const renders = 300

	writerDone := make(chan struct{})
	stopWriter := make(chan struct{})
	go func() {
		defer close(writerDone)
		for {
			select {
			case <-stopWriter:
				return
			default:
			}
			win.LockIO()
			win.UnlockIO()
		}
	}()

	renderDone := make(chan struct{})
	go func() {
		defer close(renderDone)
		for range renders {
			win.MarkContentDirty()
			m.renderTerminal(win, true, true)
		}
	}()

	select {
	case <-renderDone:
	case <-time.After(15 * time.Second):
		close(stopWriter)
		t.Fatal("renderTerminal deadlocked against a concurrent I/O-lock writer: " +
			"the render path took the window I/O read lock reentrantly, which parks " +
			"behind any queued writer while still holding the lock that writer needs")
	}

	close(stopWriter)
	<-writerDone
}

// TestGetRealCursorStillLocksForItsOwnCallers guards the other half of the fix.
// getRealCursor reads emulator state that the PTY writer mutates, so it must
// keep taking the lock for callers that do not already hold it, such as View.
// Deleting the lock instead of hoisting the call would silence the deadlock and
// reintroduce the data race it was added to close, which -race would catch here.
func TestGetRealCursorStillLocksForItsOwnCallers(t *testing.T) {
	win := newTestWindow(t, "cursor-lock", 80, 24)
	m := newTestOS(win)
	m.Mode = TerminalMode

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			win.WriteOutput([]byte("x"))
		}
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		m.getRealCursor()
	}

	close(stop)
	<-done

	var _ = win
}
