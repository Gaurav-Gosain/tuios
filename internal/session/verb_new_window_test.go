package session

import (
	"encoding/json"
	"net"
	"testing"
	"time"
)

// collectStateSyncs drains the pushes the daemon sends a fake TUI so a test can
// assert the client was told about a mutation rather than left to discover it on
// its next sync.
func collectStateSyncs(clientSide net.Conn) <-chan *SessionState {
	pushed := make(chan *SessionState, 8)
	go func() {
		for {
			msg, _, err := ReadMessageWithCodec(clientSide)
			if err != nil {
				return
			}
			if msg.Type != MsgStateSync {
				continue
			}
			var p StateSyncPayload
			if err := msg.ParsePayloadWithCodec(&p, DefaultCodec()); err == nil {
				pushed <- p.State
			}
		}
	}()
	return pushed
}

// TestNewWindowWithAttachedTUIRunsOnTheDaemon pins the converged create. Creating
// used to be routed to the attached client, so the window existed in the renderer
// first and in daemon state only when that client got round to syncing back, and
// the verb could fail with command_failed purely because the renderer was busy.
// The daemon now spawns the PTY and adds the window itself, and tells the client.
func TestNewWindowWithAttachedTUIRunsOnTheDaemon(t *testing.T) {
	d := NewDaemon(&DaemonConfig{Version: "test", DisableAutoRestore: true})
	defer d.manager.Shutdown()

	sess, err := d.manager.CreateSession("creating", &SessionConfig{}, 80, 24)
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	_, clientSide := newFakeTUI(t, d, sess.ID)
	pushed := collectStateSyncs(clientSide)

	out, verr := d.verbNewWindow(nil, json.RawMessage(`{"session":"creating","name":"build"}`))
	if verr != nil {
		t.Fatalf("verbNewWindow: %v", verr)
	}
	res := out.(map[string]any)
	if res["type"] != "window_created" {
		t.Fatalf("result = %v, want window_created", res)
	}
	windowID, _ := res["window_id"].(string)
	if windowID == "" {
		t.Fatal("verbNewWindow returned no window_id")
	}

	// The daemon holds the window, immediately, with no client round trip.
	state := sess.GetState()
	if len(state.Windows) != 1 {
		t.Fatalf("daemon state holds %d windows, want 1", len(state.Windows))
	}
	win := state.Windows[0]
	if win.ID != windowID {
		t.Errorf("daemon window ID = %q, want %q", win.ID, windowID)
	}
	if win.CustomName != "build" {
		t.Errorf("CustomName = %q, want build", win.CustomName)
	}
	if win.PTYID == "" {
		t.Fatal("created window has no PTY")
	}
	if pty := sess.GetPTY(win.PTYID); pty == nil || pty.IsExited() {
		t.Error("the created window's PTY is not running")
	}
	if state.FocusedWindowID != windowID {
		t.Errorf("FocusedWindowID = %q, want the new window", state.FocusedWindowID)
	}

	// And the attached client was told, with the window marked as needing a
	// position, because the daemon has no viewport to have chosen one.
	deadline := time.After(3 * time.Second)
	for {
		select {
		case state := <-pushed:
			for i := range state.Windows {
				if state.Windows[i].ID != windowID {
					continue
				}
				if !state.Windows[i].Unplaced {
					t.Error("the pushed window is not marked Unplaced, so no client will place it")
				}
				return
			}
		case <-deadline:
			t.Fatal("the attached client was never told the window was created")
		}
	}
}

// TestDaemonCreatedWindowIsUnplaced states the contract the client relies on: a
// window the daemon made carries a usable box so its PTY has a sane size, and
// says outright that the box is a placeholder rather than a position.
func TestDaemonCreatedWindowIsUnplaced(t *testing.T) {
	sess := newTestSession(t)

	win, err := sess.AddDaemonWindow("", nil)
	if err != nil {
		t.Fatalf("AddDaemonWindow failed: %v", err)
	}
	if !win.Unplaced {
		t.Error("a daemon-created window must be marked Unplaced")
	}
	if win.Width <= 0 || win.Height <= 0 {
		t.Errorf("placeholder box is %dx%d, want a usable size", win.Width, win.Height)
	}
	if win.Title == "" {
		t.Error("a daemon-created window must carry a default title")
	}
}

// TestClientSyncClearsUnplaced covers the other half of the handshake. A client
// answers the placement question by pushing geometry, and its snapshots never set
// Unplaced, so the flag clears itself. Nothing in the merge may resurrect it.
func TestClientSyncClearsUnplaced(t *testing.T) {
	sess := newTestSession(t)

	win, err := sess.AddDaemonWindow("", nil)
	if err != nil {
		t.Fatalf("AddDaemonWindow failed: %v", err)
	}

	snap := sess.GetState()
	placed := &SessionState{
		Name:        snap.Name,
		BaseVersion: snap.Version,
		Windows: []WindowState{{
			ID:     win.ID,
			PTYID:  win.PTYID,
			X:      10,
			Y:      5,
			Width:  40,
			Height: 12,
		}},
		FocusedWindowID: win.ID,
	}
	if !sess.UpdateState(placed) {
		t.Fatal("a current client sync was not accepted")
	}

	got := sess.GetState().Windows[0]
	if got.Unplaced {
		t.Error("Unplaced survived a client sync, so the client would place the window again on every push")
	}
	if got.X != 10 || got.Y != 5 || got.Width != 40 || got.Height != 12 {
		t.Errorf("geometry = %d,%d %dx%d, want the client's 10,5 40x12", got.X, got.Y, got.Width, got.Height)
	}
}
