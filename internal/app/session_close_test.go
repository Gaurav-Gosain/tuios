package app

import (
	"testing"
)

// sessionCloseOS is a session of three panes, agent states left to the caller.
func sessionCloseOS(t testing.TB) *OS {
	t.Helper()
	m := dockSessionOS(t, 160, true)
	for _, id := range []string{"bravo", "gamma"} {
		w := newTestWindow(t, id, 60, 20)
		w.Workspace = 1
		m.Windows = append(m.Windows, w)
	}
	return m
}

// TestSessionCloseOpensOnCancel checks the destructive row is never what enter
// would run on the frame the dialog first draws.
func TestSessionCloseOpensOnCancel(t *testing.T) {
	m := sessionCloseOS(t)
	m.SessionCloseSelected = SessionCloseRowClose
	m.OpenSessionClose()
	if m.SessionCloseSelected != SessionCloseRowCancel {
		t.Fatalf("the dialog opened on row %d, want the cancel row %d", m.SessionCloseSelected, SessionCloseRowCancel)
	}
	if !m.ShowSessionClose {
		t.Fatal("OpenSessionClose did not raise the dialog")
	}
}

// TestSessionCloseCancelChangesNothing is the other half of a confirmation
// being worth having: saying no has to be free.
func TestSessionCloseCancelChangesNothing(t *testing.T) {
	m := sessionCloseOS(t)
	before := len(m.Windows)
	m.OpenSessionClose()

	if cmd := m.SessionCloseActivate(SessionCloseRowCancel); cmd != nil {
		t.Error("cancelling returned a command; it must do nothing at all")
	}
	if m.ShowSessionClose {
		t.Error("cancelling left the dialog up")
	}
	if m.QuitRequested {
		t.Error("cancelling asked to quit")
	}
	if len(m.Windows) != before {
		t.Errorf("cancelling changed the pane count to %d, want %d", len(m.Windows), before)
	}

	// Click-away is the same answer as esc.
	m.OpenSessionClose()
	m.closeOverlay("sessionclose")
	if m.ShowSessionClose || m.QuitRequested {
		t.Error("clicking away from the dialog did something")
	}
}
