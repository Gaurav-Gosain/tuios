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

// TestApplyStateSyncLeavesModeAlone pins that input mode is per-viewer. A state
// sync from another client used to carry that client's Mode and apply it here,
// so one client entering terminal mode flipped every attached client's input
// mode out from under its user.
func TestApplyStateSyncLeavesModeAlone(t *testing.T) {
	m := &OS{Settings: config.Global, Mode: WindowManagementMode}
	state := &session.SessionState{Name: "shared", CurrentWorkspace: 1}
	if err := m.ApplyStateSync(state); err != nil {
		t.Fatalf("ApplyStateSync returned error: %v", err)
	}
	if m.Mode != WindowManagementMode {
		t.Errorf("Mode = %v after a state sync, want it untouched (%v)", m.Mode, WindowManagementMode)
	}
}
