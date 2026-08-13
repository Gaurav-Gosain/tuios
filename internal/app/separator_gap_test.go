package app

import (
	"fmt"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// The columns a division between two panes is allowed to hold.
//
// With shared borders and tiled panes the panes are borderless, so the division
// is one column wide and the separator overlay paints it. Every other cell has
// each pane drawing its own border inside its own rectangle, so the two
// adjacent border columns are the division and a third column would hold
// nothing. Reserving it there wastes a column of the user's screen on every
// split.
func wantGap(tiling, shared bool) int {
	if tiling && shared {
		return 1
	}
	return 0
}

// dividedNeighbours returns, for every pair of panes that face each other
// across a vertical or horizontal division, the number of cells between them.
func dividedNeighbours(m *OS) []int {
	var vis []*terminal.Window
	for _, w := range m.Windows {
		if w.Workspace == m.CurrentWorkspace && !w.Minimized && !w.IsFloating {
			vis = append(vis, w)
		}
	}
	var gaps []int
	for _, a := range vis {
		for _, b := range vis {
			if a == b {
				continue
			}
			// b sits to the right of a and their rows overlap.
			if b.X > a.X && a.Y < b.Y+b.Height && b.Y < a.Y+a.Height {
				if g := b.X - (a.X + a.Width); g >= 0 && g <= 2 {
					gaps = append(gaps, g)
				}
			}
			// b sits below a and their columns overlap.
			if b.Y > a.Y && a.X < b.X+b.Width && b.X < a.X+a.Width {
				if g := b.Y - (a.Y + a.Height); g >= 0 && g <= 2 {
					gaps = append(gaps, g)
				}
			}
		}
	}
	return gaps
}

func checkGaps(t *testing.T, m *OS, tiling, shared bool, label string) {
	t.Helper()
	want := wantGap(tiling, shared)
	gaps := dividedNeighbours(m)
	if len(gaps) == 0 {
		t.Fatalf("%s: found no adjacent panes to measure", label)
	}
	for _, g := range gaps {
		if g != want {
			t.Errorf("%s: division holds %d columns, want %d (tiling=%v shared=%v)",
				label, g, want, tiling, shared)
		}
	}
}

// TestDivisionReservesOnlyWhatItDraws walks the four cells of the
// tiling-by-shared-borders matrix and pins the width of the division between
// two panes in each, with the sidebar off, left and right since the content
// region's width moves with it.
func TestDivisionReservesOnlyWhatItDraws(t *testing.T) {
	prevShared, prevSide, prevPos := config.SharedBorders, config.SidebarEnabled, config.SidebarPosition
	t.Cleanup(func() {
		config.SharedBorders, config.SidebarEnabled, config.SidebarPosition = prevShared, prevSide, prevPos
	})

	for _, side := range []struct {
		name string
		on   bool
		pos  string
	}{{"sidebar-off", false, "left"}, {"sidebar-left", true, "left"}, {"sidebar-right", true, "right"}} {
		for _, mode := range []string{LayoutModeBSP, LayoutModeMasterStack} {
			for _, shared := range []bool{false, true} {
				for _, tiling := range []bool{true, false} {
					name := fmt.Sprintf("%s/%s/shared=%v/tiling=%v", side.name, mode, shared, tiling)
					t.Run(name, func(t *testing.T) {
						config.SidebarEnabled, config.SidebarPosition = side.on, side.pos
						config.SharedBorders = shared
						m := gapTestOS(t, 2)
						m.UseBSPLayout = true
						m.TileAllWindows()
						m.ApplyLayoutModeName(mode)
						m.TileAllWindows()
						if !tiling {
							m.ToggleAutoTiling()
						}
						checkGaps(t, m, tiling, shared, name)
					})
				}
			}
		}
	}
}
