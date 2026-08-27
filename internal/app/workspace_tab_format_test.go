package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// withDockWorkspaceTabFormat saves and restores the tab-format global, which
// is package state shared with every other test in the run.
func withDockWorkspaceTabFormat(t *testing.T) {
	t.Helper()
	prev := config.Global.DockWorkspaceTabFormat
	t.Cleanup(func() { config.Global.DockWorkspaceTabFormat = prev })
}

// The dock strip formats each tab's label through appearance.dock_workspace_tab_format.
// A named workspace shows "{index}: {name}" once configured, which is what the
// "name and index on every tab" request (issue #80) asked for.
func TestWorkspaceTabFormatReachesTheStrip(t *testing.T) {
	withDockWorkspaceTabFormat(t)
	config.Global.DockWorkspaceTabFormat = "{index}: {name}"

	m := chipOS(t)
	tabs := m.buildDockWorkspaceTabs()
	if len(tabs) < 3 {
		t.Fatalf("the strip drew %d tabs, want at least three", len(tabs))
	}
	want := map[int]string{1: "1: 1", 2: "2: review", 3: "3: 3"}
	for _, tab := range tabs {
		if tab.Add {
			continue // the "+" tab is a control, not a workspace label
		}
		if got := want[tab.Workspace]; tab.Label != got {
			t.Errorf("workspace %d's chip reads %q with the format on, want %q", tab.Workspace, tab.Label, got)
		}
	}
}

// The tab width must track the formatted label, or the hit rectangle would no
// longer cover the cells the pill actually draws.
func TestWorkspaceTabFormatWidthFollowsLabel(t *testing.T) {
	withDockWorkspaceTabFormat(t)
	config.Global.DockWorkspaceTabFormat = "{index} {name}"

	m := chipOS(t)
	for _, tab := range m.buildDockWorkspaceTabs() {
		if tab.Add {
			continue
		}
		if want := workspacePillWidth(tab.Label, &config.Global); tab.Width != want {
			t.Errorf("workspace %d records width %d, want %d for formatted label %q",
				tab.Workspace, tab.Width, want, tab.Label)
		}
	}
}

// A name-only tab (empty format) is unchanged: the historic rendering.
func TestWorkspaceTabFormatEmptyIsNameOnly(t *testing.T) {
	withDockWorkspaceTabFormat(t)
	config.Global.DockWorkspaceTabFormat = ""

	m := chipOS(t)
	want := map[int]string{1: "1", 2: "review", 3: "3"}
	for _, tab := range m.buildDockWorkspaceTabs() {
		if tab.Add {
			continue
		}
		if got := want[tab.Workspace]; tab.Label != got {
			t.Errorf("workspace %d's chip reads %q with no format, want %q", tab.Workspace, tab.Label, got)
		}
	}
}
