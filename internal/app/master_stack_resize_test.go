package app

import (
	"fmt"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
)

// A master-stack resize goes through the geometry scan, which moves the pane
// rectangles and nothing else, while the master-stack tiler recomputes every
// rectangle from its ratios on the next retile. These cases resize, retile,
// and check the resize is still there. Before the ratios were written back
// from the geometry, every one of them came back at the split it started at.

// paneRectsByID is the geometry of every pane, by window ID.
func paneRectsByID(m *OS) map[string]layout.Rect {
	out := make(map[string]layout.Rect, len(m.Windows))
	for _, w := range m.Windows {
		out[w.ID] = layout.Rect{X: w.X, Y: w.Y, W: w.Width, H: w.Height}
	}
	return out
}

// retile lays the workspace out again from the model's ratios, the way any
// later layout pass does.
func retile(m *OS) {
	m.TileAllWindows()
	m.CompleteAllAnimations()
}

// requireSurvives checks that the retiled geometry is the resized geometry: the
// resized pane exactly in the dimension that was resized, and every edge of
// every pane within a cell. The cell is the separator gap, which the geometry
// scan closes and the retile opens again.
func requireSurvives(t *testing.T, m *OS, resized map[string]layout.Rect, id string, height bool) {
	t.Helper()
	after := paneRectsByID(m)
	want, got := resized[id], after[id]
	if height && got.H != want.H {
		t.Errorf("the resized pane is %d rows after a retile, want the %d the resize left it at", got.H, want.H)
	}
	if !height && got.W != want.W {
		t.Errorf("the resized pane is %d columns after a retile, want the %d the resize left it at", got.W, want.W)
	}
	for wid, w := range resized {
		g := after[wid]
		if abs(g.X-w.X) > 1 || abs(g.Y-w.Y) > 1 || abs(g.W-w.W) > 1 || abs(g.H-w.H) > 1 {
			t.Errorf("pane %s moved from %+v to %+v on the retile", wid, w, g)
		}
	}
}

// TestMasterStackWidthPercentSurvivesRetile covers resize_width_N on the master
// pane with two and three panes, with and without shared borders, and at 85%,
// which the tiler used to clamp to 70%.
//
// NEGATIVE CONTROL: drop the SyncMasterStackFromGeometry call from
// AdjustTilingNeighbors and the master pane goes back to half the width.
func TestMasterStackWidthPercentSurvivesRetile(t *testing.T) {
	for _, n := range []int{2, 3} {
		for _, shared := range []bool{false, true} {
			for _, pct := range []int{25, 85} {
				t.Run(fmt.Sprintf("n=%d/shared=%v/%d%%", n, shared, pct), func(t *testing.T) {
					m := modeOS(t, LayoutModeMasterStack, shared, 0, n, 160, 40)
					m.FocusedWindow = 0
					before := m.Windows[0].Width

					m.SetFocusedWindowWidthPercent(pct)
					if m.Windows[0].Width == before {
						t.Fatalf("setup: the resize left the master pane at %d columns", before)
					}
					resized := paneRectsByID(m)

					retile(m)
					requireSurvives(t, m, resized, m.Windows[0].ID, false)
					if got := m.WorkspaceMasterRatio[m.CurrentWorkspace]; got != m.MasterRatio {
						t.Errorf("the workspace holds master ratio %v, want the %v in force", got, m.MasterRatio)
					}
				})
			}
		}
	}
}

// TestMasterStackStackedPairHeightSurvivesRetile is the two pane layout on a
// tall screen, where the panes are stacked and the master ratio is the top
// pane's share of the height.
//
// NEGATIVE CONTROL: as above.
func TestMasterStackStackedPairHeightSurvivesRetile(t *testing.T) {
	m := modeOS(t, LayoutModeMasterStack, true, 0, 2, 60, 50)
	if layout.MasterStackSideBySide(m.GetContentWidth(), m.GetUsableHeight()) {
		t.Fatalf("setup: a %dx%d region is laid out side by side", m.GetContentWidth(), m.GetUsableHeight())
	}
	m.FocusedWindow = 0
	before := m.Windows[0].Height

	m.SetFocusedWindowHeightPercent(30)
	if m.Windows[0].Height == before {
		t.Fatalf("setup: the resize left the top pane at %d rows", before)
	}
	resized := paneRectsByID(m)

	retile(m)
	requireSurvives(t, m, resized, m.Windows[0].ID, true)
}

