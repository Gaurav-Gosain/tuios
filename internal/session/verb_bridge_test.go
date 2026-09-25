package session

import (
	"encoding/json"
	"net"
	"testing"
	"time"
)

func pendingCount(d *Daemon) int {
	d.pendingRequestsMu.RLock()
	defer d.pendingRequestsMu.RUnlock()
	return len(d.pendingRequests)
}

func TestRouteToTUISyncTimeout(t *testing.T) {
	d := NewDaemon(&DaemonConfig{Version: "test", DisableAutoRestore: true})
	defer d.manager.Shutdown()

	tui, clientSide := newFakeTUI(t, d, "sess-2")
	// Drain the command but never reply.
	go func() { _, _ = ReadMessage(clientSide) }()

	_, err := d.routeToTUISync(tui, "req-timeout",
		&RemoteCommandPayload{CommandType: "send_keys", Keys: "x"},
		150*time.Millisecond)
	if err == nil {
		t.Fatal("expected a timeout error")
	}
	if n := pendingCount(d); n != 0 {
		t.Errorf("pending requests not cleaned up after timeout: %d", n)
	}
}

// TestRenameWindowWithAttachedTUIUpdatesDaemonState reproduces a bug a user hits
// today: with a TUI attached, "tuios run-command RenameWindow" was routed to the
// client, which renamed its own copy of the window and reported success. The
// daemon's state, which every read verb answers from, kept the old name, so the
// rename reported success and list-windows still showed the old name.
//
// The rename is a change to a field the daemon owns, so the daemon performs it.
func TestRenameWindowWithAttachedTUIUpdatesDaemonState(t *testing.T) {
	d := NewDaemon(&DaemonConfig{Version: "test", DisableAutoRestore: true})
	defer d.manager.Shutdown()

	sess := makeSessionWithWindow(t, d, "renamed")
	win := sess.GetState().Windows[0]

	_, clientSide := newFakeTUI(t, d, sess.ID)
	// The client-side pipe must be drained or a daemon push would block.
	go func() {
		for {
			if _, err := ReadMessage(clientSide); err != nil {
				return
			}
		}
	}()

	requester := &connState{
		conn: newDiscardConn(t), clientID: "ctl",
		done: make(chan struct{}),
	}
	msg, err := NewMessage(MsgExecuteCommand, &ExecuteCommandPayload{
		RequestID:   "req-rename",
		SessionName: "renamed",
		CommandType: "RenameWindow",
		Args:        []string{win.ID, "build"},
	})
	if err != nil {
		t.Fatalf("NewMessage: %v", err)
	}
	if err := d.handleExecuteCommand(requester, msg); err != nil {
		t.Fatalf("handleExecuteCommand: %v", err)
	}

	got := sess.GetState().Windows[0]
	if got.CustomName != "build" {
		t.Fatalf("daemon state CustomName = %q, want %q: a rename that reports success "+
			"but leaves daemon state stale makes list-windows report the old name", got.CustomName, "build")
	}

	// The window list is what the user actually reads back.
	data := buildWindowListData(sess.GetState())
	windows, ok := data["windows"].([]map[string]any)
	if !ok || len(windows) != 1 {
		t.Fatalf("window list = %v", data["windows"])
	}
	if windows[0]["display_name"] != "build" {
		t.Errorf("list-windows display_name = %v, want build", windows[0]["display_name"])
	}

}

// newDiscardConn returns a connection whose writes go nowhere, for a fake
// requester that never reads its replies.
func newDiscardConn(t *testing.T) net.Conn {
	t.Helper()
	a, b := net.Pipe()
	go func() {
		buf := make([]byte, 4096)
		for {
			if _, err := b.Read(buf); err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() { _ = a.Close(); _ = b.Close() })
	return a
}

// TestCloseWindowWithAttachedTUIRunsOnTheDaemon pins the converged close. Closing
// used to be routed to the attached client, which meant a close needed a live
// renderer to round-trip through, could fail with command_failed when that
// renderer was busy, and left the daemon holding the window until the client
// happened to sync back. Closing is now the daemon's: it removes the window,
// kills the PTY, and tells the client what it did.
func TestCloseWindowWithAttachedTUIRunsOnTheDaemon(t *testing.T) {
	d := NewDaemon(&DaemonConfig{Version: "test", DisableAutoRestore: true})
	defer d.manager.Shutdown()

	sess := makeSessionWithWindow(t, d, "closing")
	win := sess.GetState().Windows[0]

	_, clientSide := newFakeTUI(t, d, sess.ID)
	// Collect what the daemon pushes at the client, so the test can assert the
	// client was told rather than left to find out on its next sync.
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

	out, verr := d.verbCloseWindow(nil, json.RawMessage(`{"session":"closing","window":"`+win.ID+`"}`))
	if verr != nil {
		t.Fatalf("verbCloseWindow: %v", verr)
	}
	if m := out.(map[string]any); m["type"] != "ok" {
		t.Fatalf("result = %v, want ok", m)
	}

	if got := len(sess.GetState().Windows); got != 0 {
		t.Errorf("daemon state still holds %d windows after close", got)
	}
	if pty := sess.GetPTY(win.PTYID); pty != nil && !pty.IsExited() {
		t.Error("the closed window's PTY is still running")
	}

	select {
	case state := <-pushed:
		if len(state.Windows) != 0 {
			t.Errorf("pushed state still holds %d windows", len(state.Windows))
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the attached client was never told the window closed")
	}
}
