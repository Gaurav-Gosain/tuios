package app

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// dockMinimizedOS is an OS with two minimized panes, which is what puts entries
// on the dock at all.
func dockMinimizedOS(t testing.TB) *OS {
	t.Helper()
	a := newTestWindow(t, "alpha", 60, 20)
	b := newTestWindow(t, "bravo", 60, 20)
	m := newTestOS(a)
	m.Windows = []*terminal.Window{a, b}
	m.Width, m.Height = 160, 40
	m.CurrentWorkspace = 1
	for i, w := range m.Windows {
		w.Workspace = 1
		w.CustomName = []string{"alpha", "bravo"}[i]
		w.Minimized = true
		w.MinimizeOrder = int64(i + 1)
	}
	m.FocusedWindow = -1
	return m
}

// dockEntryColumns finds where a minimized entry's label was actually drawn on
// the dock row, which is the geometry a click has to agree with.
func dockEntryColumns(t *testing.T, m *OS, label string) (x0, x1 int) {
	t.Helper()
	dock, top := m.renderDockString()
	lines := strings.Split(dock, "\n")
	r := m.GetDockbarContentYPosition() - top
	if r < 0 || r >= len(lines) {
		t.Fatalf("the dock's content row %d is outside the %d rows it drew", r, len(lines))
	}
	row := stripANSIForTrace(lines[r])
	i := strings.Index(row, label)
	if i < 0 {
		t.Fatalf("the dock never drew %q:\n%s", label, row)
	}
	// Columns, not bytes: the dock's mode glyph is multi-byte, so a byte offset
	// lands a couple of columns left of the label and hides a real misalignment.
	x0 = lipgloss.Width(row[:i])
	return x0, x0 + lipgloss.Width(label)
}

// TestDockMinimizedEntriesAreClickableWhereTheyAreDrawn is the reported bug:
// clicking a minimized entry did nothing while clicking a few columns to its
// right restored it. The click path recomputed the dock's geometry instead of
// reading what the renderer drew, and the two stopped agreeing once the left
// region grew the workspace strip.
//
// It runs with the strip on and off and with the dock at top and bottom,
// because the offset the recomputation got wrong is exactly the strip's width.
func TestDockMinimizedEntriesAreClickableWhereTheyAreDrawn(t *testing.T) {
	for _, strip := range []bool{true, false} {
		for _, pos := range []string{"bottom", "top"} {
			name := "strip-off"
			if strip {
				name = "strip-on"
			}
			t.Run(name+"/"+pos, func(t *testing.T) {
				prevStrip, prevPos := config.DockWorkspaceTabs, config.DockbarPosition
				config.DockWorkspaceTabs, config.DockbarPosition = strip, pos
				t.Cleanup(func() {
					config.DockWorkspaceTabs, config.DockbarPosition = prevStrip, prevPos
				})

				m := dockMinimizedOS(t)
				x0, x1 := dockEntryColumns(t, m, "1:alpha")
				y := m.GetDockbarContentYPosition()

				// Every column the entry occupies restores that entry.
				for x := x0; x < x1; x++ {
					if got := m.DockItemAt(x, y); got != 0 {
						t.Errorf("column %d of the alpha entry routed to window %d, want 0", x, got)
					}
				}
				// Its neighbours are not it.
				if got := m.DockItemAt(x0-2, y); got == 0 {
					t.Errorf("the column left of the alpha entry still routed to it")
				}
				if got := m.DockItemAt(x1+1, y); got == 0 {
					t.Errorf("the column right of the alpha entry still routed to it")
				}
				// The dock is one row.
				above := y - 1
				if config.DockbarPosition == "top" {
					above = y + 1
				}
				if got := m.DockItemAt(x0, above); got != -1 {
					t.Errorf("the row off the dock routed to window %d", got)
				}

				// The second entry is reachable and is a different window.
				bx0, bx1 := dockEntryColumns(t, m, "2:bravo")
				for x := bx0; x < bx1; x++ {
					if got := m.DockItemAt(x, y); got != 1 {
						t.Errorf("column %d of the bravo entry routed to window %d, want 1", x, got)
					}
				}

				// The entries are centred against the left and right regions, both
				// of which change width on their own (the mode pill, the stats
				// readout). The click has to answer for the frame on screen, so
				// state moving on without a repaint must not move the target.
				m.Mode = TerminalMode
				for x := x0; x < x1; x++ {
					if got := m.DockItemAt(x, y); got != 0 {
						t.Errorf("column %d of the alpha entry routed to %d after unrelated state moved on; the hit test is recomputing, not reading the frame", x, got)
					}
				}
			})
		}
	}
}
