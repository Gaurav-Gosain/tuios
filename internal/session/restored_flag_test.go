package session

import (
	"testing"
)

// The restored mark is the only thing at session level that distinguishes a
// session the daemon rebuilt from saved state from one that has been alive for
// days. These tests pin the two halves of its life: it is set by a restore and
// only by a restore, and the first attach takes it off for good.

// TestRestoredSessionIsMarkedAndAFreshOneIsNot is the headline: a session that
// came back from disk says so, and an ordinary new session says nothing.
func TestRestoredSessionIsMarkedAndAFreshOneIsNot(t *testing.T) {
	tmpDir := t.TempDir()
	defer useResurrectionDir(tmpDir)()

	cwd := t.TempDir()
	saved := &SessionState{
		Name:   "work",
		Width:  120,
		Height: 40,
		Windows: []WindowState{
			{ID: "win-1", Title: "shell", Width: 60, Height: 40, Workspace: 1, PTYID: "dead-pty-1", Cwd: cwd},
		},
	}
	if err := SaveSessionForResurrection(saved); err != nil {
		t.Fatalf("failed to save state: %v", err)
	}

	d := NewDaemon(&DaemonConfig{})
	d.restoreAllSessions()
	defer d.manager.Shutdown()

	restored := d.manager.GetSession("work")
	if restored == nil {
		t.Fatal("session 'work' was not restored")
	}
	if !restored.GetState().Restored {
		t.Error("a session rebuilt from saved state is not marked restored, so no surface can say why it is here")
	}
	if !restored.Info().Restored {
		t.Error("the restored mark does not reach the session listing, so 'tuios ls' and the rail cannot show it")
	}

	fresh, err := d.manager.CreateSession("brand-new", &SessionConfig{}, 80, 24)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if fresh.GetState().Restored {
		t.Error("a freshly created session is marked restored")
	}
	if fresh.Info().Restored {
		t.Error("a freshly created session is listed as restored")
	}
}

// TestClearingTheRestoredMarkDoesNotPushState pins the reason the clear is not
// published. It runs inside the attach handler, after the connection's session
// is recorded, so a push would reach the attaching client on the same socket
// ahead of the attach reply it is blocked on, and that client fails the attach
// with "unexpected response" instead of opening.
func TestClearingTheRestoredMarkDoesNotPushState(t *testing.T) {
	tmpDir := t.TempDir()
	defer useResurrectionDir(tmpDir)()

	sess := newTestSession(t)
	sess.MarkRestored()

	pushes := 0
	sess.SetStateSink(func(*SessionState) { pushes++ })

	sess.ClearRestored()

	if pushes != 0 {
		t.Errorf("clearing the restored mark pushed state %d times; it would outrun the attach reply", pushes)
	}
	if sess.GetState().Restored {
		t.Error("the mark was not cleared")
	}
}

// TestRestoredMarkClearsOnFirstAttachAndDoesNotComeBack proves the mark is
// spent once someone looks at the session, and that a client state sync cannot
// resurrect it: the field is daemon-owned in both directions.
func TestRestoredMarkClearsOnFirstAttachAndDoesNotComeBack(t *testing.T) {
	d, _ := startTestDaemon(t)

	cwd := t.TempDir()
	saved := &SessionState{
		Name:   "work",
		Width:  80,
		Height: 24,
		Windows: []WindowState{
			{ID: "win-1", Title: "shell", Width: 80, Height: 24, Workspace: 1, PTYID: "dead-pty-1", Cwd: cwd},
		},
	}
	if err := SaveSessionForResurrection(saved); err != nil {
		t.Fatalf("failed to save state: %v", err)
	}

	d.restoreAllSessions()
	sess := d.manager.GetSession("work")
	if sess == nil {
		t.Fatal("session 'work' was not restored")
	}
	if !sess.GetState().Restored {
		t.Fatal("precondition: the restored session is not marked")
	}

	attachTestClient(t, "work")

	if sess.GetState().Restored {
		t.Error("the restored mark survived the first attach; it would sit on the row forever")
	}

	// A client pushing a state that claims the mark must not raise it again.
	pushed := sess.GetState()
	pushed.Restored = true
	sess.UpdateState(pushed)
	if sess.GetState().Restored {
		t.Error("a client state sync put the restored mark back; the field is daemon-owned")
	}
	if sess.Info().Restored {
		t.Error("the listing shows a restored mark the daemon has cleared")
	}
}
