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

// TestTheZoomStaysOnTheFocusedPaneUnderACamera is the report: zoomed in, the
// next-pane key focused the pane after it and the zoom appeared to come off.
//
// Under a camera the zoom is which pane the layout is aimed at rather than a
// box somebody holds, so moving it means moving the mark and letting the tiler
// aim again. The handover moved the mark and never retiled, so the layout went
// on being drawn at the arrangement it already had: the focus had moved, the
// mark had moved, and nothing had been redrawn against either.
func TestTheZoomStaysOnTheFocusedPaneUnderACamera(t *testing.T) {
	for _, mode := range []string{
		config.LayoutModeBSP,
		config.LayoutModeMasterStack,
		config.LayoutModeScrolling,
	} {
		t.Run(mode, func(t *testing.T) {
			m, wins := tiledZoomOS(t, mode)
			m.FocusedWindow = 0

			m.ToggleZoom()
			m.CompleteAllAnimations()
			// Where the pane about to receive the zoom sits while somebody
			// else has it: pushed off the edges by the camera aimed at pane 0.
			// Taking the zoom has to bring it back and lift it.
			before := [4]int{wins[1].X, wins[1].Y, wins[1].Width, wins[1].Height}

			m.FocusWindow(1)
			m.CompleteAllAnimations()

			if wins[0].Zoomed {
				t.Error("the pane that lost the focus kept the zoom")
			}
			if !wins[1].Zoomed {
				t.Fatal("the pane the focus reached did not take the zoom")
			}
			// And the layout was actually redrawn against the new mark.
			// Without the retile the pane sat at the tile it had all along,
			// with nothing but a flag to say it was zoomed, which is exactly
			// what the zoom coming off looks like.
			got := [4]int{wins[1].X, wins[1].Y, wins[1].Width, wins[1].Height}
			if got == before {
				t.Errorf("the pane that took the zoom never moved from %v: the layout was never re-aimed", before)
			}
			// And it came back onto the screen, which is what the camera being
			// re-aimed means: while somebody else held the zoom this pane was
			// pushed past the edges.
			if got[0] >= m.GetLeftMargin()+m.GetContentWidth() || got[1] >= m.GetTopMargin()+m.GetUsableHeight() {
				t.Errorf("the pane that took the zoom is at %v, off the screen", got)
			}
		})
	}
}

// TestTheHandoverSlidesUnderEveryTiler pins the second half of the report: it
// has to be smooth. Every layout arms something to move, rather than cutting
// from one arrangement to the next.
func TestTheHandoverSlidesUnderEveryTiler(t *testing.T) {
	for _, mode := range []string{
		config.LayoutModeBSP,
		config.LayoutModeMasterStack,
	} {
		t.Run(mode, func(t *testing.T) {
			m, _ := tiledZoomOS(t, mode)
			m.Settings.ZoomAnimation = true
			m.FocusedWindow = 0

			m.ToggleZoom()
			m.CompleteAllAnimations()

			m.Animations = nil
			m.FocusWindow(1)

			if len(m.Animations) == 0 {
				t.Error("the handover moved nothing: the layout cut from one arrangement to the next")
			}
		})
	}
}
