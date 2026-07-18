package app

import "testing"

// TestRestoreWorkspaceLayoutDoesNotForceCustom verifies that restoring a saved
// layout does not mark the workspace as custom. SaveCurrentLayout runs on every
// workspace switch, so a saved layout always exists after the first switch;
// forcing the custom flag here previously suppressed the retile-if-not-custom
// check permanently, disabling auto-retiling after a single round-trip.
func TestRestoreWorkspaceLayoutDoesNotForceCustom(t *testing.T) {
	m := &OS{
		AutoTiling: true,
		WorkspaceLayouts: map[int][]WindowLayout{
			2: {{WindowID: "nonexistent", X: 0, Y: 0, Width: 10, Height: 10}},
		},
		WorkspaceMasterRatio: map[int]float64{},
		WorkspaceHasCustom:   map[int]bool{},
	}

	m.RestoreWorkspaceLayout(2)

	if m.WorkspaceHasCustom[2] {
		t.Error("RestoreWorkspaceLayout must not mark a workspace custom; only MarkLayoutCustom (a real user resize) may")
	}
}

// TestRestoreWorkspaceLayoutRoundTripKeepsRetiling simulates two workspace-switch
// saves followed by a restore and confirms auto-retiling stays enabled (the
// workspace is not stuck as custom), which is what previously broke.
func TestRestoreWorkspaceLayoutRoundTripKeepsRetiling(t *testing.T) {
	m := &OS{
		AutoTiling:           true,
		WorkspaceLayouts:     map[int][]WindowLayout{},
		WorkspaceMasterRatio: map[int]float64{},
		WorkspaceHasCustom:   map[int]bool{},
		CurrentWorkspace:     1,
	}

	// Simulate a switch away from workspace 1 (saves its layout).
	m.SaveCurrentLayout()
	// Simulate switching back to workspace 1 (restores the just-saved layout).
	m.RestoreWorkspaceLayout(1)

	if m.WorkspaceHasCustom[1] {
		t.Error("auto-retiling permanently disabled: workspace 1 marked custom after a plain save/restore round-trip")
	}
}
