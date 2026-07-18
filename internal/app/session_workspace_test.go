package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

func TestClampWorkspace(t *testing.T) {
	cases := []struct {
		in, want int
	}{
		{-1, 1},
		{0, 1},
		{1, 1},
		{2, 2},
		{9, 9},
	}
	for _, c := range cases {
		if got := clampWorkspace(c.in); got != c.want {
			t.Errorf("clampWorkspace(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

// TestRestoreFromStateClampsWorkspace verifies that restoring a session whose
// persisted CurrentWorkspace is 0 (legacy or freshly created with no windows)
// normalizes to workspace 1, which is reachable, instead of stranding future
// windows on the unreachable workspace 0.
func TestRestoreFromStateClampsWorkspace(t *testing.T) {
	m := &OS{}
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
