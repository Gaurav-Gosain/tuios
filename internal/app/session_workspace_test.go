package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// TestRestoreFromStateClampsWorkspace verifies that restoring a session whose
// persisted CurrentWorkspace is 0 (legacy or freshly created with no windows)
// normalizes to workspace 1, which is reachable, instead of stranding future
// windows on the unreachable workspace 0.
func TestRestoreFromStateClampsWorkspace(t *testing.T) {
	m := &OS{Settings: config.Global}
	state := &session.SessionState{
		Name:             "fresh",
		CurrentWorkspace: 0,
	}
	if err := m.RestoreFromState(state); err != nil {
		t.Fatalf("RestoreFromState returned error: %v", err)
	}
	if m.CurrentWorkspace != 1 {
		t.Errorf("CurrentWorkspace = %d after restoring workspace 0, want 1", m.CurrentWorkspace)
	}
}
