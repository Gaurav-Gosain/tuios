package app

import (
	"fmt"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// A division is two panes facing each other across a split, and the cells the
// layout left between their rectangles.
type division struct {
	a, b     *terminal.Window
	cells    int
	vertical bool
}

// wantCells is what a division between these two panes is allowed to hold.
//
// It reads the answer off the panes as drawn rather than off the setting that
// put them that way. Borderless panes are guest output edge to edge, so the
// divider the user sees needs a column of its own between them. Panes drawing
// their own borders have two adjacent border columns doing that job already,
// and a third column would draw nothing while costing both panes a column.
func wantCells(d division) int {
	if d.a.Tiled && d.b.Tiled {
		return 1
	}
	return 0
}

// paneDivisions finds every pair of visible panes that face each other across a
// split. Pairs further apart than a division could plausibly be are ignored:
// they are separated by a third pane, not by a divider.
func paneDivisions(m *OS) []division {
	var vis []*terminal.Window
	for _, w := range m.Windows {
		if w.Workspace == m.CurrentWorkspace && !w.Minimized && !w.IsFloating {
			vis = append(vis, w)
		}
	}
	var out []division
	for _, a := range vis {
		for _, b := range vis {
			if a == b {
				continue
			}
			if b.X > a.X && a.Y < b.Y+b.Height && b.Y < a.Y+a.Height {
				if g := b.X - (a.X + a.Width); g >= 0 && g <= 2 {
					out = append(out, division{a: a, b: b, cells: g, vertical: true})
				}
			}
			if b.Y > a.Y && a.X < b.X+b.Width && b.X < a.X+a.Width {
				if g := b.Y - (a.Y + a.Height); g >= 0 && g <= 2 {
					out = append(out, division{a: a, b: b, cells: g})
				}
			}
		}
	}
	return out
}

// checkDivisions asserts no division holds a cell nothing is drawn in. It is
// called from checkPaneSizes so that every layout transition the announce
// matrix already walks is checked for a stranded gap as well.
func checkDivisions(t *testing.T, m *OS, label string) {
	t.Helper()
	for _, d := range paneDivisions(m) {
		if want := wantCells(d); d.cells != want {
			t.Errorf("%s: division between %s and %s holds %d cells, want %d (tiled %v/%v)",
				label, d.a.ID, d.b.ID, d.cells, want, d.a.Tiled, d.b.Tiled)
		}
	}
}

// TestLiveToggleReclaimsTheDivider drives both settings in both directions at
// runtime. Each toggle has to re-lay-out: a layout computed under the other
// setting and then left alone strands its panes around a column nobody fills,
// which is the shape of failure this area keeps producing.
func TestLiveToggleReclaimsTheDivider(t *testing.T) {
	prevShared := config.Global.SharedBorders
	t.Cleanup(func() { config.Global.SharedBorders = prevShared })

	for _, mode := range []string{LayoutModeBSP, LayoutModeMasterStack} {
		for _, start := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/from-shared=%v", mode, start), func(t *testing.T) {
				config.Global.SharedBorders = start
				m := gapTestOS(t, 3)
				m.UseBSPLayout = true
				m.TileAllWindows()
				m.ApplyLayoutModeName(mode)
				m.TileAllWindows()

				step := func(label string) {
					t.Helper()
					checkDivisions(t, m, label)
				}
				step("settled")

				setSharedBorders(m, !start)
				step("shared toggled")
				setSharedBorders(m, start)
				step("shared toggled back")

				m.ToggleAutoTiling()
				step("tiling off")
				m.ToggleAutoTiling()
				step("tiling on")

				// Off again, then flip the setting underneath a layout that is
				// no longer tiling, and back on: no combination may leave a
				// column stranded.
				m.ToggleAutoTiling()
				setSharedBorders(m, !start)
				step("tiling off, shared flipped")
				setSharedBorders(m, start)
				step("tiling off, shared restored")
				m.ToggleAutoTiling()
				step("tiling on again")
			})
		}
	}
}

// TestDividerReclaimReachesEveryWorkspace pins the stranding case: tiling is
// turned off while another workspace is on screen, so the workspace holding the
// gap is not the one being retiled. Its panes must not be left drawing their
// own boxes inside rectangles still spaced for a separator.
func TestDividerReclaimReachesEveryWorkspace(t *testing.T) {
	prevShared := config.Global.SharedBorders
	config.Global.SharedBorders = true
	t.Cleanup(func() { config.Global.SharedBorders = prevShared })

	m := gapTestOS(t, 4)
	m.UseBSPLayout = true
	m.Windows[2].Workspace = 2
	m.Windows[3].Workspace = 2
	m.TileAllWindows()
	m.SwitchToWorkspace(2)
	m.TileAllWindows()
	m.SwitchToWorkspace(1)

	m.ToggleAutoTiling()
	checkDivisions(t, m, "workspace 1 after toggle")
	m.SwitchToWorkspace(2)
	checkDivisions(t, m, "workspace 2 after toggle")
}
