package app

import (
	"fmt"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// tileDaemonWindowsMode drives the same daemon create/sync loop the tiling test
// uses, returning the client OS holding the tiled windows, for an explicit
// layout mode ("bsp", "master-stack", or "scrolling"), so the content-box
// assertions can run against every tiling path, not only the BSP one. The
// sidebar globals must be set before calling.
func tileDaemonWindowsMode(t *testing.T, width, height, count int, layoutMode string) *OS {
	t.Helper()
	prevAnim := config.Global.AnimationsEnabled
	config.Global.AnimationsEnabled = false
	t.Cleanup(func() { config.Global.AnimationsEnabled = prevAnim })

	m := &OS{
		Settings:             config.Global,
		NumWorkspaces:        9,
		CurrentWorkspace:     1,
		WorkspaceFocus:       make(map[int]int),
		WorkspaceHasCustom:   make(map[int]bool),
		WorkspaceLayouts:     make(map[int][]WindowLayout),
		WorkspaceMasterRatio: make(map[int]float64),
		Width:                width,
		Height:               height,
		AutoTiling:           true,
	}
	m.ApplyLayoutModeName(layoutMode)

	daemonState := &session.SessionState{
		Name:             "tiling",
		CurrentWorkspace: 1,
		AutoTiling:       true,
		LayoutMode:       layoutMode,
		WorkspaceFocus:   map[int]string{},
		Version:          1,
	}

	for i := 0; i < count; i++ {
		id := fmt.Sprintf("win-%036d", i+1)
		daemonState.Windows = append(daemonState.Windows, session.WindowState{
			ID:        id,
			PTYID:     fmt.Sprintf("pty-%d", i+1),
			Title:     id,
			Width:     width,
			Height:    height,
			Workspace: 1,
			Unplaced:  true,
		})
		daemonState.FocusedWindowID = id
		daemonState.Version++

		if err := m.ApplyStateSync(daemonState); err != nil {
			t.Fatalf("window %d: ApplyStateSync: %v", i+1, err)
		}
		daemonState = m.BuildSessionState()
		daemonState.Version = i + 2
	}
	return m
}

// TestSidebarTilingPartitionsContentWidth asserts panes tile into the reduced
// content box beside the sidebar with no overlap and no large gap, mirroring
// daemon_tiling_test's assertions but against GetContentWidth, in BOTH
// non-scrolling tiling modes: the BSP tree and the master-stack tiler each have
// their own geometry path, and only the BSP one honored the margin at first.
func TestSidebarTilingPartitionsContentWidth(t *testing.T) {
	for _, mode := range []string{LayoutModeBSP, LayoutModeMasterStack} {
		for _, pos := range []string{"left", "right"} {
			t.Run(mode+"/"+pos, func(t *testing.T) {
				const width, height = 120, 40
				withSidebar(t, true, pos, config.SidebarDefaultWidth)

				m := tileDaemonWindowsMode(t, width, height, 6, mode)
				if len(m.Windows) != 6 {
					t.Fatalf("client holds %d windows, want 6", len(m.Windows))
				}

				leftMargin := m.GetLeftMargin()
				contentW := m.GetContentWidth()
				rightEdge := leftMargin + contentW
				top := m.GetTopMargin()

				type rect struct{ x, y, w, h int }
				rects := make([]rect, 0, 6)
				for _, w := range m.Windows {
					rects = append(rects, rect{w.X, w.Y, w.Width, w.Height})
					// Every pane sits inside the content region, never under the sidebar.
					if w.X < leftMargin {
						t.Errorf("window at x=%d starts before content left margin %d", w.X, leftMargin)
					}
					if w.X+w.Width > rightEdge {
						t.Errorf("window right edge %d exceeds content right edge %d", w.X+w.Width, rightEdge)
					}
					if w.Width >= contentW && contentW < width {
						t.Errorf("window spans the full content width %d: it was never tiled beside the sidebar", w.Width)
					}
				}

				for a := 0; a < len(rects); a++ {
					for b := a + 1; b < len(rects); b++ {
						if rectsOverlap(rects[a].x, rects[a].y, rects[a].w, rects[a].h,
							rects[b].x, rects[b].y, rects[b].w, rects[b].h) {
							t.Errorf("windows overlap: %+v and %+v", rects[a], rects[b])
						}
					}
				}

				area := 0
				for _, r := range rects {
					area += r.w * r.h
				}
				want := contentW * (height - top)
				if area < want*9/10 {
					t.Errorf("tiled area = %d, want about %d (panes leave a large gap in the content box)", area, want)
				}
			})
		}
	}
}

// TestSidebarFloatingClampRespectsReservedRegion checks a floating pane cannot be
// left hidden under the sidebar: ClampWindowsToView keeps it inside the content
// region.
func TestSidebarFloatingClampRespectsReservedRegion(t *testing.T) {
	const width, height = 120, 40
	withSidebar(t, true, "left", config.SidebarDefaultWidth)

	m := &OS{
		Settings:         config.Global,
		NumWorkspaces:    9,
		CurrentWorkspace: 1,
		WorkspaceFocus:   make(map[int]int),
		Width:            width,
		Height:           height,
		AutoTiling:       false,
	}
	// A floating window shoved fully into the reserved band on the left.
	win := &terminal.Window{ID: "float", X: -5, Y: 5, Width: 20, Height: 10, Workspace: 1}
	m.Windows = []*terminal.Window{win}

	m.ClampWindowsToView()

	leftMargin := m.GetLeftMargin()
	minVisibleX := 20
	if win.X+win.Width < leftMargin+minVisibleX {
		t.Errorf("floating window clamped to x=%d (w=%d) is not visible past the sidebar (leftMargin=%d)",
			win.X, win.Width, leftMargin)
	}
	if win.X+win.Width > leftMargin+m.GetContentWidth() {
		t.Errorf("floating window right edge %d exceeds content region %d",
			win.X+win.Width, leftMargin+m.GetContentWidth())
	}
}

// TestSidebarScrollingStripStartsAtLeftMargin checks the scrolling (niri-style)
// layout lays its strip out inside the content box: with the viewport at the
// strip's origin, the first column starts at the left margin rather than at
// screen column zero underneath the sidebar, and every column is sized against
// the content width.
func TestSidebarScrollingStripStartsAtLeftMargin(t *testing.T) {
	const width, height = 120, 40
	withSidebar(t, true, "left", config.SidebarDefaultWidth)

	m := tileDaemonWindowsMode(t, width, height, 2, LayoutModeScrolling)
	if len(m.Windows) != 2 {
		t.Fatalf("client holds %d windows, want 2", len(m.Windows))
	}

	// Settle the slide animations from the create loop first, then reposition
	// from the strip origin and settle again, so the assertion reads final
	// geometry rather than a mid-slide frame.
	m.CompleteAllAnimations()
	sl := m.GetOrCreateScrollingLayout()
	sl.ViewportX = 0
	m.ScrollingSetPositions()
	m.CompleteAllAnimations()

	leftMargin := m.GetLeftMargin()
	contentW := m.GetContentWidth()

	minX := m.Windows[0].X
	for _, w := range m.Windows {
		if w.X < minX {
			minX = w.X
		}
		if w.Width > contentW*9/10 {
			t.Errorf("column width %d exceeds 90%% of the content width %d", w.Width, contentW)
		}
	}
	if minX != leftMargin {
		t.Errorf("scrolling strip starts at x=%d, want the left margin %d", minX, leftMargin)
	}
}

// TestSidebarResizeCannotEnterReservedBand checks a keyboard tiling resize is
// blocked at the content-region edges: the left edge cannot be pushed under a
// left sidebar, and the right edge cannot be pushed under a right sidebar.
func TestSidebarResizeCannotEnterReservedBand(t *testing.T) {
	t.Run("left-edge", func(t *testing.T) {
		const width, height = 120, 40
		withSidebar(t, true, "left", config.SidebarDefaultWidth)

		m := tileDaemonWindowsMode(t, width, height, 2, LayoutModeMasterStack)
		// Focus the window sitting against the left margin.
		leftMargin := m.GetLeftMargin()
		for i, w := range m.Windows {
			if w.X == leftMargin {
				m.FocusedWindow = i
			}
		}
		win := m.Windows[m.FocusedWindow]
		beforeX, beforeW := win.X, win.Width

		// Grow from the left edge: would move X to leftMargin-2, under the band.
		m.ResizeFocusedWindowWidthLeft(-2)

		if win.X != beforeX || win.Width != beforeW {
			t.Errorf("left-edge resize entered the reserved band: x=%d w=%d (was x=%d w=%d, margin=%d)",
				win.X, win.Width, beforeX, beforeW, leftMargin)
		}
		if win.X < leftMargin {
			t.Errorf("window x=%d is under the sidebar (margin=%d)", win.X, leftMargin)
		}
	})

	t.Run("right-edge", func(t *testing.T) {
		const width, height = 120, 40
		withSidebar(t, true, "right", config.SidebarDefaultWidth)

		m := tileDaemonWindowsMode(t, width, height, 2, LayoutModeMasterStack)

		m.Settings = config.Global
		contentRight := m.GetLeftMargin() + m.GetContentWidth()
		for i, w := range m.Windows {
			if w.X+w.Width == contentRight {
				m.FocusedWindow = i
			}
		}
		win := m.Windows[m.FocusedWindow]
		beforeRight := win.X + win.Width

		// The right edge is the sidebar band, not a divider, so > grows the pane
		// from its left edge instead. The right edge must stay put and never cross
		// into the reserved band.
		m.ResizeFocusedWindowWidth(2)

		if win.X+win.Width > contentRight {
			t.Errorf("window right edge %d is under the sidebar (contentRight=%d)", win.X+win.Width, contentRight)
		}
		if win.X+win.Width != beforeRight {
			t.Errorf("right edge moved (%d -> %d); the grow should have used the left edge", beforeRight, win.X+win.Width)
		}
		if win.Width <= 0 {
			t.Fatalf("window collapsed: w=%d", win.Width)
		}
	})
}
