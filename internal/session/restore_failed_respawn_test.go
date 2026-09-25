package session

import (
	"testing"
)

// TestFailedRespawnDropsTheWindowRatherThanLeavingItPointingAtNothing covers the
// restore path's one unhandled failure. When a window's shell will not start,
// the window used to be kept with the PTY ID of the previous daemon's process:
// an id nothing answers to, on a pane that can neither print nor be typed into
// and that no surface draws as dead.
func TestFailedRespawnDropsTheWindowRatherThanLeavingItPointingAtNothing(t *testing.T) {
	tmpDir := t.TempDir()
	defer useResurrectionDir(tmpDir)()

	// Every shell in this restore fails to exec, so every window's respawn fails.
	t.Setenv("SHELL", "/nonexistent/definitely-not-a-shell")

	saved := &SessionState{
		Name:            "work",
		Width:           120,
		Height:          40,
		FocusedWindowID: "win-1",
		WorkspaceFocus:  map[int]string{1: "win-1"},
		Windows: []WindowState{
			{ID: "win-1", Title: "shell", Width: 60, Height: 40, Workspace: 1, PTYID: "dead-pty-1"},
			{ID: "win-2", Title: "editor", Width: 60, Height: 40, Workspace: 1, PTYID: "dead-pty-2"},
		},
	}
	if err := SaveSessionForResurrection(saved); err != nil {
		t.Fatalf("failed to save state: %v", err)
	}

	d := NewDaemon(&DaemonConfig{})
	defer d.manager.Shutdown()
	d.restoreAllSessions()

	sess := d.manager.GetSession("work")
	if sess == nil {
		// An empty restore is refused outright, which is also an acceptable
		// answer to "every window failed"; either way no pane survives.
		return
	}

	for _, w := range sess.GetState().Windows {
		if w.PTYID == "dead-pty-1" || w.PTYID == "dead-pty-2" {
			t.Errorf("window %s kept the dead daemon's PTY id %q; the pane points at nothing",
				w.ID, w.PTYID)
		}
		if sess.GetPTY(w.PTYID) == nil {
			t.Errorf("window %s survived the restore with PTY id %q that no live PTY answers to",
				w.ID, w.PTYID)
		}
	}

	state := sess.GetState()
	if state.FocusedWindowID != "" && !hasWindow(state.Windows, state.FocusedWindowID) {
		t.Errorf("focus points at %q, which is not one of the restored windows", state.FocusedWindowID)
	}
	for ws, id := range state.WorkspaceFocus {
		if !hasWindow(state.Windows, id) {
			t.Errorf("workspace %d focuses %q, which is not one of the restored windows", ws, id)
		}
	}
}
