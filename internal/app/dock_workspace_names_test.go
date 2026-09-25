package app

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
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

// TestWorkspaceChipWidthFollowsItsLabel is the pairing that matters: the width
// the hit rectangle is cut to has to be the width the label actually takes. Two
// places deriving the same width from state that has since moved is how a dock
// entry became unclickable while the cell to its right worked.
func TestWorkspaceChipWidthFollowsItsLabel(t *testing.T) {
	m := chipOS(t)
	for _, tab := range m.buildDockWorkspaceTabs() {
		drawn := lipgloss.Width(workspacePill(tab.Label, tab.Active, false, theme.UI(), &config.Global))
		if drawn != tab.Width {
			t.Errorf("workspace %d's chip draws %d cells but claims %d", tab.Workspace, drawn, tab.Width)
		}
		if tab.Width != workspacePillWidth(tab.Label, &config.Global) {
			t.Errorf("workspace %d's recorded width %d is not its label's %d",
				tab.Workspace, tab.Width, workspacePillWidth(tab.Label, &config.Global))
		}
		// Active and inactive must measure the same, or the strip reflows as the
		// current workspace moves along it and every rect past it shifts.
		if a, b := lipgloss.Width(workspacePill(tab.Label, true, false, theme.UI(), &config.Global)),
			lipgloss.Width(workspacePill(tab.Label, false, false, theme.UI(), &config.Global)); a != b {
			t.Errorf("workspace %d measures %d active and %d inactive", tab.Workspace, a, b)
		}
	}
}

// TestWorkspaceChipLabelIsCapped: the strip shares the bar with the mode pill
// and the minimized entries, so a workspace named after a branch cannot be
// allowed to push either off it.
func TestWorkspaceChipLabelIsCapped(t *testing.T) {
	m := chipOS(t)
	m.adoptSessionLabels(&session.SessionState{
		WorkspaceNames: map[int]string{2: strings.Repeat("long-", 12)},
	})
	label := m.workspacePillLabel(2)
	if lipgloss.Width(label) > workspacePillLabelMax {
		t.Errorf("a long name renders %d cells on the chip, want at most %d", lipgloss.Width(label), workspacePillLabelMax)
	}

	// An unnamed workspace is still its number, which is both its identity and
	// what the chip has always shown.
	if got := m.workspacePillLabel(3); got != "3" {
		t.Errorf("an unnamed workspace's chip reads %q, want its number", got)
	}
}
