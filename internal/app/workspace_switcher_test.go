package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// wsSwitcherOS builds an OS with panes spread over workspaces 1, 2 and 4.
func wsSwitcherOS() *OS {
	m := &OS{
		Settings:         config.Global,
		NumWorkspaces:    9,
		CurrentWorkspace: 1,
		Windows: []*terminal.Window{
			{ID: "a", Workspace: 1},
			{ID: "b", Workspace: 2},
			{ID: "c", Workspace: 2},
			{ID: "d", Workspace: 4},
		},
	}
	return m
}

// TestWorkspaceSwitcherFilterTargetsTheFilteredRow is the off-by-one guard:
// with a query typed, row 0 is the first row on screen, not the first workspace.
func TestWorkspaceSwitcherFilterTargetsTheFilteredRow(t *testing.T) {
	m := wsSwitcherOS()
	m.adoptSessionLabels(&session.SessionState{WorkspaceNames: map[int]string{4: "review"}})
	m.OpenWorkspaceSwitcher()

	m.WorkspaceSwitcherQuery = "rev"
	m.WorkspaceSwitcherSelected = 0

	filtered := FilterWorkspaceItems(m.WorkspaceSwitcherItems, m.WorkspaceSwitcherQuery)
	if len(filtered) != 1 {
		t.Fatalf("filtered count = %d, want 1", len(filtered))
	}
	target, ok := m.WorkspaceSwitcherTarget(0)
	if !ok || target.Number != 4 {
		t.Errorf("target = %+v, want workspace 4: the row was resolved against the unfiltered list", target)
	}

	// The number stays the way to reach a workspace whatever it is called.
	m.WorkspaceSwitcherQuery = "4"
	byNumber, ok := m.WorkspaceSwitcherTarget(0)
	if !ok || byNumber.Number != 4 {
		t.Errorf("searching by number found %+v, want workspace 4", byNumber)
	}

	m.WorkspaceSwitcherQuery = "zzz"
	if _, ok := m.WorkspaceSwitcherTarget(0); ok {
		t.Error("an empty filtered list still resolved a target")
	}
}
