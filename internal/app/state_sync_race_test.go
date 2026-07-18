package app

import (
	"sync"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// TestApplyStateSyncResizeRacesOutput is the regression test for daemon windows
// going blank when focus moves to another pane.
//
// The terminal emulator has no lock of its own. Every other resize path takes
// the window's I/O lock (Window.Resize, ToggleZoom) because the daemon
// outputWriter goroutine writes the cell buffer under that lock and the
// renderer reads it under RLockIO. updateWindowFromState resized the emulator
// without the lock, and ultraviolet's Buffer.Resize reallocates every line, so
// a sync landing mid-write or mid-render tore the buffer and the pane rendered
// as empty cells. renderTerminal then cached that empty render, and an idle
// shell emits nothing to re-dirty it, so the pane stayed blank.
//
// The trigger is a focus change: input.HandleInput calls SyncStateToDaemon
// after any input in a daemon session, the daemon broadcasts the new state
// back, and ApplyStateSync feeds it through updateWindowFromState. The resize
// only runs when the geometry actually changed, which is why it was
// intermittent rather than constant.
//
// This is a race-detector test. It asserts nothing itself and only fails under
// -race, where the unsynchronized buffer access is reported.
func TestApplyStateSyncResizeRacesOutput(t *testing.T) {
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
	defer close(drainDone)

	const winID = "sync-race-window-0001"
	win := terminal.NewDaemonWindow(winID, "race", 0, 0, 60, 20, 0, "pty-sync-0001", ptyDataChan)
	if win == nil {
		t.Fatal("NewDaemonWindow returned nil")
	}

	m := &OS{
		Windows:        []*terminal.Window{win},
		FocusedWindow:  0,
		WorkspaceFocus: map[int]int{},
		NumWorkspaces:  9,
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Background output, as the daemon read loop delivers it. The newlines force
	// scrolling, which is what mutates the buffer under the resize.
	payload := []byte("the quick brown fox jumps over the lazy dog 0123456789\r\n")
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					win.WriteOutputAsync(payload)
				}
			}
		}()
	}

	// The UI goroutine: apply syncs whose geometry alternates, so every sync
	// takes the sizeChanged branch, and render between them as the frame loop
	// does.
	for i := range 300 {
		w, h := 40, 14
		if i%2 == 0 {
			w, h = 60, 20
		}
		state := &session.SessionState{
			Name:             "race",
			CurrentWorkspace: 1,
			FocusedWindowID:  winID,
			Windows: []session.WindowState{{
				ID:        winID,
				Title:     "race",
				PTYID:     "pty-sync-0001",
				X:         0,
				Y:         0,
				Width:     w,
				Height:    h,
				Workspace: 1,
			}},
		}
		if err := m.ApplyStateSync(state); err != nil {
			t.Fatalf("ApplyStateSync: %v", err)
		}
		_ = m.renderTerminal(win, i%2 == 0, false)
	}

	close(stop)
	wg.Wait()
	win.Close()
}
