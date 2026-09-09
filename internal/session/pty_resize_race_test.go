package session

import (
	"sync"
	"testing"
	"time"
)

// TestConcurrentResizesLeaveTheEmulatorAtTheRecordedSize pins the daemon's
// answer to two clients resizing one pane at once. Each client announces the
// size it laid the pane out at on its own connection, so the daemon meets the
// two in whichever order the sockets deliver them. Whichever it records last
// is the pane's size, and the emulator has to end up at that size too: a
// snapshot handed to the next client is laid out by the emulator, and a later
// announcement of the recorded size is declined as unchanged, so an emulator
// left at the other size stays there.
//
// NEGATIVE CONTROL: with PTY.Resize recording the size under terminalMu and
// queueing the emulator resize under streamMu as two separate critical
// sections, this fails with the pane recording one size and the emulator at
// the other.
func TestConcurrentResizesLeaveTheEmulatorAtTheRecordedSize(t *testing.T) {
	_, sess := newTestDaemonSession(t)
	pty, err := sess.CreatePTY("win-resize-race", 65, 38, func(string) {})
	if err != nil {
		t.Fatalf("CreatePTY failed: %v", err)
	}

	sizes := [][2]int{{22, 16}, {26, 16}}
	for round := range 2000 {
		var wg sync.WaitGroup
		for _, size := range sizes {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_ = pty.Resize(size[0], size[1])
			}()
		}
		wg.Wait()

		w, h := pty.Size()
		deadline := time.Now().Add(2 * time.Second)
		for {
			pty.terminalMu.RLock()
			ew, eh := pty.terminal.Width(), pty.terminal.Height()
			pty.terminalMu.RUnlock()
			if ew == w && eh == h {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("round %d: the pane records %dx%d and its emulator is at %dx%d; "+
					"two resizes were recorded in one order and queued in the other",
					round, w, h, ew, eh)
			}
			time.Sleep(time.Millisecond)
		}
	}
}
