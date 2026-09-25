package session

import (
	"os"
	"testing"
)

// TestEmptySavedSessionDoesNotResurrect covers the session whose windows were
// all closed before the daemon went away. Its state file survives with an empty
// window list, and restoring it produced a live session containing nothing,
// which no surface distinguishes from a real one.
func TestEmptySavedSessionDoesNotResurrect(t *testing.T) {
	tmpDir := t.TempDir()
	defer useResurrectionDir(tmpDir)()

	empty := &SessionState{Name: "hollow", Width: 120, Height: 40, Windows: []WindowState{}}
	if err := SaveSessionForResurrection(empty); err != nil {
		t.Fatalf("failed to save state: %v", err)
	}

	cwd := t.TempDir()
	real := &SessionState{
		Name: "work", Width: 120, Height: 40,
		Windows: []WindowState{{ID: "win-1", Width: 60, Height: 40, Workspace: 1, Cwd: cwd}},
	}
	if err := SaveSessionForResurrection(real); err != nil {
		t.Fatalf("failed to save state: %v", err)
	}

	d := NewDaemon(&DaemonConfig{})
	defer d.manager.Shutdown()
	d.restoreAllSessions()

	if sess := d.manager.GetSession("hollow"); sess != nil {
		t.Errorf("an empty saved session came back with %d windows; nothing tells it from a real one",
			len(sess.GetState().Windows))
	}
	// A state file that can never restore must not go on being offered.
	if _, err := os.Stat(getResurrectionPath("hollow")); !os.IsNotExist(err) {
		t.Errorf("the unrestorable state file for 'hollow' is still on disk (stat err: %v)", err)
	}

	// The empty one must not have taken the real one down with it.
	if d.manager.GetSession("work") == nil {
		t.Error("a real session beside the empty one was not restored")
	}
}

// TestOnDemandResurrectRefusesAnEmptySession covers the same refusal on the
// 'tuios resurrect <name>' path, which loads the state itself.
func TestOnDemandResurrectRefusesAnEmptySession(t *testing.T) {
	tmpDir := t.TempDir()
	defer useResurrectionDir(tmpDir)()

	d := NewDaemon(&DaemonConfig{})
	defer d.manager.Shutdown()

	if _, err := d.restoreSession(&SessionState{Name: "hollow", Windows: nil}); err == nil {
		t.Fatal("restoring a session with no windows succeeded; it creates a session containing nothing")
	}
	if d.manager.GetSession("hollow") != nil {
		t.Error("the refused session was registered anyway")
	}
}
