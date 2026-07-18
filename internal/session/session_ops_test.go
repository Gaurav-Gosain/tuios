package session

import (
	"testing"
)

// newTestSession creates a session backed by a real shell for the state-op tests.
func newTestSession(t *testing.T) *Session {
	t.Helper()
	sess, err := NewSession("ops-test", &SessionConfig{}, 80, 24)
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	t.Cleanup(sess.Stop)
	return sess
}

func TestAddDaemonWindow(t *testing.T) {
	sess := newTestSession(t)

	win, err := sess.AddDaemonWindow("shell", nil)
	if err != nil {
		t.Fatalf("AddDaemonWindow failed: %v", err)
	}
	if win.ID == "" {
		t.Fatal("created window has empty ID")
	}
	if win.PTYID == "" {
		t.Fatal("created window has empty PTYID")
	}
	if win.Workspace != 1 {
		t.Errorf("workspace = %d, want 1", win.Workspace)
	}

	// The PTY must be live and registered on the session.
	if pty := sess.GetPTY(win.PTYID); pty == nil {
		t.Fatal("PTY not registered on session")
	} else if pty.IsExited() {
		t.Fatal("freshly created PTY already exited")
	}

	state := sess.GetState()
	if len(state.Windows) != 1 {
		t.Fatalf("window count = %d, want 1", len(state.Windows))
	}
	if state.FocusedWindowID != win.ID {
		t.Errorf("focus = %q, want %q", state.FocusedWindowID, win.ID)
	}
	if state.WorkspaceFocus[1] != win.ID {
		t.Errorf("workspace focus[1] = %q, want %q", state.WorkspaceFocus[1], win.ID)
	}
}

func TestCloseDaemonWindow(t *testing.T) {
	sess := newTestSession(t)

	w1, _ := sess.AddDaemonWindow("one", nil)
	w2, _ := sess.AddDaemonWindow("two", nil)

	// Close the focused (second) window; focus must fall back to the first.
	closedID, err := sess.CloseDaemonWindow(w2.ID)
	if err != nil {
		t.Fatalf("CloseDaemonWindow failed: %v", err)
	}
	if closedID != w2.ID {
		t.Errorf("closed ID = %q, want %q", closedID, w2.ID)
	}

	state := sess.GetState()
	if len(state.Windows) != 1 {
		t.Fatalf("window count = %d, want 1", len(state.Windows))
	}
	if state.FocusedWindowID != w1.ID {
		t.Errorf("focus after close = %q, want %q", state.FocusedWindowID, w1.ID)
	}
	if sess.GetPTY(w2.PTYID) != nil {
		t.Error("closed window's PTY is still registered")
	}
}

func TestCloseDaemonWindowByName(t *testing.T) {
	sess := newTestSession(t)

	w1, _ := sess.AddDaemonWindow("", nil)
	if err := sess.RenameDaemonWindow(w1.ID, "build"); err != nil {
		t.Fatalf("RenameDaemonWindow failed: %v", err)
	}

	if _, err := sess.CloseDaemonWindow("build"); err != nil {
		t.Fatalf("CloseDaemonWindow by name failed: %v", err)
	}
	if len(sess.GetState().Windows) != 0 {
		t.Error("window not removed when closed by name")
	}
}

