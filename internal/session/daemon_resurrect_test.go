package session

import (
	"os"
	"runtime"
	"slices"
	"testing"
)

// TestDaemonRestartRestoresWindows proves that windows saved before a daemon
// stops come back after a fresh daemon starts: the new daemon reads the saved
// state, recreates the session, and respawns a live PTY for every window in the
// window's saved cwd, clearly marked as restored.
func TestDaemonRestartRestoresWindows(t *testing.T) {
	tmpDir := t.TempDir()
	resurrectionDirOverride = tmpDir
	defer func() { resurrectionDirOverride = "" }()

	cwd1 := t.TempDir()
	cwd2 := t.TempDir()

	// Simulate the state written by a previous daemon just before shutdown: a
	// two-window, two-workspace session. (Writing the file directly stands in
	// for the periodic/Stop save of the prior daemon.)
	saved := &SessionState{
		Name:             "work",
		CurrentWorkspace: 1,
		AutoTiling:       true,
		Width:            120,
		Height:           40,
		Windows: []WindowState{
			{ID: "win-1", Title: "shell", X: 0, Y: 0, Width: 60, Height: 40, Workspace: 1, PTYID: "dead-pty-1", Cwd: cwd1},
			{ID: "win-2", Title: "editor", X: 60, Y: 0, Width: 60, Height: 40, Workspace: 2, PTYID: "dead-pty-2", Cwd: cwd2},
		},
	}
	if err := SaveSessionForResurrection(saved); err != nil {
		t.Fatalf("failed to save state: %v", err)
	}

	// Fresh daemon, empty manager: this is the "cold start".
	d := NewDaemon(&DaemonConfig{})
	d.restoreAllSessions()
	defer d.manager.Shutdown()

	sess := d.manager.GetSession("work")
	if sess == nil {
		t.Fatal("session 'work' was not restored")
	}

	state := sess.GetState()
	if len(state.Windows) != 2 {
		t.Fatalf("restored window count = %d, want 2", len(state.Windows))
	}

	// Workspaces preserved.
	workspaces := map[int]bool{}
	for _, w := range state.Windows {
		workspaces[w.Workspace] = true
	}
	if !workspaces[1] || !workspaces[2] {
		t.Errorf("restored windows lost their workspaces: %+v", state.Windows)
	}

	for _, w := range state.Windows {
		// PTY IDs must have been remapped away from the dead ones.
		if w.PTYID == "" || w.PTYID == "dead-pty-1" || w.PTYID == "dead-pty-2" {
			t.Errorf("window %s has stale/empty PTYID %q", w.ID, w.PTYID)
			continue
		}
		pty := sess.GetPTY(w.PTYID)
		if pty == nil {
			t.Errorf("no live PTY for restored window %s (PTYID %s)", w.ID, w.PTYID)
			continue
		}
		if pty.IsExited() {
			t.Errorf("restored PTY for window %s already exited", w.ID)
		}
		// Fresh shell must be marked restored.
		if !slices.Contains(pty.cmd.Env, "TUIOS_RESTORED=1") {
			t.Errorf("restored shell for window %s not marked (env: %v)", w.ID, pty.cmd.Env)
		}
	}

	// Working directories must have been honored (only reliable where the shell
	// can be started in a directory, which is all supported platforms).
	pty1 := sess.GetPTY(state.Windows[0].PTYID)
	if pty1 != nil && pty1.cmd.Dir != cwd1 {
		t.Errorf("window 1 shell dir = %q, want %q", pty1.cmd.Dir, cwd1)
	}
}

// TestDaemonRestoreSkipsLiveSession verifies restoreSession does not clobber a
// session that is already live.
func TestDaemonRestoreSkipsLiveSession(t *testing.T) {
	tmpDir := t.TempDir()
	resurrectionDirOverride = tmpDir
	defer func() { resurrectionDirOverride = "" }()

	d := NewDaemon(&DaemonConfig{})
	defer d.manager.Shutdown()

	existing, err := d.manager.CreateSession("live", &SessionConfig{}, 80, 24)
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	got, err := d.restoreSession(&SessionState{Name: "live", Width: 80, Height: 24})
	if err != nil {
		t.Fatalf("restoreSession failed: %v", err)
	}
	if got.ID != existing.ID {
		t.Error("restoreSession replaced a live session instead of returning it")
	}
}

// TestResurrectionStateCapturesCwd verifies that the daemon-side resurrection
// state enriches each window with its live shell's working directory (which
// clients never provide).
func TestResurrectionStateCapturesCwd(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("processCwd reads /proc, only reliable on Linux")
	}

	sess, err := NewSession("cwd-test", &SessionConfig{}, 80, 24)
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer sess.Stop()

	pty, err := sess.CreatePTY("win-1", 40, 20, nil)
	if err != nil {
		t.Fatalf("CreatePTY failed: %v", err)
	}

	sess.UpdateState(&SessionState{
		Name:    "cwd-test",
		Windows: []WindowState{{ID: "win-1", PTYID: pty.ID}},
	})

	state := sess.ResurrectionState()
	if len(state.Windows) != 1 {
		t.Fatalf("windows = %d, want 1", len(state.Windows))
	}
	if state.Windows[0].Cwd == "" {
		t.Error("ResurrectionState did not capture the shell cwd")
	}
	// The shell inherits the test process cwd.
	if wd, err := os.Getwd(); err == nil && state.Windows[0].Cwd != wd {
		t.Errorf("captured cwd = %q, want %q", state.Windows[0].Cwd, wd)
	}
}

// TestKillRemovesResurrectionState verifies an explicit kill deletes the saved
// state so the session is not resurrectable afterwards.
func TestKillRemovesResurrectionState(t *testing.T) {
	tmpDir := t.TempDir()
	resurrectionDirOverride = tmpDir
	defer func() { resurrectionDirOverride = "" }()

	mgr := NewManager()
	if _, err := mgr.CreateSession("doomed", &SessionConfig{}, 80, 24); err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	// Seed a saved-state file as the periodic saver would have.
	if err := SaveSessionForResurrection(&SessionState{Name: "doomed"}); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	if err := mgr.DeleteSession("doomed"); err != nil {
		t.Fatalf("DeleteSession failed: %v", err)
	}

	if _, err := LoadResurrectionState("doomed"); err == nil {
		t.Error("resurrection state should be removed after explicit kill")
	}

	names, err := ListResurrectableSessions()
	if err != nil {
		t.Fatalf("ListResurrectableSessions failed: %v", err)
	}
	if slices.Contains(names, "doomed") {
		t.Error("killed session still listed as resurrectable")
	}
}
