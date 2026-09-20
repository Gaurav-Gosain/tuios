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
	// The tilers slide panes into place, so the rectangles are not the tiled
	// ones until the slide lands. A test that reads a rectangle straight after
	// laying out reads where the panes were before it.
	m.CompleteAllAnimations()
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

// TestOpeningAPaneWhileZoomedGivesItTheZoom is the report: creating a window
// while zoomed bugged the layout out.
//
// This drives the daemon path, which is the one a session actually takes: the
// daemon creates the pane and marks it Unplaced, the client places it, and the
// focus arrives by assignment rather than through FocusWindow. So the handover
// that key presses get never ran, and the new pane was focused underneath
// somebody else's zoom, which is a pane you are typing into and cannot see.
func TestOpeningAPaneWhileZoomedGivesItTheZoom(t *testing.T) {
	for _, size := range []int{100, 85} {
		name := "full"
		if size < 100 {
			name = "camera"
		}
		t.Run(name, func(t *testing.T) {
			prev := config.Global.AnimationsEnabled
			config.Global.AnimationsEnabled = false
			defer func() { config.Global.AnimationsEnabled = prev }()

			h := newOpenAnimHarness(120, 40)
			h.m.Settings.ZoomSize = size
			h.m.Settings.ZoomAnimation = false
			h.createWindow(t)
			h.createWindow(t)

			first := h.m.Windows[0]
			h.m.FocusedWindow = 0
			h.m.ToggleZoom()
			h.m.CompleteAllAnimations()
			if !first.Zoomed {
				t.Fatal("setup: the pane did not zoom")
			}
			// Zoom is session state, and pressing the key pushes it. Without
			// this the daemon's copy still predates the zoom and the next sync
			// takes it straight back off, which is the harness lying rather
			// than the code failing.
			h.state = h.m.BuildSessionState()
			h.state.Version = h.next + 1

			h.createWindow(t)
			h.m.CompleteAllAnimations()

			fresh := h.m.Windows[len(h.m.Windows)-1]
			if h.m.GetFocusedWindow() != fresh {
				t.Fatal("the new pane is not focused")
			}
			if !fresh.Zoomed {
				t.Error("the new pane is focused underneath somebody else's zoom")
			}
			if first.Zoomed {
				t.Error("the pane that had the zoom kept it")
			}
			zoomedCount := 0
			for _, w := range h.m.Windows {
				if w.Zoomed {
					zoomedCount++
				}
			}
			if zoomedCount != 1 {
				t.Errorf("%d panes hold the zoom, want one", zoomedCount)
			}

			// And it has a rectangle the layout chose, not the nominal box the
			// daemon handed over.
			if fresh.Width <= 0 || fresh.Height <= 0 {
				t.Fatalf("the new pane is %dx%d", fresh.Width, fresh.Height)
			}
			if fresh.Width > h.m.GetContentWidth() || fresh.Height > h.m.GetUsableHeight() {
				t.Errorf("the new pane is %dx%d, larger than the %dx%d region",
					fresh.Width, fresh.Height, h.m.GetContentWidth(), h.m.GetUsableHeight())
			}
		})
	}
}

// TestACameraZoomKeepsTheSharedBorders pins that the dividers survive a zoom of
// part of the screen, and follow the camera rather than staying at the size the
// layout was computed at.
func TestACameraZoomKeepsTheSharedBorders(t *testing.T) {
	for _, mode := range []string{config.LayoutModeBSP, config.LayoutModeMasterStack} {
		t.Run(mode, func(t *testing.T) {
			m, _ := tiledZoomOS(t, mode)
			// Shared borders on, and the layout run again so the panes get the
			// borderless rectangles that reserve a column for each divider.
			m.SharedBorders = true
			m.TileAllWindows()
			m.CompleteAllAnimations()
			if !m.panesBorderless() {
				t.Fatal("shared borders did not take, so this checks nothing")
			}

			plain := m.separatorSplits()
			if len(plain) == 0 {
				t.Fatal("no dividers without a zoom, so this checks nothing")
			}

			m.FocusedWindow = 0
			m.ToggleZoom()
			m.CompleteAllAnimations()

			zoomed := m.separatorSplits()
			if len(zoomed) == 0 {
				t.Fatal("a camera zoom drew no dividers at all")
			}
			// They moved with the layout: a camera that scaled the panes and
			// left the dividers where they were is the thing this is about.
			same := len(zoomed) == len(plain)
			if same {
				for i := range zoomed {
					if zoomed[i] != plain[i] {
						same = false
						break
					}
				}
			}
			if same {
				t.Error("the dividers did not move with the camera")
			}
		})
	}
}

// TestAddWindowPlacesThePaneBeforeFocusingIt covers the local creation path,
// which is the one a session without a daemon takes.
//
// Focusing hands a pane the workspace's zoom, and the handover retiles. A
// retile that meets a pane the tree has never been told about inserts it
// wherever its repair path can rather than where the creation is about to put
// it, and records its pre-zoom rectangle as the raw creation box it has not
// left yet. So the pane has to be placed first.
func TestAddWindowPlacesThePaneBeforeFocusingIt(t *testing.T) {
	m := newStartupOS(t, false, true)
	defer closeWindows(m)
	m.AutoTiling, m.UseBSPLayout = true, true
	m.Settings.ZoomSize = 85
	m.Settings.ZoomAnimation = false

	m.AddWindow("")
	m.AddWindow("")
	if len(m.Windows) != 2 {
		t.Fatalf("setup: %d windows, want 2", len(m.Windows))
	}
	m.CompleteAllAnimations()

	m.FocusedWindow = 0
	m.ToggleZoom()
	m.CompleteAllAnimations()
	if !m.Windows[0].Zoomed {
		t.Fatal("setup: the pane did not zoom")
	}

	m.AddWindow("")
	m.CompleteAllAnimations()

	fresh := m.Windows[len(m.Windows)-1]
	if m.GetFocusedWindow() != fresh {
		t.Fatal("the new pane is not focused")
	}
	if !fresh.Zoomed {
		t.Error("the new pane is focused underneath somebody else's zoom")
	}
	// It is in the tiling structure, at a rectangle the layout chose. A pane
	// the handover retiled around before the creation placed it came out at the
	// creation box, which is half the screen centred on it.
	if fresh.Width > m.GetContentWidth() || fresh.Height > m.GetUsableHeight() {
		t.Errorf("the new pane is %dx%d, larger than the %dx%d region",
			fresh.Width, fresh.Height, m.GetContentWidth(), m.GetUsableHeight())
	}
	if !m.GetOrCreateBSPTree().HasWindow(m.getWindowIntID(fresh.ID)) {
		t.Error("the new pane is not in the tiling tree")
	}
	// And the tree holds each pane once: the repair path inserting it and the
	// creation inserting it again is how it ended up in two places.
	if got, want := m.GetOrCreateBSPTree().WindowCount(), len(m.Windows); got != want {
		t.Errorf("the tree holds %d panes, want %d", got, want)
	}
}
