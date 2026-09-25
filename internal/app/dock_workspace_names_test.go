package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// chipOS is a dock with three occupied workspaces, the middle one named.
func chipOS(t *testing.T) *OS {
	t.Helper()
	m := newNarrowOS(t, 140, 30)
	m.NumWorkspaces = 9
	m.CurrentWorkspace = 1
	m.Windows = []*terminal.Window{
		{ID: "w1", Width: 40, Height: 10, Workspace: 1},
		{ID: "w2", Width: 40, Height: 10, Workspace: 2},
		{ID: "w3", Width: 40, Height: 10, Workspace: 3},
	}
	m.adoptSessionLabels(&session.SessionState{WorkspaceNames: map[int]string{2: "review"}})
	prev := m.Settings.DockWorkspaceTabs
	m.Settings.DockWorkspaceTabs = true
	t.Cleanup(func() { m.Settings.DockWorkspaceTabs = prev })
	return m
}
