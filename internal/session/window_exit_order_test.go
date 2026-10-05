package session

import (
	"sync"
	"testing"
)

// TestOnExitSeesTheWindowOfAProcessThatExitedAtOnce: a command can exit
// before AddDaemonWindowWith has added its window to the state. onExit looks
// the window up by its PTY (close_on_exit and plugin panes close it that
// way), so it must not run until the window is there. Before the fix it ran
// at once, found nothing, and the window stayed open around a dead process.
//
// The hook holds the add until the process has exited, so the order that
// broke is the order this test runs every time.
func TestOnExitSeesTheWindowOfAProcessThatExitedAtOnce(t *testing.T) {
	d, _ := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "exit-order")

	windowSpawnedHook = func(p *PTY) {
		waitFor(t, "the process to exit", p.IsExited)
	}
	t.Cleanup(func() { windowSpawnedHook = nil })

	var (
		mu     sync.Mutex
		called bool
		found  bool
	)
	onExit := func(ptyID string) {
		in := false
		for _, w := range sess.GetState().Windows {
			if w.PTYID == ptyID {
				in = true
			}
		}
		mu.Lock()
		called, found = true, in
		mu.Unlock()
	}
	if _, err := sess.AddDaemonWindowWith(NewWindowOptions{Command: []string{"sh", "-c", "exit 0"}}, onExit); err != nil {
		t.Fatalf("AddDaemonWindowWith: %v", err)
	}
	windowSpawnedHook = nil
	waitFor(t, "onExit to run", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return called
	})
	mu.Lock()
	defer mu.Unlock()
	if !found {
		t.Error("onExit ran before the window was in the state, so a close_on_exit window stays open")
	}
}
