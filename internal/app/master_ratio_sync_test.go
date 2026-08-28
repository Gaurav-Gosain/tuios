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

// TestATunedMasterRatioReachesAClientThatNeverVisitedTheWorkspace is the two
// client case the field exists for.
//
// The master ratio used to live only in each client's own WorkspaceMasterRatio
// map, which nothing ever sent. A client that had never been to a workspace
// therefore had no entry for it, laid it out at its own configured ratio on the
// first visit, and pushed that ratio back as the session's - destroying the
// value another client had tuned, for every client, with no way back but tuning
// it again.
//
// NEGATIVE CONTROL: drop the WorkspaceMasterRatio block from BuildSessionState
// (or the adopt call from RestoreFromState) and B comes back at the configured
// 0.5 and pushes 0.5, which is the bug verbatim.
func TestATunedMasterRatioReachesAClientThatNeverVisitedTheWorkspace(t *testing.T) {
	prev := config.Global.MasterRatioPercent
	t.Cleanup(func() { config.Global.MasterRatioPercent = prev })
	config.Global.MasterRatioPercent = 50

	a := ratioClient(t)
	b := ratioClient(t)

	// A goes to workspace 3, tunes the split to 0.7 and leaves.
	a.CurrentWorkspace = 3
	a.MasterRatio = 0.7
	a.SaveCurrentLayout()

	pushed := a.BuildSessionState()
	if got := pushed.WorkspaceMasterRatio[3]; got != 0.7 {
		t.Fatalf("A pushed %v for workspace 3, want the 0.7 it tuned", got)
	}

	// B has never been to workspace 3. It adopts the session.
	if err := b.RestoreFromState(pushed); err != nil {
		t.Fatalf("RestoreFromState: %v", err)
	}
	if got := b.WorkspaceMasterRatio[3]; got != 0.7 {
		t.Fatalf("B holds %v for workspace 3 after adopting the session, want 0.7", got)
	}

	// B's own push must carry A's value rather than replacing it with B's config.
	if got := b.BuildSessionState().WorkspaceMasterRatio[3]; got != 0.7 {
		t.Errorf("B pushed %v for workspace 3, want A's 0.7: B is overwriting a ratio it never touched", got)
	}

	// And B's first visit lands on A's ratio.
	b.CurrentWorkspace = 1
	b.RestoreWorkspaceLayout(3)
	if b.MasterRatio != 0.7 {
		t.Errorf("B's first visit to workspace 3 put the split at %v, want A's 0.7", b.MasterRatio)
	}
}

// TestTheTunedWorkspaceOnScreenIsPushedBeforeItIsLeft covers the half a plain
// map copy misses: SaveCurrentLayout only writes the live ratio into the map on
// the way out of a workspace, so a client that tunes a split and stays there
// would push a map that was one tune behind for the very workspace being tuned.
//
// NEGATIVE CONTROL: remove the CurrentWorkspace line from BuildSessionState's
// block and the push carries the stale 0.5.
func TestTheTunedWorkspaceOnScreenIsPushedBeforeItIsLeft(t *testing.T) {
	a := ratioClient(t)
	a.CurrentWorkspace = 2
	a.MasterRatio = 0.5
	a.SaveCurrentLayout()
	a.MasterRatio = 0.8 // tuned with the resize keys, still on workspace 2

	if got := a.BuildSessionState().WorkspaceMasterRatio[2]; got != 0.8 {
		t.Errorf("the push carries %v for the workspace on screen, want the 0.8 in force", got)
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

// TestAdoptingAnotherSessionDropsTheRatiosOfTheOneBeingLeft: RestoreFromState is
// also the session switch, and one session's ratios say nothing about another's.
//
// NEGATIVE CONTROL: drop the map reset in RestoreFromState and workspace 8's
// ratio follows the client into the new session.
func TestAdoptingAnotherSessionDropsTheRatiosOfTheOneBeingLeft(t *testing.T) {
	m := ratioClient(t)
	m.WorkspaceMasterRatio[8] = 0.25

	if err := m.RestoreFromState(&session.SessionState{
		Name:                 "other",
		CurrentWorkspace:     1,
		AutoTiling:           true,
		MasterRatio:          0.5,
		WorkspaceMasterRatio: map[int]float64{2: 0.6},
	}); err != nil {
		t.Fatalf("RestoreFromState: %v", err)
	}
	if _, ok := m.WorkspaceMasterRatio[8]; ok {
		t.Errorf("workspace 8's ratio survived a switch to another session")
	}
	if got := m.WorkspaceMasterRatio[2]; got != 0.6 {
		t.Errorf("workspace 2 holds %v in the adopted session, want 0.6", got)
	}
}

// TestAPeerSyncCarriesTheMasterRatios covers the other adoption path. A client
// that is already attached takes the session's state through ApplyStateSync, not
// through RestoreFromState, so a ratio tuned on another client reaches it there
// or not at all.
//
// NEGATIVE CONTROL: drop the adopt call from ApplyStateSync and workspace 3
// comes back with no entry, which sends the next visit to the configured ratio.
func TestAPeerSyncCarriesTheMasterRatios(t *testing.T) {
	m := ratioClient(t)
	m.MasterRatio = 0.5

	if err := m.ApplyStateSync(&session.SessionState{
		Name:                 "peer",
		CurrentWorkspace:     1,
		MasterRatio:          0.5,
		AutoTiling:           true,
		WorkspaceMasterRatio: map[int]float64{3: 0.7},
	}); err != nil {
		t.Fatalf("ApplyStateSync: %v", err)
	}

	if got := m.WorkspaceMasterRatio[3]; got != 0.7 {
		t.Fatalf("workspace 3 holds %v after a peer sync, want the 0.7 the peer tuned", got)
	}
	m.RestoreWorkspaceLayout(3)
	if m.MasterRatio != 0.7 {
		t.Errorf("the first visit to workspace 3 put the split at %v, want 0.7", m.MasterRatio)
	}
}
