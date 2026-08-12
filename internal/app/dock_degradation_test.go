package app

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// dockCrowdedOS is a session with named workspaces and minimized panes, which
// is the state the bar has to ration: the strip wants the left region, the
// meters want the right, and the entries are what is left in the middle.
func dockCrowdedOS(t testing.TB, width, workspaces, minimized int) *OS {
	t.Helper()
	m := &OS{
		WorkspaceFocus:   map[int]int{},
		NumWorkspaces:    9,
		CurrentWorkspace: 1,
		Width:            width,
		Height:           30,
		FocusedWindow:    -1,
	}
	names := []string{"editor", "server", "logs", "notes", "build", "review"}
	m.WorkspaceNames = map[int]string{}
	for ws := 1; ws <= workspaces; ws++ {
		win := newTestWindow(t, fmt.Sprintf("ws%d", ws), 40, 12)
		win.Workspace = ws
		m.Windows = append(m.Windows, win)
		// Named workspaces are what make the strip wide enough to be worth
		// rationing, which is the state the audit captured.
		m.WorkspaceNames[ws] = names[(ws-1)%len(names)]
	}
	for i := range minimized {
		win := newTestWindow(t, fmt.Sprintf("min%d", i), 40, 12)
		win.Workspace = 1
		win.CustomName = fmt.Sprintf("min%d", i)
		win.Minimized = true
		win.MinimizeOrder = int64(i + 1)
		m.Windows = append(m.Windows, win)
	}
	return m
}

// dockRow is the dock's content row as drawn, with its styling stripped.
func dockRow(t *testing.T, m *OS) string {
	t.Helper()
	dock, top := m.renderDockString()
	lines := strings.Split(dock, "\n")
	r := m.GetDockbarContentYPosition() - top
	if r < 0 || r >= len(lines) {
		t.Fatalf("the dock's content row %d is outside the %d rows it drew", r, len(lines))
	}
	return stripANSIForTrace(lines[r])
}

// withMeters turns the system readouts on for the duration of a test. They are
// off by default, which is itself part of what the bar was rationing for.
func withMeters(t *testing.T) {
	t.Helper()
	cpu, ram := config.ShowCPU, config.ShowRAM
	config.ShowCPU, config.ShowRAM = true, true
	t.Cleanup(func() { config.ShowCPU, config.ShowRAM = cpu, ram })
}

// TestDockKeepsTheEntryTheMetersWereCrowdingOut is the priority inversion, at
// the width the audit found it.
//
// At 120 columns with four workspace pills and one minimized pane, the pane's
// entry was not drawn: the right block reserved 32 columns for meters that are
// off by default, the session controls hold their own end, and what was left
// went negative. The only recoverable user object on the bar degraded to a
// "..." that recorded no hit rectangle, so the pane could not be reached by
// mouse at all.
func TestDockKeepsTheEntryTheMetersWereCrowdingOut(t *testing.T) {
	m := dockCrowdedOS(t, 120, 6, 1)
	row := dockRow(t, m)
	if !strings.Contains(row, "min0") {
		t.Fatalf("the minimized pane got no entry on a 120-column dock:\n%s", row)
	}

	y := m.GetDockbarContentYPosition()
	if len(m.dockItemHits) != 1 {
		t.Fatalf("the renderer recorded %d entry rectangles, want 1", len(m.dockItemHits))
	}
	h := m.dockItemHits[0]

	// The rectangle is the cells the pill was drawn on, exactly: the bug this
	// guards was a rectangle sitting a few columns right of them, so both edge
	// columns are checked from both sides.
	x0, x1 := dockEntryColumns(t, m, " 1:min0 ")
	if h.X0 != x0 || h.X1 != x1 {
		t.Errorf("the entry is drawn over columns %d..%d but its rectangle is %d..%d", x0, x1-1, h.X0, h.X1-1)
	}
	for x := h.X0; x < h.X1; x++ {
		if got := m.DockItemAt(x, y); got != h.WindowIndex {
			t.Errorf("column %d of the entry (rectangle %d..%d) routes to %d", x, h.X0, h.X1-1, got)
		}
	}
	if got := m.DockItemAt(h.X0-1, y); got == h.WindowIndex {
		t.Errorf("the column before the entry (%d) routes to it", h.X0-1)
	}
	if got := m.DockItemAt(h.X1, y); got == h.WindowIndex {
		t.Errorf("the column after the entry (%d) routes to it", h.X1)
	}
}

