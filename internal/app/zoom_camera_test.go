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

// TestTheCameraLiftsTheZoomedPaneToItsShare pins the scale: whatever the pane
// started as, it ends up taking about the configured percentage of the screen
// on the axis it had room to grow on.
func TestTheCameraLiftsTheZoomedPaneToItsShare(t *testing.T) {
	for _, mode := range []string{config.LayoutModeBSP, config.LayoutModeMasterStack} {
		t.Run(mode, func(t *testing.T) {
			m, wins := camOS(t, mode, 90)
			before := wins[0].Width * wins[0].Height

			zoomAndSettle(m, 0)

			after := wins[0].Width * wins[0].Height
			if after <= before {
				t.Errorf("the zoomed pane went from %d cells to %d: it was not lifted", before, after)
			}
			if wins[0].Width > m.GetContentWidth() || wins[0].Height > m.GetUsableHeight() {
				t.Errorf("the zoomed pane is %dx%d, past the %dx%d screen",
					wins[0].Width, wins[0].Height, m.GetContentWidth(), m.GetUsableHeight())
			}
		})
	}
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

// TestTheCameraShowsTheNeighboursItHas pins where the peek falls: on the sides
// the zoomed pane has neighbours on, because the camera stops at the edge of
// the box rather than running off it.
func TestTheCameraShowsTheNeighboursItHas(t *testing.T) {
	m, wins := camOS(t, config.LayoutModeBSP, 85)
	// Pane 0 is the first in the tree, so it is the one in the corner.
	zoomAndSettle(m, 0)

	z := wins[0]
	if z.X != m.GetLeftMargin() {
		t.Errorf("the zoomed pane starts at x=%d, want the screen's left %d: the camera ran off the box",
			z.X, m.GetLeftMargin())
	}
	if z.Y != m.GetTopMargin() {
		t.Errorf("the zoomed pane starts at y=%d, want the screen's top %d", z.Y, m.GetTopMargin())
	}
	// And something reaches onto the screen from the far side.
	var peeks bool
	for _, w := range m.Windows {
		if w == z {
			continue
		}
		if w.X < m.GetLeftMargin()+m.GetContentWidth() && w.X+w.Width > m.GetLeftMargin() &&
			w.Y < m.GetTopMargin()+m.GetUsableHeight() && w.Y+w.Height > m.GetTopMargin() {
			peeks = true
		}
	}
	if !peeks {
		t.Error("no neighbour reaches onto the screen, so the camera shows nothing but the zoomed pane")
	}
}

// TestAFullHeightPaneIsLiftedOnWidthAlone pins the per-axis rule.
//
// A pane that already spans the screen top to bottom cannot be lifted
// vertically: growing the box on that axis pushes the pane's own top and bottom
// off the screen, which loses the pane you asked to see.
func TestAFullHeightPaneIsLiftedOnWidthAlone(t *testing.T) {
	// The BSP spiral gives its first pane the full height of the region beside
	// the others, which is the shape this is about.
	m, wins := camOS(t, config.LayoutModeBSP, 90)
	master := wins[0]
	if master.Height != m.GetUsableHeight() {
		t.Fatalf("the first pane is %d rows in a %d row region, so it is not full height and this checks nothing",
			master.Height, m.GetUsableHeight())
	}

	zoomAndSettle(m, 0)

	if master.Height != m.GetUsableHeight() {
		t.Errorf("the pane is %d rows, want the region's %d: it was lifted on an axis it already spanned",
			master.Height, m.GetUsableHeight())
	}
	if master.Y != m.GetTopMargin() {
		t.Errorf("the pane starts at y=%d, want the screen's top %d", master.Y, m.GetTopMargin())
	}
}

// TestAFullZoomTakesNoCamera pins the default: at 100 percent the pane takes the
// screen and the rest is not drawn, which is what zoom has always been.
func TestAFullZoomTakesNoCamera(t *testing.T) {
	m, wins := camOS(t, config.LayoutModeBSP, 100)
	if _, ok := m.zoomCanvasBounds(wins[0], m.GetBSPBounds()); ok {
		t.Error("a full zoom built a camera")
	}
}

// TestALonePaneTakesNoCamera pins that a pane which is the whole layout is left
// alone. Lifting the camera would frame it against empty canvas.
func TestALonePaneTakesNoCamera(t *testing.T) {
	m, wins := camOS(t, config.LayoutModeBSP, 80)
	if _, ok := m.zoomCanvasBounds(wins[0], m.GetBSPBounds()); ok {
		t.Error("the only pane on the workspace built a camera, framing it against nothing")
	}
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
