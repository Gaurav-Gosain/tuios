package app

import (
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// TestRenderTerminalDoesNotReenterIOLock is the regression test for the freeze
// that made tuios unusable: the whole UI would wedge at zero CPU within seconds
// of a shell producing output, in both daemon and standalone mode.
//
// renderTerminal takes window.RLockIO() for the duration of its cell walk. It
// then called m.getRealCursor(), which takes the focused window's RLockIO()
// again. When the rendered window is the focused one, that is the same
// sync.RWMutex twice on one goroutine.
//
// A sync.RWMutex is not reentrant for readers. Once a writer calls Lock, every
// subsequent RLock blocks so the writer cannot be starved. So the interleaving
// below is fatal:
//
//	UI goroutine   RLockIO            (read lock held)
//	PTY goroutine  LockIO             (queues, waits for the reader)
//	UI goroutine   RLockIO again      (parks behind the queued writer)
//
// The UI goroutine waits for the writer, the writer waits for the UI
// goroutine's first read lock, and nothing runs again. The PTY writer at
// internal/terminal/window_io.go fires on every chunk of shell output, so the
// interleaving is hit constantly in normal use.
//
// This test drives that race directly: one goroutine renders in a loop while
// another takes and releases the exclusive I/O lock the way the PTY reader
// does. Against the unfixed code it deadlocks and fails on the deadline.
// Against the fixed code the cursor query happens before the lock is taken, the
// two acquisitions never nest, and it completes promptly.
func TestRenderTerminalDoesNotReenterIOLock(t *testing.T) {
	win := newTestWindow(t, "reentry", 80, 24)
	win.WriteOutput([]byte("hello from the shell\r\n"))

	m := newTestOS(win)
	// TerminalMode with this window focused is what makes getRealCursor reach
	// the lock rather than returning nil early, and focus is what makes it the
	// same lock renderTerminal is already holding.
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
			// Exactly what the PTY reader does: take the exclusive side to
			// mutate the cell buffer, then release it.
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
			// Drives the same emulator writes the PTY reader performs, under
			// the same lock, so an unsynchronized read in getRealCursor races.
			win.WriteOutput([]byte("x"))
		}
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		m.getRealCursor()
	}

	close(stop)
	<-done

	var _ *terminal.Window = win
}
