package app

import (
	"fmt"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// withSidebar sets the sidebar globals for a test and restores them after. It
// also points the sidebar state file at a scratch directory so a test that
// toggles or reorders never touches the developer's real state.
func withSidebar(t *testing.T, enabled bool, pos string, width int) {
	t.Helper()
	pe, pp, pw := config.SidebarEnabled, config.SidebarPosition, config.SidebarWidth
	config.SidebarEnabled = enabled
	config.SidebarPosition = pos
	config.SidebarWidth = width
	dir := t.TempDir()
	prevDir := sidebarStateDir
	sidebarStateDir = func() string { return dir }
	t.Cleanup(func() {
		config.SidebarEnabled, config.SidebarPosition, config.SidebarWidth = pe, pp, pw
		sidebarStateDir = prevDir
	})
}

// TestGetSidebarWidthBreakpoints checks the single width-folding function against
// the documented breakpoints, and that the content area never goes negative or
// drops below the pane floor.
func TestGetSidebarWidthBreakpoints(t *testing.T) {
	withSidebar(t, true, "left", config.SidebarDefaultWidth)

	cases := []struct {
		renderW  int
		wantW    int
		wantName string
	}{
		{120, config.SidebarDefaultWidth, "full"},
		{90, config.SidebarDefaultWidth, "full at boundary"},
		{89, config.SidebarNarrowWidth, "narrow"},
		{60, config.SidebarNarrowWidth, "narrow at boundary"},
		{59, config.SidebarGlyphWidth, "glyph"},
		{40, config.SidebarGlyphWidth, "glyph at boundary"},
		{39, 0, "auto-hidden"},
		{10, 0, "tiny"},
		{0, 0, "unknown size"},
	}
	for _, c := range cases {
		t.Run(c.wantName, func(t *testing.T) {
			m := &OS{Width: c.renderW, Height: 40}
			got := m.GetSidebarWidth()
			if got != c.wantW {
				t.Errorf("render %d: GetSidebarWidth = %d, want %d", c.renderW, got, c.wantW)
			}
			if cw := m.GetContentWidth(); cw < 0 {
				t.Errorf("render %d: content width negative: %d", c.renderW, cw)
			}
			if got > 0 && m.GetContentWidth() < config.SidebarMinPaneFloor {
				t.Errorf("render %d: content %d below floor %d", c.renderW, m.GetContentWidth(), config.SidebarMinPaneFloor)
			}
		})
	}
}

// TestGetSidebarWidthOversizedConfigStepsDown checks the floor enforcement: a
// configured width too large for the screen steps down rather than starving the
// content area.
func TestGetSidebarWidthOversizedConfigStepsDown(t *testing.T) {
	withSidebar(t, true, "left", 100) // absurdly wide for a 100-col screen
	m := &OS{Width: 100, Height: 40}
	w := m.GetSidebarWidth()
	if w == 0 {
		t.Fatalf("sidebar hidden entirely; expected a step-down variant")
	}
	if m.GetContentWidth() < config.SidebarMinPaneFloor {
		t.Errorf("content %d below floor %d after step-down", m.GetContentWidth(), config.SidebarMinPaneFloor)
	}
}

// TestMarginsFollowPosition checks left/right margins track the configured side
// and that a hidden or disabled sidebar reserves nothing.
func TestMarginsFollowPosition(t *testing.T) {
	m := &OS{Width: 120, Height: 40}

	withSidebar(t, true, "left", config.SidebarDefaultWidth)
	if m.GetLeftMargin() != config.SidebarDefaultWidth || m.GetRightMargin() != 0 {
		t.Errorf("left sidebar: left=%d right=%d", m.GetLeftMargin(), m.GetRightMargin())
	}

	config.SidebarPosition = "right"
	if m.GetLeftMargin() != 0 || m.GetRightMargin() != config.SidebarDefaultWidth {
		t.Errorf("right sidebar: left=%d right=%d", m.GetLeftMargin(), m.GetRightMargin())
	}

	config.SidebarEnabled = false
	if m.GetLeftMargin() != 0 || m.GetRightMargin() != 0 || m.GetContentWidth() != 120 {
		t.Errorf("disabled sidebar still reserves space: left=%d right=%d content=%d",
			m.GetLeftMargin(), m.GetRightMargin(), m.GetContentWidth())
	}
}

// tileDaemonWindows drives the same daemon create/sync loop the tiling test uses,
// returning the client OS holding the tiled windows. The sidebar globals must be
// set before calling.
func tileDaemonWindows(t *testing.T, width, height, count int) *OS {
	t.Helper()
	return tileDaemonWindowsMode(t, width, height, count, LayoutModeBSP)
}

// tileDaemonWindowsMode is tileDaemonWindows for an explicit layout mode
// ("bsp", "master-stack", or "scrolling"), so the content-box assertions can
// run against every tiling path, not only the BSP one.
func tileDaemonWindowsMode(t *testing.T, width, height, count int, layoutMode string) *OS {
	t.Helper()
	prevAnim := config.AnimationsEnabled
	config.AnimationsEnabled = false
	t.Cleanup(func() { config.AnimationsEnabled = prevAnim })

	m := &OS{
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

// TestSidebarSnapBoundsUseContentRegion checks floating snap targets are the
// content region beside the sidebar, not the raw screen.
func TestSidebarSnapBoundsUseContentRegion(t *testing.T) {
	const width, height = 120, 40
	withSidebar(t, true, "left", config.SidebarDefaultWidth)

	m := &OS{Width: width, Height: height}
	leftMargin := m.GetLeftMargin()
	contentW := m.GetContentWidth()

	x, _, w, _ := m.calculateSnapBounds(SnapLeft)
	if x != leftMargin {
		t.Errorf("SnapLeft x = %d, want left margin %d", x, leftMargin)
	}
	if w != contentW/2 {
		t.Errorf("SnapLeft width = %d, want half the content width %d", w, contentW/2)
	}

	x, _, w, _ = m.calculateSnapBounds(SnapFullScreen)
	if x != leftMargin || w != contentW {
		t.Errorf("SnapFullScreen = x %d w %d, want x %d w %d", x, w, leftMargin, contentW)
	}

	x, _, w, _ = m.calculateSnapBounds(SnapRight)
	if x != leftMargin+contentW/2 || x+w != leftMargin+contentW {
		t.Errorf("SnapRight = x %d w %d, want the right half of [%d,%d)", x, w, leftMargin, leftMargin+contentW)
	}
}
