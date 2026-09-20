package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// A zoom has to mean something in each of the three tiled layouts, and it is
// not the same mechanism in all of them.
//
// BSP and master-stack get a camera: the layout is laid out on a canvas larger
// than the screen and the screen is panned to the pane you zoomed. The
// scrolling strip is already a camera, wider than the screen and showing what
// it cannot fit at the edges, so there it is the zoomed column's own width. In
// all three the pane is placed by the layout, and nothing hands it a rectangle
// beside that.

// tiledZoomOS is four panes under the named layout, with the slide off so a
// rectangle can be read straight after the toggle.
func tiledZoomOS(t *testing.T, mode string) (*OS, []*terminal.Window) {
	t.Helper()
	m, wins := zoomPeekOS(t)
	m.Settings.ZoomAnimation = false
	m.Settings.ZoomSize = 85
	m.AutoTiling = true
	m.FocusedWindow = 0

	switch mode {
	case config.LayoutModeBSP:
		m.UseBSPLayout = true
		for _, w := range wins {
			m.AddWindowToBSPTree(w)
		}
	case config.LayoutModeMasterStack:
		m.UseBSPLayout = false
	case config.LayoutModeScrolling:
		m.UseScrollingLayout = true
		for _, w := range wins {
			m.ScrollingOnWindowAdded(w)
		}
	}
	m.TileAllWindows()
	return m, wins
}

// TestAPartialZoomIsTheLayoutsJobInEveryTiler pins the rule every layout shares:
// the tiler places the zoomed pane, so no box is handed to it beside that.
//
// Without this the pane was given the whole region by applyZoomRect and then
// the layout placed it somewhere else on the same frame, which is two answers
// to one question and the wrong one winning by being last.
func TestAPartialZoomIsTheLayoutsJobInEveryTiler(t *testing.T) {
	for _, mode := range []string{
		config.LayoutModeBSP,
		config.LayoutModeMasterStack,
		config.LayoutModeScrolling,
	} {
		t.Run(mode, func(t *testing.T) {
			m, wins := tiledZoomOS(t, mode)
			if !m.zoomUsesLayout(wins[0]) {
				t.Fatal("a partial zoom was not left to the layout")
			}

			m.FocusedWindow = 0
			m.ToggleZoom()
			m.CompleteAllAnimations()

			if !wins[0].Zoomed {
				t.Fatal("the pane did not zoom")
			}
			// Not the whole region, which is what applyZoomRect would have
			// given it and what the peek exists to avoid.
			if wins[0].Width >= m.GetContentWidth() && wins[0].Height >= m.GetUsableHeight() {
				t.Errorf("the pane took the whole region (%dx%d), so nothing is left to peek",
					wins[0].Width, wins[0].Height)
			}
			// And it did grow: a zoom that changes nothing is not a zoom.
			if wins[0].Width <= 1 || wins[0].Height <= 1 {
				t.Errorf("the pane came out at %dx%d", wins[0].Width, wins[0].Height)
			}
		})
	}
}

// TestAFullZoomTakesTheRegionInEveryTiler pins the default, which no layout may
// reinterpret: at 100 percent the pane takes the screen.
func TestAFullZoomTakesTheRegionInEveryTiler(t *testing.T) {
	for _, mode := range []string{
		config.LayoutModeBSP,
		config.LayoutModeMasterStack,
		config.LayoutModeScrolling,
	} {
		t.Run(mode, func(t *testing.T) {
			m, wins := tiledZoomOS(t, mode)
			m.Settings.ZoomSize = 100
			if m.zoomUsesLayout(wins[0]) {
				t.Fatal("a full zoom was left to the layout")
			}

			m.FocusedWindow = 0
			m.ToggleZoom()
			m.CompleteAllAnimations()

			if wins[0].Width != m.GetContentWidth() || wins[0].Height != m.GetUsableHeight() {
				t.Errorf("the pane took %dx%d, want the whole region %dx%d",
					wins[0].Width, wins[0].Height, m.GetContentWidth(), m.GetUsableHeight())
			}
		})
	}
}

// TestUnzoomingPutsThePaneBackInEveryTiler pins the way out. A zoom that cannot
// be undone cleanly is worse than no zoom.
func TestUnzoomingPutsThePaneBackInEveryTiler(t *testing.T) {
	for _, mode := range []string{
		config.LayoutModeBSP,
		config.LayoutModeMasterStack,
		config.LayoutModeScrolling,
	} {
		t.Run(mode, func(t *testing.T) {
			m, wins := tiledZoomOS(t, mode)
			m.FocusedWindow = 0
			before := [4]int{wins[0].X, wins[0].Y, wins[0].Width, wins[0].Height}

			m.ToggleZoom()
			m.CompleteAllAnimations()
			m.ToggleZoom()
			m.CompleteAllAnimations()

			if wins[0].Zoomed {
				t.Fatal("the pane is still marked zoomed")
			}
			if got := [4]int{wins[0].X, wins[0].Y, wins[0].Width, wins[0].Height}; got != before {
				t.Errorf("the pane came back to %v, want the tile it left %v", got, before)
			}
		})
	}
}
