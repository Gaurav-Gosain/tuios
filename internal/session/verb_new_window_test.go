package session

import (
	"net"
	"testing"
)

// collectStateSyncs drains the pushes the daemon sends a fake TUI so a test can
// assert the client was told about a mutation rather than left to discover it on
// its next sync.
func collectStateSyncs(clientSide net.Conn) <-chan *SessionState {
	pushed := make(chan *SessionState, 8)
	go func() {
		for {
			msg, err := ReadMessage(clientSide)
			if err != nil {
				return
			}
			if msg.Type != MsgStateSync {
				continue
			}
			var p StateSyncPayload
			if err := msg.ParsePayload(&p); err == nil {
				pushed <- p.State
			}
		}
	}()
	return pushed
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
