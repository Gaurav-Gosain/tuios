package input

import (
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
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

	win := terminal.NewDaemonWindow(id, "copymode", 0, 0, 80, 24, 0, "pty-"+id, ptyDataChan)
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
// The lock narrowing moved the copy-mode side effects out of the RLockIO
// region. No runtime test distinguishes the pre-fix structure from this one,
// and one was tried and removed rather than kept as false comfort: asserting
// the lock is free after HandleCopyModeKey returns passes on BOTH structures,
// because the old code's `defer window.RUnlockIO()` also released before
// returning. The pre-fix defect is latent - nothing currently called inside
// that region blocks or re-locks - so there is no observable behaviour to
// assert on.
//
// What actually guarantees the fix is structural, and the compiler enforces
// it: handleNormalInput, handleSearchInput and handleVisualInput no longer
// receive a *app.OS at all. Code running under the lock has no route to the
// OS, and therefore no route to a PTY write or a second RLockIO. Reintroducing
// the hazard now requires changing those signatures, which is a visible edit
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
	o := &app.OS{Mode: app.WindowManagementMode}

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
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 50 {
			win.WriteOutputAsync([]byte("output from the shell\r\n"))
		}
	}()

	keys := []string{"j", "k", "h", "l", "w", "b", "e", "0", "$"}
	for i := range 200 {
		HandleCopyModeKey(key(keys[i%len(keys)]), o, win)
		if win.CopyMode == nil || !win.CopyMode.Active {
			win.EnterCopyMode()
		}
	}

	wg.Wait()
}

// TestCopyModeEffectsPreserveHandlerBehaviour is the behaviour-parity guard for
// the refactor. Routing side effects through copyModeEffects changed where they
// run, and must not have changed what they do: the notifications the handler
// emits, their order, the clipboard command it returns, and the mode
// transitions it performs.
//
// Without this, the lock narrowing could silently drop a notification or a
// clipboard write and every remaining test would still pass.
func TestCopyModeEffectsPreserveHandlerBehaviour(t *testing.T) {
	t.Run("quit exits copy mode and notifies", func(t *testing.T) {
		win := newCopyModeWindow(t, "copymode-fx-0001")
		o := &app.OS{Mode: app.WindowManagementMode}

		_, cmd := HandleCopyModeKey(key("q"), o, win)
		if cmd != nil {
			t.Fatal("quitting copy mode should not return a command")
		}
		if win.CopyMode != nil && win.CopyMode.Active {
			t.Fatal("q did not leave copy mode")
		}
		if !hasNotification(o, "Copy Mode Exited") {
			t.Fatalf("expected the exit notification, got %v", notificationMessages(o))
		}
	})

	t.Run("i enters terminal mode", func(t *testing.T) {
		win := newCopyModeWindow(t, "copymode-fx-0002")
		o := &app.OS{Mode: app.WindowManagementMode}

		HandleCopyModeKey(key("i"), o, win)
		if win.CopyMode != nil && win.CopyMode.Active {
			t.Fatal("i did not leave copy mode")
		}
		if o.Mode != app.TerminalMode {
			t.Fatalf("i left the OS in mode %v, want TerminalMode", o.Mode)
		}
		if !hasNotification(o, "Terminal Mode") {
			t.Fatalf("expected the terminal-mode notification, got %v", notificationMessages(o))
		}
	})

	t.Run("yank in visual mode returns a clipboard command", func(t *testing.T) {
		win := newCopyModeWindow(t, "copymode-fx-0003")
		o := &app.OS{Mode: app.WindowManagementMode}

		HandleCopyModeKey(key("v"), o, win)
		if win.CopyMode.State != terminal.CopyModeVisualChar {
			t.Fatalf("v left copy mode in state %v, want visual char", win.CopyMode.State)
		}
		HandleCopyModeKey(key("l"), o, win)

		_, cmd := HandleCopyModeKey(key("y"), o, win)
		if cmd == nil {
			t.Fatal("y in visual mode must return a clipboard command")
		}
		if win.CopyMode.State != terminal.CopyModeNormal {
			t.Fatal("y did not return to normal copy mode")
		}
		if !hasNotificationPrefix(o, "Yanked ") {
			t.Fatalf("expected a yank notification, got %v", notificationMessages(o))
		}
	})

	t.Run("count prefix notifies then clears", func(t *testing.T) {
		win := newCopyModeWindow(t, "copymode-fx-0004")
		o := &app.OS{Mode: app.WindowManagementMode}

		HandleCopyModeKey(key("5"), o, win)
		if win.CopyMode.PendingCount != 5 {
			t.Fatalf("PendingCount = %d, want 5", win.CopyMode.PendingCount)
		}
		if !hasNotification(o, "5") {
			t.Fatalf("expected the count notification, got %v", notificationMessages(o))
		}

		HandleCopyModeKey(key("j"), o, win)
		if win.CopyMode.PendingCount != 0 {
			t.Fatalf("PendingCount = %d after the motion, want 0", win.CopyMode.PendingCount)
		}
	})

	t.Run("search entry notifies with the prefix", func(t *testing.T) {
		win := newCopyModeWindow(t, "copymode-fx-0005")
		o := &app.OS{Mode: app.WindowManagementMode}

		HandleCopyModeKey(key("/"), o, win)
		if win.CopyMode.State != terminal.CopyModeSearch {
			t.Fatal("/ did not enter search state")
		}
		if !hasNotification(o, "/") {
			t.Fatalf("expected the search prompt notification, got %v", notificationMessages(o))
		}
	})
}

func notificationMessages(o *app.OS) []string {
	msgs := make([]string, 0, len(o.Notifications))
	for _, n := range o.Notifications {
		msgs = append(msgs, n.Message)
	}
	return msgs
}

func hasNotification(o *app.OS, want string) bool {
	for _, n := range o.Notifications {
		if n.Message == want {
			return true
		}
	}
	return false
}

func hasNotificationPrefix(o *app.OS, prefix string) bool {
	for _, n := range o.Notifications {
		if strings.HasPrefix(n.Message, prefix) {
			return true
		}
	}
	return false
}