func TestFocusAndCycleDaemonWindows(t *testing.T) {
	sess := newTestSession(t)

	w1, _ := sess.AddDaemonWindow("one", nil)
	w2, _ := sess.AddDaemonWindow("two", nil)
	w3, _ := sess.AddDaemonWindow("three", nil)

	if err := sess.FocusDaemonWindow(w1.ID); err != nil {
		t.Fatalf("FocusDaemonWindow failed: %v", err)
	}
	if sess.GetState().FocusedWindowID != w1.ID {
		t.Fatalf("focus = %q, want %q", sess.GetState().FocusedWindowID, w1.ID)
	}

	// Next wraps forward: w1 -> w2 -> w3 -> w1.
	if err := sess.CycleDaemonFocus(1); err != nil {
		t.Fatalf("CycleDaemonFocus failed: %v", err)
	}
	if got := sess.GetState().FocusedWindowID; got != w2.ID {
		t.Errorf("after next, focus = %q, want %q", got, w2.ID)
	}

	// Prev wraps backward from w2 to w1.
	if err := sess.CycleDaemonFocus(-1); err != nil {
		t.Fatalf("CycleDaemonFocus(-1) failed: %v", err)
	}
	if got := sess.GetState().FocusedWindowID; got != w1.ID {
		t.Errorf("after prev, focus = %q, want %q", got, w1.ID)
	}

	// Prev from the first window wraps to the last.
	if err := sess.CycleDaemonFocus(-1); err != nil {
		t.Fatalf("CycleDaemonFocus(-1) wrap failed: %v", err)
	}
	if got := sess.GetState().FocusedWindowID; got != w3.ID {
		t.Errorf("after wrap prev, focus = %q, want %q", got, w3.ID)
	}
}

func TestMoveAndSwitchWorkspace(t *testing.T) {
	sess := newTestSession(t)

	w1, _ := sess.AddDaemonWindow("one", nil)

	if err := sess.MoveDaemonWindowToWorkspace(w1.ID, 3); err != nil {
		t.Fatalf("MoveDaemonWindowToWorkspace failed: %v", err)
	}
	state := sess.GetState()
	if state.Windows[0].Workspace != 3 {
		t.Errorf("window workspace = %d, want 3", state.Windows[0].Workspace)
	}

	if err := sess.SwitchDaemonWorkspace(3); err != nil {
		t.Fatalf("SwitchDaemonWorkspace failed: %v", err)
	}
	if sess.GetState().CurrentWorkspace != 3 {
		t.Errorf("current workspace = %d, want 3", sess.GetState().CurrentWorkspace)
	}

	// Out-of-range workspaces are rejected.
	if err := sess.SwitchDaemonWorkspace(0); err == nil {
		t.Error("expected error for workspace 0")
	}
	if err := sess.MoveDaemonWindowToWorkspace(w1.ID, 99); err == nil {
		t.Error("expected error for workspace 99")
	}
}

func TestMinimizeDaemonWindow(t *testing.T) {
	sess := newTestSession(t)

	w1, _ := sess.AddDaemonWindow("one", nil)
	if err := sess.SetDaemonWindowMinimized(w1.ID, true); err != nil {
		t.Fatalf("SetDaemonWindowMinimized failed: %v", err)
	}
	if !sess.GetState().Windows[0].Minimized {
		t.Error("window not minimized")
	}
	if err := sess.SetDaemonWindowMinimized(w1.ID, false); err != nil {
		t.Fatalf("SetDaemonWindowMinimized(false) failed: %v", err)
	}
	if sess.GetState().Windows[0].Minimized {
		t.Error("window still minimized after restore")
	}
}

func TestFindWindowStateIndexAmbiguity(t *testing.T) {
	windows := []WindowState{
		{ID: "aaa-1", CustomName: "dup"},
		{ID: "aaa-2", CustomName: "dup"},
		{ID: "bbb-3", Title: "unique"},
	}

	if _, err := findWindowStateIndex(windows, "aaa"); err == nil {
		t.Error("expected ambiguous prefix error")
	}
	if _, err := findWindowStateIndex(windows, "dup"); err == nil {
		t.Error("expected ambiguous name error")
	}
	if idx, err := findWindowStateIndex(windows, "bbb-3"); err != nil || idx != 2 {
		t.Errorf("exact ID match: idx=%d err=%v", idx, err)
	}
	if idx, err := findWindowStateIndex(windows, "unique"); err != nil || idx != 2 {
		t.Errorf("title match: idx=%d err=%v", idx, err)
	}
	if _, err := findWindowStateIndex(windows, "nope"); err == nil {
		t.Error("expected no-match error")
	}
}
