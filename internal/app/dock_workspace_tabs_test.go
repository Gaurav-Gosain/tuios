package app

import (
	"strconv"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
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

	// Three occupied workspaces plus the trailing "+".
	if len(m.dockWorkspaceHits) != 4 {
		t.Fatalf("occupied workspaces 1, 3, 7 plus the add tab should draw 4 tabs, got %d", len(m.dockWorkspaceHits))
	}

	y := m.GetDockbarContentYPosition()
	for _, h := range m.dockWorkspaceHits {
		if h.Workspace == 0 {
			continue // the add tab resolves at click time, covered separately
		}
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

	// The last workspace tab, which now sits before the trailing "+".
	lastWS := m.dockWorkspaceHits[len(m.dockWorkspaceHits)-2]
	m.SwitchToWorkspace(m.DockWorkspaceAt(lastWS.X0, y))
	if m.CurrentWorkspace != 7 {
		t.Errorf("clicking the last workspace tab should switch to workspace 7, on %d", m.CurrentWorkspace)
	}
}

// TestDockWorkspaceAddTabOpensTheNextFreeOne pins the "+" affordance: making a
// workspace should be a click, not a remembered keybind. It also means a single
// occupied workspace still draws a strip, because "1 +" is worth showing.
func TestDockWorkspaceAddTabOpensTheNextFreeOne(t *testing.T) {
	m := dockTabTestOS(t, 1, 1, 1)
	m.renderDockString()
	if len(m.dockWorkspaceHits) != 2 {
		t.Fatalf("one workspace plus the add tab should draw 2 tabs, got %d", len(m.dockWorkspaceHits))
	}
	add := m.dockWorkspaceHits[len(m.dockWorkspaceHits)-1]
	if add.Workspace != 0 {
		t.Fatalf("the trailing tab should be the add tab (workspace 0), got %d", add.Workspace)
	}
	// Clicking it resolves to a real, empty workspace.
	if got := m.DockWorkspaceAt(add.X0, add.Y); got != 2 {
		t.Fatalf("the add tab resolved to workspace %d, want the next free one (2)", got)
	}
}

// TestDockWorkspaceAddTabHidesWhenFull keeps the strip honest: with every
// workspace in use there is nothing to add.
func TestDockWorkspaceAddTabHidesWhenFull(t *testing.T) {
	m := dockTabTestOS(t, 1, 1, 1)
	m.NumWorkspaces = 1
	m.renderDockString()
	for _, h := range m.dockWorkspaceHits {
		if h.Workspace == 0 {
			t.Fatal("the add tab is drawn with no free workspace to open")
		}
	}
}

// TestDockLeftRegionSurvivesTheStrip is the regression test for the reverted
// first attempt, which replaced the left region with the workspace number alone:
// the mode pill vanished and the app booted broken. The strip is additive, so
// the pill must still be there, coloured, and on screen with it on.
//
// The "<workspace>:<count>" readout is asserted against the drawn row as well
// as the format string. It is the dock's live pane count and the e2e harness
// reads it as its source of truth, so a restyle that stops drawing it takes the
// whole suite down with it, which is the failure this test is named for.
func TestDockLeftRegionSurvivesTheStrip(t *testing.T) {
	for _, name := range []string{"one workspace", "three workspaces"} {
		m := dockTabTestOS(t, 1, 1, 1)
		if name == "three workspaces" {
			m = dockTabTestOS(t, 2, 1, 2, 5)
		}
		t.Run(name, func(t *testing.T) {
			modeLabel, trail, _, width, mode := m.buildDockLeftText()
			if mode.Color == "" {
				t.Error("the mode pill lost its color")
			}
			if strings.TrimSpace(modeLabel) == "" {
				t.Error("the mode pill lost its label")
			}
			if width <= 0 {
				t.Errorf("the left region claims %d columns, so the dock items would run over it", width)
			}
			// "current:count" is the window count the dock has always shown.
			want := " " + strconv.Itoa(m.CurrentWorkspace) + ":"
			if !strings.Contains(trail, want) {
				t.Errorf("left text %q lost its %q window count", trail, want)
			}

			dock, _ := m.renderDockString()
			plain := stripANSIForTrace(dock)
			if lipgloss.Width(strings.Split(dock, "\n")[0]) != m.GetRenderWidth() {
				t.Error("the dock stopped being exactly one screen wide")
			}
			if !strings.Contains(plain, strings.TrimSpace(modeLabel)) {
				t.Errorf("the drawn dock does not show the mode label %q", modeLabel)
			}
			if !strings.Contains(plain, want) {
				t.Errorf("the drawn dock does not show the %q window count", want)
			}
		})
	}
}

// The dock's pills are flat by default: the caps repeated on the mode pill and
// on every minimized window read as a row of beads rather than a status line.
// The capped look stays one config key away.
//
// The workspace strip is deliberately outside this. Its pills are tabs, they
// keep their rounded ends either way, and TestWorkspacePillsKeepTheirCapsWhatever
// TheDockDoes is where that is pinned; the strip is turned off here so the two
// rules are tested one at a time.
func TestDockPillsAreFlatByDefault(t *testing.T) {
	m := dockTabTestOS(t, 1, 1, 2, 3)

	prevCaps, prevTabs := m.Settings.DockPillCaps, m.Settings.DockWorkspaceTabs
	t.Cleanup(func() { m.Settings.DockPillCaps, m.Settings.DockWorkspaceTabs = prevCaps, prevTabs })

	m.Settings.DockPillCaps = false
	if lc := m.Settings.GetDockPillLeftChar(); lc != "" {
		t.Errorf("flat pills still report a left cap %q", lc)
	}
	m.Settings.DockWorkspaceTabs = false
	flat, _ := m.renderDockString()
	// The mode chip is outside this rule too. It heads a row of capped tabs, so
	// a square chip beside them reads as an unfinished pill. It wears exactly
	// one of each cap, and anything beyond that is the minimized run, which is
	// what the setting governs.
	for _, cap := range []string{config.DockPillLeftChar, config.DockPillRightChar} {
		if n := strings.Count(flat, cap); n != 1 {
			t.Errorf("flat dock draws the cap glyph %q %d times, want 1 for the mode chip alone", cap, n)
		}
	}
	m.Settings.DockWorkspaceTabs = prevTabs
	m.renderDockString()

	// The active tab is a filled cell rather than a cap pair, so the strip must
	// keep its width and its hit rects across the two styles.
	flatHits := append([]dockWorkspaceHit(nil), m.dockWorkspaceHits...)

	m.Settings.DockPillCaps = true
	capped, _ := m.renderDockString()
	if !strings.Contains(capped, config.DockPillLeftChar) {
		t.Error("turning pill caps on did not bring the caps back")
	}
	if lipgloss.Width(strings.Split(capped, "\n")[0]) != m.GetRenderWidth() {
		t.Error("the capped dock stopped being exactly one screen wide")
	}
	if len(m.dockWorkspaceHits) != len(flatHits) {
		t.Fatalf("the strip has %d tabs capped and %d flat", len(m.dockWorkspaceHits), len(flatHits))
	}
}

// The totals are gone on purpose: the strip beside them already names every
// occupied workspace, so "5 terminals across 3 workspaces" drove no decision
// and cost two icons and a separator to say.
func TestDockDropsTheStatsTotals(t *testing.T) {
	m := dockTabTestOS(t, 2, 1, 2, 5)
	dock, _ := m.renderDockString()
	plain := stripANSIForTrace(dock)
	for _, icon := range []string{m.Settings.GetDockIconWorkspaceCount(), m.Settings.GetDockIconTerminalCount()} {
		if strings.Contains(plain, icon) {
			t.Errorf("the dock still shows the stats icon %q: %q", icon, plain)
		}
	}
	if strings.Contains(plain, m.Settings.GetDockSeparator()+"3") {
		t.Errorf("the dock still shows the totals after their separator: %q", plain)
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