// TestDockDropsTheMetersBeforeTheEntries pins the order itself across the
// widths the audit captured. A readout the user cannot act on gives up its
// columns before a control they can: an entry is only ever dropped once the
// meters have nothing left to give.
func TestDockDropsTheMetersBeforeTheEntries(t *testing.T) {
	withMeters(t)
	for _, width := range []int{60, 80, 90, 100, 120, 160} {
		t.Run(fmt.Sprintf("w%d", width), func(t *testing.T) {
			m := dockCrowdedOS(t, width, 6, 3)
			layout := m.CalculateDockLayout()
			row := dockRow(t, m)
			meters := strings.Contains(row, "CPU:") || strings.Contains(row, "RAM:")

			if layout.TruncatedCount > 0 && meters {
				t.Errorf("an entry was dropped while the meters still drew:\n%s", row)
			}
			if layout.TruncatedCount > 0 && layout.RightWidth > 0 {
				t.Errorf("%d entries dropped while the right block held %d columns", layout.TruncatedCount, layout.RightWidth)
			}
			// Whatever survived is clickable where it is drawn.
			y := m.GetDockbarContentYPosition()
			for _, it := range layout.VisibleItems {
				name := m.Windows[it.WindowIndex].CustomName
				x0, x1 := dockEntryColumns(t, m, name)
				for _, x := range []int{x0, x1 - 1} {
					if got := m.DockItemAt(x, y); got != it.WindowIndex {
						t.Errorf("edge column %d of %q routes to %d, want %d", x, name, got, it.WindowIndex)
					}
				}
			}
			if lipgloss.Width(row) > width {
				t.Errorf("the dock row is %d cells on a %d-column screen", lipgloss.Width(row), width)
			}
		})
	}
}

// TestDockOverflowMarkerIsClickableWhereItIsDrawn covers the last resort. When
// the bar genuinely cannot hold every entry, the marker standing for the rest
// has a rectangle of its own, over the cells it was drawn on.
func TestDockOverflowMarkerIsClickableWhereItIsDrawn(t *testing.T) {
	m := dockCrowdedOS(t, 80, 6, 8)
	layout := m.CalculateDockLayout()
	row := dockRow(t, m)
	if layout.TruncatedCount == 0 {
		t.Fatalf("eight entries on an 80-column dock all fitted; the test no longer crowds it:\n%s", row)
	}

	x0, x1 := dockEntryColumns(t, m, "...")
	y := m.GetDockbarContentYPosition()
	for x := x0; x < x1; x++ {
		if !m.DockOverflowAt(x, y) {
			t.Errorf("column %d of the marker (drawn %d..%d) is not on its rectangle", x, x0, x1-1)
		}
	}
	if m.DockOverflowAt(x1, y) {
		t.Errorf("the column after the marker (%d) is on its rectangle", x1)
	}
	if m.DockOverflowAt(x0, y-1) || m.DockOverflowAt(x0, y+1) {
		t.Error("the marker's rectangle covers a row it was not drawn on")
	}
	if m.dockOverflowHit.Overflowed != layout.TruncatedCount {
		t.Errorf("the marker stands for %d entries, %d were dropped", m.dockOverflowHit.Overflowed, layout.TruncatedCount)
	}
}

// TestDockMetersReserveOnlyWhatTheyDraw is the reservation itself: off, they
// hold nothing.
func TestDockMetersReserveOnlyWhatTheyDraw(t *testing.T) {
	m := dockCrowdedOS(t, 120, 2, 0)
	m.Windows = append(m.Windows, &terminal.Window{ID: "x", Workspace: 1})

	cpu, ram := config.ShowCPU, config.ShowRAM
	t.Cleanup(func() { config.ShowCPU, config.ShowRAM = cpu, ram })

	config.ShowCPU, config.ShowRAM = false, false
	if got := m.calculateDockRightWidth(); got != 0 {
		t.Errorf("the meters reserved %d columns while drawing nothing", got)
	}
	config.ShowCPU, config.ShowRAM = true, true
	if got := m.calculateDockRightWidth(); got <= 0 {
		t.Errorf("the meters reserved %d columns while drawing both readouts", got)
	}
}
