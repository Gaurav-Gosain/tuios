package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// Zooming a pane to part of the screen is a camera move over the layout, not a
// resize of the one pane.
//
// The layout is laid out in a box larger than the screen and the screen is
// panned to the pane you zoomed. Every pane keeps its place and its proportions
// relative to every other; the zoomed one is the one the camera is on, and its
// neighbours run off the edges because the box is bigger than the window you
// are looking through.
//
// These drive the tilers rather than the transform, because the transform is
// half the answer: the other half is that the layout is computed again at the
// camera's size, and only a test that goes through the tiler can see it.

// camOS is n panes under the named layout with a camera zoom configured, laid
// out and settled.
func camOS(t *testing.T, mode string, pct int) (*OS, []*terminal.Window) {
	t.Helper()
	m, wins := tiledZoomOS(t, mode)
	m.Settings.ZoomSize = pct
	return m, wins
}

// zoomAndSettle zooms the pane at i and lands every slide.
func zoomAndSettle(m *OS, i int) {
	m.FocusedWindow = i
	m.ToggleZoom()
	m.CompleteAllAnimations()
}

// TestTheCameraKeepsTheGapsTheSize is the property that decided how this is
// built.
//
// The camera used to stretch the tiler's finished rectangles, which scaled the
// gaps between panes along with the panes. A gap is not content: it is the one
// column a shared border is drawn in. At any zoom past about 1.5x that column
// became two, which drew fat dividers under BSP and none at all under
// master-stack, whose divider finder looks for neighbours exactly one gap
// apart. The layout is computed in the larger box instead, so every gap is the
// width the user asked for whatever the camera is doing.
func TestTheCameraKeepsTheGapsTheSize(t *testing.T) {
	for _, mode := range []string{config.LayoutModeBSP, config.LayoutModeMasterStack} {
		t.Run(mode, func(t *testing.T) {
			m, _ := camOS(t, mode, 85)
			m.SharedBorders = true
			m.TileAllWindows()
			m.CompleteAllAnimations()
			gap := m.separatorGap()
			if gap <= 0 {
				t.Fatal("no gap to check")
			}
			plain := smallestGapBetweenPanes(m)

			zoomAndSettle(m, 0)

			if got := smallestGapBetweenPanes(m); got != plain {
				t.Errorf("the closest two panes are %d cells apart under the camera, want the %d they were",
					got, plain)
			}
		})
	}
}

// smallestGapBetweenPanes is the narrowest horizontal run of empty columns
// between two panes that share rows, or -1 when no two do.
func smallestGapBetweenPanes(m *OS) int {
	best := -1
	for _, a := range m.Windows {
		for _, b := range m.Windows {
			if a == b || a.Workspace != m.CurrentWorkspace || b.Workspace != m.CurrentWorkspace {
				continue
			}
			// b starts to the right of a, and they overlap vertically.
			if b.X <= a.X+a.Width {
				continue
			}
			if a.Y >= b.Y+b.Height || b.Y >= a.Y+a.Height {
				continue
			}
			if d := b.X - (a.X + a.Width); best < 0 || d < best {
				best = d
			}
		}
	}
	return best
}

// TestEverySideWithANeighbourPeeks is the report: zooming the middle pane of a
// grid showed the panes beside it but nothing of the ones above and below.
//
// It was not a rounding slip. A percentage buys very different numbers of cells
// on the two axes, because a screen is far wider in columns than it is tall in
// rows: at 95 percent of a 160 by 42 region the leftover is eight columns and
// two rows, so the sides showed four columns each and the top and bottom showed
// one row each. One row is the neighbour's border and nothing else, which reads
// as a line the layout forgot to remove.
// Master-stack with nine panes is a three by three grid, so its middle pane has
// a neighbour on all four sides. That is the arrangement the report is about
// and the only one where all four can be checked at once: the BSP spiral does
// not make a grid, and its middle pane genuinely has no neighbour on some
// sides.
func TestEverySideWithANeighbourPeeks(t *testing.T) {
	for _, mode := range []string{config.LayoutModeMasterStack} {
		t.Run(mode, func(t *testing.T) {
			m, wins := gridZoomOS(t, mode, 9, 95)
			middle := 4
			zoomAndSettle(m, middle)
			z := wins[middle]

			left := z.X - m.GetLeftMargin()
			right := m.GetLeftMargin() + m.GetContentWidth() - (z.X + z.Width)
			top := z.Y - m.GetTopMargin()
			bottom := m.GetTopMargin() + m.GetUsableHeight() - (z.Y + z.Height)

			for _, side := range []struct {
				name string
				got  int
			}{{"left", left}, {"right", right}, {"top", top}, {"bottom", bottom}} {
				if side.got < zoomPeekMinCells {
					t.Errorf("the %s side shows %d cells, want at least %d: the neighbour there is a line rather than a pane",
						side.name, side.got, zoomPeekMinCells)
				}
			}
		})
	}
}

// gridZoomOS is n panes under the named layout at the given zoom size, laid out
// and settled, in a region wide enough for a grid.
func gridZoomOS(t *testing.T, mode string, n, pct int) (*OS, []*terminal.Window) {
	t.Helper()
	prev := config.Global
	t.Cleanup(func() { config.Global = prev })

	var wins []*terminal.Window
	for i := range n {
		w := newTestWindow(t, string(rune('a'+i))+"0000000000000000000000000000000", 40, 20)
		w.Workspace = 1
		wins = append(wins, w)
	}
	m := &OS{
		Settings: config.Global, Windows: wins, FocusedWindow: 0,
		WorkspaceFocus: map[int]int{}, NumWorkspaces: 9, CurrentWorkspace: 1,
		Width: 160, Height: 44, PendingResizes: map[string][2]int{},
	}
	m.Settings.ZoomSize = pct
	m.Settings.ZoomAnimation = false
	m.AutoTiling = true
	switch mode {
	case config.LayoutModeBSP:
		m.UseBSPLayout = true
		for _, w := range wins {
			m.AddWindowToBSPTree(w)
		}
	default:
		m.UseBSPLayout = false
	}
	m.TileAllWindows()
	m.CompleteAllAnimations()
	return m, wins
}
