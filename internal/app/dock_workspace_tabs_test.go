package app

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// dockTabTestOS is an OS with one window per named workspace, wide enough to
// draw a full dock.
func dockTabTestOS(t testing.TB, current int, workspaces ...int) *OS {
	t.Helper()
	wins := make([]*terminal.Window, 0, len(workspaces))
	for i, ws := range workspaces {
		w := newTestWindow(t, "dock-tab-"+strings.Repeat("x", i+1), 60, 20)
		w.Workspace = ws
		wins = append(wins, w)
	}
	m := newTestOS(wins[0])
	m.Windows = wins
	m.Width, m.Height = 160, 40
	m.CurrentWorkspace = current
	return m
}

// TestDockWorkspaceTabsAreClickable is the contract for deliverable 1: every
// occupied workspace gets a tab, and every column of a tab routes to its own
// workspace.
func TestDockWorkspaceTabsAreClickable(t *testing.T) {
	m := dockTabTestOS(t, 1, 1, 3, 3, 7)
	m.renderDockString()

	if len(m.dockWorkspaceHits) != 3 {
		t.Fatalf("occupied workspaces 1, 3, 7 should draw 3 tabs, got %d", len(m.dockWorkspaceHits))
	}

	y := m.GetDockbarContentYPosition()
	for _, h := range m.dockWorkspaceHits {
		for x := h.X0; x < h.X1; x++ {
			if got := m.DockWorkspaceAt(x, y); got != h.Workspace {
				t.Errorf("column %d of the workspace-%d tab routed to %d", x, h.Workspace, got)
			}
		}
	}

	// A column just off either end of the strip belongs to no tab.
	first, last := m.dockWorkspaceHits[0], m.dockWorkspaceHits[len(m.dockWorkspaceHits)-1]
	if got := m.DockWorkspaceAt(first.X0-1, y); got != 0 {
		t.Errorf("the column before the strip routed to workspace %d", got)
	}
	if got := m.DockWorkspaceAt(last.X1, y); got != 0 {
		t.Errorf("the column after the strip routed to workspace %d", got)
	}
	// The strip is one row: the separator above it is not clickable.
	if got := m.DockWorkspaceAt(first.X0, y-1); got != 0 {
		t.Errorf("the row above the dock routed to workspace %d", got)
	}

	m.SwitchToWorkspace(m.DockWorkspaceAt(last.X0, y))
	if m.CurrentWorkspace != 7 {
		t.Errorf("clicking the last tab should switch to workspace 7, on %d", m.CurrentWorkspace)
	}
}

// TestDockWorkspaceTabsStayOffWithNowhereToGo keeps the idle dock exactly what
// it was: one workspace is not a strip, it is a digit the stats already show.
func TestDockWorkspaceTabsStayOffWithNowhereToGo(t *testing.T) {
	m := dockTabTestOS(t, 1, 1, 1)
	m.renderDockString()
	if len(m.dockWorkspaceHits) != 0 {
		t.Fatalf("a single occupied workspace should draw no tabs, got %d", len(m.dockWorkspaceHits))
	}
}

// TestDockLeftRegionSurvivesTheStrip is the regression test for the reverted
// first attempt, which replaced the left region with the workspace number alone:
// the mode pill and the window-count stats vanished and the app booted broken.
// The strip is additive, so both must still be there with it on.
func TestDockLeftRegionSurvivesTheStrip(t *testing.T) {
	for _, name := range []string{"one workspace", "three workspaces"} {
		m := dockTabTestOS(t, 1, 1, 1)
		if name == "three workspaces" {
			m = dockTabTestOS(t, 2, 1, 2, 5)
		}
		t.Run(name, func(t *testing.T) {
			leftText, _, mode := m.buildDockLeftText()
			if mode.Color == "" {
				t.Error("the mode pill lost its color")
			}
			// "current:count" is the window count the dock has always shown.
			want := " " + string(rune('0'+m.CurrentWorkspace)) + ":"
			if !strings.Contains(leftText, want) {
				t.Errorf("left text %q lost its %q window count", leftText, want)
			}

			dock, _ := m.renderDockString()
			if lipgloss.Width(strings.Split(dock, "\n")[0]) != m.GetRenderWidth() {
				t.Error("the dock stopped being exactly one screen wide")
			}
		})
	}
}

// TestDockWorkspaceTabsAreUniformWidth keeps the strip from reflowing under the
// pointer: switching workspace must not move the other tabs.
func TestDockWorkspaceTabsAreUniformWidth(t *testing.T) {
	m := dockTabTestOS(t, 1, 1, 2, 3)
	m.renderDockString()
	before := append([]dockWorkspaceHit(nil), m.dockWorkspaceHits...)

	m.CurrentWorkspace = 3
	m.renderDockString()

	if len(before) != len(m.dockWorkspaceHits) {
		t.Fatalf("tab count changed on a switch: %d then %d", len(before), len(m.dockWorkspaceHits))
	}
	for i, h := range m.dockWorkspaceHits {
		if h.X0 != before[i].X0 || h.X1 != before[i].X1 {
			t.Errorf("tab %d moved from [%d,%d) to [%d,%d) on a switch",
				h.Workspace, before[i].X0, before[i].X1, h.X0, h.X1)
		}
	}
}
