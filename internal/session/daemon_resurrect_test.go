package session

import (
	"os"
	"runtime"
	"testing"
)

// TestDaemonRestoreSkipsLiveSession verifies restoreSession does not clobber a
// session that is already live.
func TestDaemonRestoreSkipsLiveSession(t *testing.T) {
	tmpDir := t.TempDir()
	defer useResurrectionDir(tmpDir)()

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
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("ptyspawn.ProcessCwd has an answer only on Linux and darwin")
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
