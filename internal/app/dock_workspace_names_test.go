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
	prev := config.DockWorkspaceTabs
	config.DockWorkspaceTabs = true
	t.Cleanup(func() { config.DockWorkspaceTabs = prev })
	return m
}

// TestWorkspaceChipsCarryTheirNames: the user named the workspace to recognise
// it by, and a strip of bare digits made the name reachable only from the
// switcher overlay.
func TestWorkspaceChipsCarryTheirNames(t *testing.T) {
	m := chipOS(t)
	tabs := m.buildDockWorkspaceTabs()
	if len(tabs) < 3 {
		t.Fatalf("the strip drew %d tabs, want at least three", len(tabs))
	}
	want := map[int]string{1: "1", 2: "review", 3: "3"}
	for _, tab := range tabs {
		if tab.Add {
			continue
		}
		if got := want[tab.Workspace]; tab.Label != got {
			t.Errorf("workspace %d's chip reads %q, want %q", tab.Workspace, tab.Label, got)
		}
	}
}

// TestWorkspaceChipWidthFollowsItsLabel is the pairing that matters: the width
// the hit rectangle is cut to has to be the width the label actually takes. Two
// places deriving the same width from state that has since moved is how a dock
// entry became unclickable while the cell to its right worked.
func TestWorkspaceChipWidthFollowsItsLabel(t *testing.T) {
	m := chipOS(t)
	for _, tab := range m.buildDockWorkspaceTabs() {
		drawn := lipgloss.Width(workspacePill(tab.Label, tab.Active, theme.UI()))
		if drawn != tab.Width {
			t.Errorf("workspace %d's chip draws %d cells but claims %d", tab.Workspace, drawn, tab.Width)
		}
		if tab.Width != workspacePillWidth(tab.Label) {
			t.Errorf("workspace %d's recorded width %d is not its label's %d",
				tab.Workspace, tab.Width, workspacePillWidth(tab.Label))
		}
		// Active and inactive must measure the same, or the strip reflows as the
		// current workspace moves along it and every rect past it shifts.
		if a, b := lipgloss.Width(workspacePill(tab.Label, true, theme.UI())),
			lipgloss.Width(workspacePill(tab.Label, false, theme.UI())); a != b {
			t.Errorf("workspace %d measures %d active and %d inactive", tab.Workspace, a, b)
		}
	}
}

// TestWorkspaceChipHitsCoverEveryCellOfTheirChip walks the edges directly. A
// name makes the chips different widths, so an off-by-one in the strip's
// running x lands a click on the neighbour rather than on nothing, which is the
// harder failure to notice.
func TestWorkspaceChipHitsCoverEveryCellOfTheirChip(t *testing.T) {
	m := chipOS(t)
	m.renderDockString()
	if len(m.dockWorkspaceHits) < 3 {
		t.Fatalf("the renderer recorded %d tab rects", len(m.dockWorkspaceHits))
	}

	for i, h := range m.dockWorkspaceHits {
		if i > 0 {
			// The pills are separated by a bare column that belongs to neither
			// of them, so a rect starting on or before its neighbour's last cell
			// is an overlap and one starting further out has swallowed a pill.
			if prev := m.dockWorkspaceHits[i-1]; h.X0 != prev.X1+dockWorkspacePillGap {
				t.Errorf("tab %d starts at %d but its neighbour ended at %d", i, h.X0, prev.X1)
			}
		}
		// Both edge cells resolve to this tab, and the cell before the first one
		// does not.
		for _, x := range []int{h.X0, h.X1 - 1} {
			if got := m.DockWorkspaceAt(x, h.Y); got == 0 {
				t.Errorf("tab %d: column %d in [%d,%d) resolves to no workspace", i, x, h.X0, h.X1)
			}
		}
		if h.Workspace > 0 {
			if got := m.DockWorkspaceAt(h.X0, h.Y); got != h.Workspace {
				t.Errorf("tab %d: its first column resolves to workspace %d, want %d", i, got, h.Workspace)
			}
			if got := m.DockWorkspaceAt(h.X1-1, h.Y); got != h.Workspace {
				t.Errorf("tab %d: its last column resolves to workspace %d, want %d", i, got, h.Workspace)
			}
		}
	}
	first := m.dockWorkspaceHits[0]
	if got := m.DockWorkspaceAt(first.X0-1, first.Y); got != 0 {
		t.Errorf("the cell before the strip resolves to workspace %d", got)
	}
	last := m.dockWorkspaceHits[len(m.dockWorkspaceHits)-1]
	if got := m.DockWorkspaceAt(last.X1, last.Y); got != 0 {
		t.Errorf("the cell after the strip resolves to workspace %d", got)
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

// TestWorkspaceChipStripIsInTheDockWidth: the strip rides in the dock's left
// region, so a wider chip has to move the items rather than draw over them.
func TestWorkspaceChipStripIsInTheDockWidth(t *testing.T) {
	m := chipOS(t)
	narrow := dockWorkspaceTabsWidth(m.buildDockWorkspaceTabs())

	m.adoptSessionLabels(&session.SessionState{WorkspaceNames: map[int]string{2: "review-branch"}})
	wide := dockWorkspaceTabsWidth(m.buildDockWorkspaceTabs())
	if wide <= narrow {
		t.Errorf("a longer name did not widen the strip: %d then %d", narrow, wide)
	}
}
