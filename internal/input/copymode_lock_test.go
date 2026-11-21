package input

import (
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

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
