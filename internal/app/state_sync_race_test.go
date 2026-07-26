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
//
// Its detection power has been verified rather than assumed: compiled against
// the parent commit b0cd5e9, which lacks the LockIO calls, it fails with a
// reported race on the emulator cell buffer, and it passes with them. Keep it
// that way. If a later refactor makes it pass on unlocked code it has stopped
// guarding anything.
//
// Note for anyone extending this coverage: the black box tuitest scenarios that
// drive a real binary and assert on rendered pane text do NOT have this
// property. A focus-cycling scenario of that kind passes against both the
// locked and unlocked builds, because a torn buffer does not reliably surface
// as missing text within the run. The race detector is the mechanism that
// works here, so express regressions of this class as -race tests.
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

// TestPlaceUnplacedWindowsRacesOutput is the sibling of the test above for the
// other unlocked emulator resize on the state-sync path.
//
// A window the daemon creates arrives marked Unplaced, because the daemon has
// no viewport and will not guess a position. placeUnplacedWindows turns that
// into a real box on the client, and resizing the emulator to the new box was
// the one resize on this path that did not take the window's I/O lock. By the
// time the placing sync arrives the window is already subscribed, so the
// outputWriter goroutine is writing the same cell buffer that Resize is
// reallocating: the symptom is the same permanently blank pane, since a torn
// render is cached and an idle shell never re-dirties it.
//
// The trigger on a real session is pressing n, or any other route that has the
// daemon rather than this client create the window.
//
// Like the test above this asserts nothing and only fails under -race. Its
// detection power was verified by running it against the unlocked code, where
// it reports a race on the emulator buffer inside placeUnplacedWindows.
func TestPlaceUnplacedWindowsRacesOutput(t *testing.T) {
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

	const winID = "place-race-window-001"
	win := terminal.NewDaemonWindow(winID, "race", 0, 0, 60, 20, 0, "pty-place-0001", ptyDataChan)
	if win == nil {
		t.Fatal("NewDaemonWindow returned nil")
	}

	m := &OS{
		Windows:        []*terminal.Window{win},
		FocusedWindow:  0,
		WorkspaceFocus: map[int]int{},
		NumWorkspaces:  9,
		Width:          120,
		Height:         40,
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})

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

	// The daemon re-broadcasts the creation state until this client's placing
	// push lands, so Unplaced arriving repeatedly is the real shape. The screen
	// size alternates so NewWindowPlacement returns a different box each time
	// and the emulator really reallocates rather than taking Buffer.Resize's
	// same-dimensions early return.
	for i := range 300 {
		m.Width, m.Height = 120, 40
		if i%2 == 0 {
			m.Width, m.Height = 100, 30
		}
		state := &session.SessionState{
			Name:             "race",
			CurrentWorkspace: 1,
			FocusedWindowID:  winID,
			Windows: []session.WindowState{{
				ID:        winID,
				Title:     "race",
				PTYID:     "pty-place-0001",
				X:         0,
				Y:         0,
				Width:     60,
				Height:    20,
				Workspace: 1,
				Unplaced:  true,
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
