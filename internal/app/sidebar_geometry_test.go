package app

import (
	"fmt"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// withSidebar sets the sidebar globals for a test and restores them after.
func withSidebar(t *testing.T, enabled bool, pos string, width int) {
	t.Helper()
	pe, pp, pw := config.SidebarEnabled, config.SidebarPosition, config.SidebarWidth
	config.SidebarEnabled = enabled
	config.SidebarPosition = pos
	config.SidebarWidth = width
	t.Cleanup(func() {
		config.SidebarEnabled, config.SidebarPosition, config.SidebarWidth = pe, pp, pw
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
	prevAnim := config.AnimationsEnabled
	config.AnimationsEnabled = false
	t.Cleanup(func() { config.AnimationsEnabled = prevAnim })

	m := &OS{
		NumWorkspaces:    9,
		CurrentWorkspace: 1,
		WorkspaceFocus:   make(map[int]int),
		Width:            width,
		Height:           height,
		AutoTiling:       true,
		UseBSPLayout:     true,
	}

	daemonState := &session.SessionState{
		Name:             "tiling",
		CurrentWorkspace: 1,
		AutoTiling:       true,
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
// daemon_tiling_test's assertions but against GetContentWidth.
func TestSidebarTilingPartitionsContentWidth(t *testing.T) {
	for _, pos := range []string{"left", "right"} {
		t.Run(pos, func(t *testing.T) {
			const width, height = 120, 40
			withSidebar(t, true, pos, config.SidebarDefaultWidth)

			m := tileDaemonWindows(t, width, height, 6)
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
