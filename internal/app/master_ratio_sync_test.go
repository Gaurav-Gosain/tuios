package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// ratioClient is a client with nothing on screen: these cases are about which
// number a workspace switch lands on and where that number came from, not about
// where any pane ends up.
func ratioClient(t *testing.T) *OS {
	t.Helper()
	return &OS{
		Settings:             config.Global,
		SharedBorders:        config.Global.SharedBorders,
		PaneGap:              config.Global.PaneGap,
		AutoTiling:           true,
		UseBSPLayout:         false, // master-stack: the mode MasterRatio drives
		WorkspaceLayouts:     map[int][]WindowLayout{},
		WorkspaceMasterRatio: map[int]float64{},
		WorkspaceHasCustom:   map[int]bool{},
		WorkspaceFocus:       map[int]int{},
		WorkspaceTrees:       map[int]*layout.BSPTree{},
		CurrentWorkspace:     1,
		NumWorkspaces:        9,
		MasterRatio:          config.Global.MasterRatioFraction(),
	}
}

// TestASyncThatSaysNothingLeavesTheRatiosAlone is the compatibility contract.
// A peer too old to know the field, and every state file written before it
// existed, carry no map at all; gob also drops an empty one, so absent and empty
// have to mean the same thing. Either way the client keeps what it holds and a
// workspace nobody has a value for still falls back to the configured ratio.
//
// NEGATIVE CONTROL: make adoptWorkspaceMasterRatio replace the map instead of
// merging into it and the entry for workspace 4 disappears.
func TestASyncThatSaysNothingLeavesTheRatiosAlone(t *testing.T) {
	prev := config.Global.MasterRatioPercent
	t.Cleanup(func() { config.Global.MasterRatioPercent = prev })
	config.Global.MasterRatioPercent = 70

	m := ratioClient(t)
	m.WorkspaceMasterRatio[4] = 0.3

	for _, tc := range []struct {
		name  string
		ratio map[int]float64
	}{
		{"absent", nil},
		{"empty", map[int]float64{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m.adoptWorkspaceMasterRatio(&session.SessionState{
				CurrentWorkspace:     1,
				MasterRatio:          0.55,
				WorkspaceMasterRatio: tc.ratio,
			})
			if got := m.WorkspaceMasterRatio[4]; got != 0.3 {
				t.Errorf("workspace 4 holds %v after a sync that said nothing, want the 0.3 it held", got)
			}
			m.CurrentWorkspace = 1
			m.RestoreWorkspaceLayout(5)
			if got := m.MasterRatio; got != 0.7 {
				t.Errorf("a workspace nobody has a value for came up at %v, want the configured 0.7", got)
			}
		})
	}
}

// TestALaggingSyncCannotUndoARatioJustTuned is the other direction of the merge:
// the daemon echoes a client's own push back, and an echo built before the last
// tune must not roll that tune back.
//
// NEGATIVE CONTROL: replace the map in adoptWorkspaceMasterRatio rather than
// merging and workspace 6 reverts to 0.5.
func TestALaggingSyncCannotUndoARatioJustTuned(t *testing.T) {
	m := ratioClient(t)
	m.WorkspaceMasterRatio[6] = 0.9

	m.adoptWorkspaceMasterRatio(&session.SessionState{
		CurrentWorkspace:     1,
		WorkspaceMasterRatio: map[int]float64{1: 0.5},
	})
	if got := m.WorkspaceMasterRatio[6]; got != 0.9 {
		t.Errorf("workspace 6 holds %v after a sync that never heard of it, want the 0.9 this client tuned", got)
	}
	if got := m.WorkspaceMasterRatio[1]; got != 0.5 {
		t.Errorf("workspace 1 holds %v, want the 0.5 the sync carried", got)
	}
}
