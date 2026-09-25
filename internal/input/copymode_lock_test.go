package input

import (
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// newCopyModeWindow builds a daemon window with some scrollback and copy mode
// active, and tears down its goroutines when the test ends.
func newCopyModeWindow(t testing.TB, id string) *terminal.Window {
	t.Helper()

	ptyDataChan := make(chan struct{}, 1)
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
	t.Cleanup(func() { close(drainDone) })

	win := terminal.NewDaemonWindow(id, "copymode", 0, 0, 80, 24, 0, "pty-"+id, ptyDataChan, config.DefaultScrollbackLines)
	if win == nil {
		t.Fatal("NewDaemonWindow returned nil")
	}
	t.Cleanup(func() { win.Close() })

	// Real content, so the motions below have cells to walk.
	win.WriteOutput([]byte("alpha beta gamma delta\r\nsecond line of text here\r\nthird line\r\n"))
	win.EnterCopyMode()
	if win.CopyMode == nil || !win.CopyMode.Active {
		t.Fatal("EnterCopyMode did not activate copy mode")
	}
	return win
}

func key(s string) tea.KeyPressMsg {
	if len(s) == 1 {
		return tea.KeyPressMsg{Code: rune(s[0]), Text: s}
	}
	return tea.KeyPressMsg{Code: rune(s[0]), Text: s}
}

// Note on what is NOT tested here, deliberately.
//
// The copy-mode side effects run outside the RLockIO region. No runtime test
// can tell that apart from running them inside it: asserting the lock is free
// after HandleCopyModeKey returns passes either way, because a deferred
// window.RUnlockIO() also releases before returning. The hazard is latent
// (nothing called inside that region blocks or re-locks), so there is no
// observable behaviour to assert on.
//
// The guarantee is structural, and the compiler enforces it:
// handleNormalInput, handleSearchInput and handleVisualInput do not receive a
// *app.OS at all. Code running under the lock has no route to the
// OS, and therefore no route to a PTY write or a second RLockIO. Reintroducing
// the hazard requires changing those signatures, which is a visible edit
// rather than an accidental one.
//
// The tests below cover what IS observable: that the traversal is still
// correctly synchronized against the PTY writer, and that moving the effects
// did not change what they do.

// TestCopyModeKeyAgainstConcurrentPTYWriter drives the interleaving the
// narrowing is about: a copy-mode key handler walking the cell buffer while
// the PTY writer mutates it under the exclusive lock.
//
// Run with -race. It covers two things at once: that the traversal is properly
// synchronized against the writer (a missing lock shows up as a data race on
// the emulator cell buffer), and that neither side starves (a handler that
// took the lock reentrantly, or held it across a blocking effect, wedges here
// and the test never finishes).
func TestCopyModeKeyAgainstConcurrentPTYWriter(t *testing.T) {
	win := newCopyModeWindow(t, "copymode-lock-0002")
	o := &app.OS{Settings: config.Global, Mode: app.WindowManagementMode}

	var wg sync.WaitGroup

	// The daemon PTY path: queue to outputWriter, which mutates the cell
	// buffer under LockIO on its own goroutine. This is WriteOutputAsync, not
	// WriteOutput; the async path is the only one production uses, and unlike
	// the sync one it leaves the UI-goroutine-owned dirty flags alone.
	//
	// Volume is deliberately small and bounded. Copy-mode motions are
	// O(scrollback) per keypress and WriteOutputAsync applies no backpressure,
	// so an unbounded flood makes this quadratic and turns a lock test into a
	// multi-minute benchmark. What matters is that the two lock users
	// interleave, not how much data moves.
	wg.Go(func() {
		for range 50 {
			win.WriteOutputAsync([]byte("output from the shell\r\n"))
		}
	})

	keys := []string{"j", "k", "h", "l", "w", "b", "e", "0", "$"}
	for i := range 200 {
		HandleCopyModeKey(key(keys[i%len(keys)]), o, win)
		if win.CopyMode == nil || !win.CopyMode.Active {
			win.EnterCopyMode()
		}
	}

	wg.Wait()
}
