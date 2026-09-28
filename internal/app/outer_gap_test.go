package app

import (
	"fmt"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// appearance.outer_gap insets the pane region on every side and leaves the
// chrome where it was. These tests read the rectangles the tilers, snap, zoom
// and floating placement produce and hold them to the region worked out by
// hand from the chrome: the dock's two rows, the rail's width, the screen.

const (
	gapCols, gapRows = 120, 40
	gapDockRows      = 2 // config.DockHeight: the dock row and its rule
)

// outerGapOS is a three pane session in the named mode with the dock and the
// rail placed as asked and the outer gap set.
func outerGapOS(t *testing.T, mode, dock, rail string, gap int) *OS {
	t.Helper()
	m := modeOS(t, mode, false, 0, 3, gapCols, gapRows)
	m.Settings.DockbarPosition = dock
	m.Settings.SidebarEnabled = rail != "hidden"
	if rail != "hidden" {
		m.Settings.SidebarPosition = rail
	}
	m.OuterGap = gap
	m.TileAllWindows()
	m.CompleteAllAnimations()
	return m
}

// wantRegion is the pane region worked out from the chrome, independent of
// the getters under test.
func wantRegion(m *OS, dock, rail string, gap int) layout.Rect {
	top, bottom, left, right := 0, 0, 0, 0
	switch dock {
	case "top":
		top = gapDockRows
	case "bottom":
		bottom = gapDockRows
	}
	switch rail {
	case "left":
		left = m.GetSidebarWidth()
	case "right":
		right = m.GetSidebarWidth()
	}
	return layout.Rect{
		X: left + gap, Y: top + gap,
		W: gapCols - left - right - 2*gap,
		H: gapRows - top - bottom - 2*gap,
	}
}

// tiledBox is the smallest rectangle holding every tiled pane.
func tiledBox(m *OS) layout.Rect {
	x0, y0, x1, y1 := 1<<30, 1<<30, -1, -1
	for _, w := range m.Windows {
		if w.Workspace != m.CurrentWorkspace || w.Minimized || w.IsFloating {
			continue
		}
		x0, y0 = min(x0, w.X), min(y0, w.Y)
		x1, y1 = max(x1, w.X+w.Width), max(y1, w.Y+w.Height)
	}
	return layout.Rect{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}
}

func TestOuterGapInsetsEveryTiledLayout(t *testing.T) {
	for _, mode := range []string{"bsp", "master-stack", "scrolling"} {
		for _, dock := range []string{"top", "bottom", "hidden"} {
			for _, rail := range []string{"left", "right", "hidden"} {
				for _, gap := range []int{0, 1, 3} {
					name := fmt.Sprintf("%s/dock-%s/rail-%s/gap-%d", mode, dock, rail, gap)
					t.Run(name, func(t *testing.T) {
						m := outerGapOS(t, mode, dock, rail, gap)
						want := wantRegion(m, dock, rail, gap)
						got := layout.Rect{X: m.GetLeftMargin(), Y: m.GetTopMargin(), W: m.GetContentWidth(), H: m.GetUsableHeight()}
						if got != want {
							t.Fatalf("pane region %+v, want %+v", got, want)
						}
						box := tiledBox(m)
						if mode == "scrolling" {
							// The strip runs off the sides by design, so only
							// its rows are the region's.
							box.X, box.W = want.X, want.W
						}
						if box != want {
							t.Fatalf("the panes fill %+v, want the pane region %+v\n%s", box, want, rects(m))
						}
					})
				}
			}
		}
	}
}

// TestOuterGapZeroIsTheLayoutBefore pins the default: with no gap the pane
// region is the chrome region, so nothing moves for anyone who never sets it.
func TestOuterGapZeroIsTheLayoutBefore(t *testing.T) {
	m := outerGapOS(t, "bsp", "top", "right", 0)
	if m.GetTopMargin() != m.chromeTop() || m.GetUsableHeight() != m.chromeHeight() ||
		m.GetLeftMargin() != m.chromeLeft() || m.GetRightMargin() != m.chromeRight() {
		t.Fatalf("gap 0 moved the pane region off the chrome region: top %d/%d height %d/%d left %d/%d right %d/%d",
			m.GetTopMargin(), m.chromeTop(), m.GetUsableHeight(), m.chromeHeight(),
			m.GetLeftMargin(), m.chromeLeft(), m.GetRightMargin(), m.chromeRight())
	}
}

// TestOuterGapLeavesTheRailFullHeight: the rail is chrome, so it spans the
// rows between the dock and the screen edge whatever the gap, and a click on
// its first and last rows still lands on it.
func TestOuterGapLeavesTheRailFullHeight(t *testing.T) {
	for _, dock := range []string{"top", "bottom"} {
		t.Run(dock, func(t *testing.T) {
			m := outerGapOS(t, "bsp", dock, "right", 3)
			top, h := 0, gapRows-gapDockRows
			if dock == "top" {
				top = gapDockRows
			}
			layer := m.renderSidebar()
			if layer == nil {
				t.Fatal("the rail did not draw")
			}
			if y := layer.GetY(); y != top {
				t.Fatalf("the rail starts at row %d, want %d under the dock", y, top)
			}
			railX := gapCols - m.GetSidebarWidth() + 1
			for _, y := range []int{top, top + h - 1} {
				if !m.SidebarBandContains(railX, y) {
					t.Fatalf("row %d is the rail's but a click there misses it", y)
				}
			}
			if m.SidebarBandContains(railX, top+h) {
				t.Fatalf("row %d is past the rail but a click there hits it", top+h)
			}
		})
	}
}

// TestOuterGapSnapZoomAndFloatFollow: the rectangles that are not tiled come
// from the same region.
func TestOuterGapSnapZoomAndFloatFollow(t *testing.T) {
	m := outerGapOS(t, "bsp", "top", "left", 3)
	want := wantRegion(m, "top", "left", 3)

	x, y, w, h := m.calculateSnapBounds(SnapFullScreen)
	if got := (layout.Rect{X: x, Y: y, W: w, H: h}); got != want {
		t.Fatalf("a full snap fills %+v, want %+v", got, want)
	}
	x, y, _, h = m.calculateSnapBounds(SnapLeft)
	if x != want.X || y != want.Y || h != want.H {
		t.Fatalf("a left snap starts at %d,%d and is %d rows, want %d,%d and %d", x, y, h, want.X, want.Y, want.H)
	}
	if q := m.SnapZoneAt(want.X, want.Y+want.H/2); q != SnapLeft {
		t.Fatalf("the pane region's left edge is snap zone %v, want SnapLeft", q)
	}

	x, y, w, h = m.zoomRect()
	if got := (layout.Rect{X: x, Y: y, W: w, H: h}); got != want {
		t.Fatalf("a zoom fills %+v, want %+v", got, want)
	}

	x, y, w, h = m.NewWindowPlacement()
	if x < want.X || y < want.Y || x+w > want.X+want.W || y+h > want.Y+want.H {
		t.Fatalf("a new floating window lands at %d,%d %dx%d, outside %+v", x, y, w, h, want)
	}

	// A floating window shoved above the region is pulled back under the gap.
	m.AutoTiling = false
	fw := m.Windows[0]
	fw.IsFloating = true
	fw.Y = 0
	m.ClampWindowsToView()
	if fw.Y < want.Y {
		t.Fatalf("a floating window was left at row %d, above the pane region at %d", fw.Y, want.Y)
	}
}

// TestOuterGapStopsDividersShortOfTheRules: with shared borders a divider
// meets the dock's rule and the rail's edge. Across a gap there is no rule to
// meet, and a line reaching for it would cross the gap.
func TestOuterGapStopsDividersShortOfTheRules(t *testing.T) {
	m := outerGapOS(t, "bsp", "top", "right", 0)
	m.Settings.BorderStyle = "rounded"
	if !m.Settings.BorderJoinsChromeRules() {
		t.Skip("the border style does not join chrome rules")
	}
	if r := m.chromeRules(m.GetBSPBounds()); r.top < 0 || r.right < 0 {
		t.Fatalf("with no gap the dividers should meet the dock and the rail: %+v", r)
	}
	m.OuterGap = 2
	if r := m.chromeRules(m.GetBSPBounds()); r != (chromeRules{-1, -1, -1, -1}) {
		t.Fatalf("with a gap no divider should reach a rule: %+v", r)
	}
}

// TestOuterGapGivesWayOnASmallScreen: a gap the screen cannot carry is cut
// down so the panes keep a usable size.
func TestOuterGapGivesWayOnASmallScreen(t *testing.T) {
	m := modeOS(t, "bsp", false, 0, 1, 40, 12)
	m.Settings.DockbarPosition = "top"
	m.Settings.SidebarEnabled = false
	m.OuterGap = config.OuterGapMax
	if w := m.GetContentWidth(); w < config.DefaultWindowWidth {
		t.Fatalf("the panes got %d columns, fewer than %d", w, config.DefaultWindowWidth)
	}
	if h := m.GetUsableHeight(); h < config.DefaultWindowHeight {
		t.Fatalf("the panes got %d rows, fewer than %d", h, config.DefaultWindowHeight)
	}
	if m.GetTopMargin() <= gapDockRows {
		t.Fatalf("some gap fits under the dock, but none was kept")
	}
}

func TestSetOuterGapSettingClampsAndRetiles(t *testing.T) {
	m := outerGapOS(t, "bsp", "bottom", "hidden", 0)
	before := tiledBox(m)
	m.SetOuterGapSetting(99)
	if m.OuterGap != config.OuterGapMax {
		t.Fatalf("outer gap %d, want it clamped to %d", m.OuterGap, config.OuterGapMax)
	}
	m.CompleteAllAnimations()
	if after := tiledBox(m); after == before {
		t.Fatalf("setting the gap did not retile: still %+v", after)
	}
	m.SetOuterGapSetting(-4)
	if m.OuterGap != 0 {
		t.Fatalf("outer gap %d, want it clamped to 0", m.OuterGap)
	}
}

// TestOuterGapIsSessionState: the gap moves rectangles, so it travels in the
// session's pane geometry and a peer adopts it.
func TestOuterGapIsSessionState(t *testing.T) {
	m := outerGapOS(t, "bsp", "top", "hidden", 0)
	if !m.adoptPaneGeometry(&session.SessionState{PaneGeometry: &session.PaneGeometryState{OuterGap: 2}}) {
		t.Fatal("adopting a new outer gap reported no change")
	}
	if m.OuterGap != 2 {
		t.Fatalf("outer gap %d after adopting 2", m.OuterGap)
	}
	a := session.SessionState{PaneGeometry: &session.PaneGeometryState{OuterGap: 1}}
	b := session.SessionState{PaneGeometry: &session.PaneGeometryState{OuterGap: 2}}
	if session.StateFingerprint(&a) == session.StateFingerprint(&b) {
		t.Fatal("two states that differ only in the outer gap have one fingerprint, so the change is never pushed")
	}
}

// TestOuterGapSettlesAcrossClientsOfDifferentSizes: two clients of one
// session, on terminals of different sizes, whose configs disagree about the
// gap. The session's gap is the one that was there first, both clients lay the
// panes out with it, and the shells get one size.
func TestOuterGapSettlesAcrossClientsOfDifferentSizes(t *testing.T) {
	r, p, _ := geometryRigSized(t, clientGlobals{outer: 3}, clientGlobals{outer: 0}, 100, 30)
	if r.m.OuterGap != 3 || p.m.OuterGap != 3 {
		t.Fatalf("outer gap local %d peer %d, want the session's 3 on both", r.m.OuterGap, p.m.OuterGap)
	}
	for _, c := range []struct {
		name string
		m    *OS
	}{{"local", r.m}, {"peer", p.m}} {
		if got, want := c.m.GetLeftMargin(), c.m.chromeLeft()+3; got != want {
			t.Fatalf("%s: left margin %d, want %d", c.name, got, want)
		}
		if got, want := c.m.GetTopMargin(), c.m.chromeTop()+3; got != want {
			t.Fatalf("%s: top margin %d, want %d", c.name, got, want)
		}
	}
	if local, peer := rects(r.m), rects(p.m); local != peer {
		t.Fatalf("the clients place the panes differently:\n local %s\n peer  %s", local, peer)
	}
	if local, peer := contentSizes(r.m), contentSizes(p.m); local != peer {
		t.Fatalf("the clients disagree on pane sizes:\n local %s\n peer  %s", local, peer)
	}
}

// TestOuterGapIsOnTheSettingsPage: the row is there, reads the session's
// value, and its stepper moves it.
func TestOuterGapIsOnTheSettingsPage(t *testing.T) {
	m := searchOS(t)
	items, _ := searchFor(m, "outer gap")
	var row *settingItem
	for i := range items {
		if items[i].Label == "Outer gap" {
			row = &items[i]
		}
	}
	if row == nil {
		t.Fatalf("no Outer gap row on the settings page")
	}
	m.OuterGap = 2
	if v := row.value(m); v != "2" {
		t.Fatalf("the row reads %q, want the session's 2", v)
	}
	row.adjust(m, 1)
	if m.OuterGap != 3 {
		t.Fatalf("stepping the row up left the gap at %d, want 3", m.OuterGap)
	}
}