// TestMasterStackStackHeightSurvivesRetile resizes the upper of the two
// stacked panes in the three pane layout. The stacked panes used to split the
// height equally on every retile whatever the user had done.
//
// NEGATIVE CONTROL: make CalculateMasterStackLayout ignore stackRatio, or drop
// the setWorkspaceStackRatio call, and the stacked panes go back to equal
// halves.
func TestMasterStackStackHeightSurvivesRetile(t *testing.T) {
	for _, shared := range []bool{false, true} {
		t.Run(fmt.Sprintf("shared=%v", shared), func(t *testing.T) {
			m := modeOS(t, LayoutModeMasterStack, shared, 0, 3, 160, 40)
			m.FocusedWindow = 1
			before := m.Windows[1].Height

			m.SetFocusedWindowHeightPercent(25)
			if m.Windows[1].Height == before {
				t.Fatalf("setup: the resize left the upper stacked pane at %d rows", before)
			}
			resized := paneRectsByID(m)

			retile(m)
			requireSurvives(t, m, resized, m.Windows[1].ID, true)
			if _, ok := m.WorkspaceStackRatio[m.CurrentWorkspace]; !ok {
				t.Error("the workspace holds no stack ratio after a stack resize")
			}
		})
	}
}

// TestMasterStackGrowKeySurvivesRetile is the resize_master_grow action (the
// ">" key), which widens the focused pane by four columns a press.
//
// NEGATIVE CONTROL: as for the percentage case.
func TestMasterStackGrowKeySurvivesRetile(t *testing.T) {
	m := modeOS(t, LayoutModeMasterStack, true, 0, 3, 160, 40)
	m.FocusedWindow = 0
	before := m.Windows[0].Width

	for range 3 {
		m.ResizeFocusedWindowWidth(4)
	}
	if m.Windows[0].Width != before+12 {
		t.Fatalf("setup: three presses took the master pane from %d to %d columns, want %d",
			before, m.Windows[0].Width, before+12)
	}
	resized := paneRectsByID(m)

	retile(m)
	requireSurvives(t, m, resized, m.Windows[0].ID, false)
}

// TestMasterStackGridResizeIsNotRecorded pins the scope: a grid of four or
// more panes is equal-share and has no ratio to write, so a resize there does
// not move the master ratio or add a stack ratio.
func TestMasterStackGridResizeIsNotRecorded(t *testing.T) {
	m := modeOS(t, LayoutModeMasterStack, true, 0, 4, 160, 40)
	m.FocusedWindow = 0
	ratio := m.MasterRatio

	m.ResizeFocusedWindowWidth(8)

	if m.MasterRatio != ratio {
		t.Errorf("a grid resize moved the master ratio from %v to %v", ratio, m.MasterRatio)
	}
	if len(m.WorkspaceStackRatio) != 0 {
		t.Errorf("a grid resize recorded stack ratios %v", m.WorkspaceStackRatio)
	}
}

// TestTheStackRatioRoundTripsThroughSessionState: the ratios a resize records
// are what BuildSessionState sends and what RestoreFromState adopts, and a
// client that adopts them lays the workspace out the same way.
//
// NEGATIVE CONTROL: drop the WorkspaceStackRatio block from BuildSessionState,
// or the adopt call from RestoreFromState, and the fresh client holds no stack
// ratio.
func TestTheStackRatioRoundTripsThroughSessionState(t *testing.T) {
	m := modeOS(t, LayoutModeMasterStack, true, 0, 3, 160, 40)
	m.FocusedWindow = 0
	m.SetFocusedWindowWidthPercent(35)
	m.FocusedWindow = 1
	m.SetFocusedWindowHeightPercent(30)
	stack, ok := m.WorkspaceStackRatio[m.CurrentWorkspace]
	if !ok {
		t.Fatal("setup: the stack resize recorded no stack ratio")
	}

	state := m.BuildSessionState()
	if got := state.WorkspaceStackRatio[m.CurrentWorkspace]; got != stack {
		t.Fatalf("the state carries stack ratio %v, want %v", got, stack)
	}
	if got := state.WorkspaceMasterRatio[m.CurrentWorkspace]; got != m.MasterRatio {
		t.Fatalf("the state carries master ratio %v, want %v", got, m.MasterRatio)
	}

	fresh := ratioClient(t)
	if err := fresh.RestoreFromState(state); err != nil {
		t.Fatalf("RestoreFromState: %v", err)
	}
	if got := fresh.WorkspaceStackRatio[state.CurrentWorkspace]; got != stack {
		t.Errorf("the restored client holds stack ratio %v, want %v", got, stack)
	}
	if fresh.MasterRatio != m.MasterRatio {
		t.Errorf("the restored client holds master ratio %v, want %v", fresh.MasterRatio, m.MasterRatio)
	}
}

// TestAPeerAdoptsAStackResize is the same on the live route: a second client on
// the session takes the ratios a resize on the first one recorded, and a retile
// on it draws the panes where the first client's user put them.
//
// NEGATIVE CONTROL: drop the adoptWorkspaceStackRatio call from ApplyStateSync,
// or the field from BuildSessionState, and the peer holds no stack ratio and
// retiles the stacked panes to equal halves.
func TestAPeerAdoptsAStackResize(t *testing.T) {
	prev := config.Global.AnimationsEnabled
	config.Global.AnimationsEnabled = false
	t.Cleanup(func() { config.Global.AnimationsEnabled = prev })

	r := newRig(t, 3)
	r.m.AutoTiling = true
	r.m.ApplyLayoutModeName(config.LayoutModeMasterStack)
	r.m.TileAllWindows()
	r.m.SyncStateToDaemon()

	p := joinPeerOS(t, r, r.cols, r.rows)
	p.m.AutoTiling = true
	p.m.ApplyLayoutModeName(config.LayoutModeMasterStack)
	p.m.TileAllWindows()

	ex := &exchange{t: t}
	ex.route(r.client, r.m, "local")
	ex.route(p.c, p.m, "peer")
	ex.settle(200, 50*time.Millisecond)

	panes := r.m.tilablePanes(r.m.CurrentWorkspace)
	if len(panes) != 3 {
		t.Fatalf("setup: the workspace holds %d panes, want 3", len(panes))
	}
	for i, w := range r.m.Windows {
		if w == panes[1] {
			r.m.FocusedWindow = i
		}
	}
	before := panes[1].Height
	r.m.SetFocusedWindowHeightPercent(25)
	if panes[1].Height == before {
		t.Fatalf("setup: the resize left the upper stacked pane at %d rows", before)
	}
	want := r.m.WorkspaceStackRatio[r.m.CurrentWorkspace]
	r.m.SyncStateToDaemon()
	ex.settle(200, 100*time.Millisecond)

	if got, ok := p.m.WorkspaceStackRatio[p.m.CurrentWorkspace]; !ok || got != want {
		t.Fatalf("the peer holds stack ratio %v (present %v), want the %v the other client recorded", got, ok, want)
	}

	retile(r.m)
	retile(p.m)
	local, remote := paneRectsByID(r.m), paneRectsByID(p.m)
	for id, l := range local {
		if rm, ok := remote[id]; ok && rm != l {
			t.Errorf("pane %s is %+v on the peer and %+v on the client that resized it", id, rm, l)
		}
	}
	if got := local[panes[1].ID].H; abs(got-panes[1].Height) > 1 {
		t.Errorf("the upper stacked pane is %d rows after the retile, want about %d", got, panes[1].Height)
	}
}
