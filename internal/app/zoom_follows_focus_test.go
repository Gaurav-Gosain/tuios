package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// zoomFocusOS is two panes on one workspace, tiled or floating, with the
// animations off so a snap never owns a rectangle mid-test.
func zoomFocusOS(t *testing.T, tiling bool) (*OS, *terminal.Window, *terminal.Window) {
	t.Helper()
	prevAnim := config.Global.AnimationsEnabled
	config.Global.AnimationsEnabled = false
	t.Cleanup(func() { config.Global.AnimationsEnabled = prevAnim })

	a := newTestWindow(t, "zoom-follow-a", 40, 20)
	b := newTestWindow(t, "zoom-follow-b", 30, 15)
	a.Workspace, b.Workspace = 1, 1
	a.X, a.Y, a.Width, a.Height = 10, 5, 40, 20
	b.X, b.Y, b.Width, b.Height = 60, 8, 30, 15
	a.Tiled, b.Tiled = false, false

	m := &OS{
		Settings:         config.Global,
		Windows:          []*terminal.Window{a, b},
		FocusedWindow:    0,
		WorkspaceFocus:   map[int]int{},
		NumWorkspaces:    9,
		CurrentWorkspace: 1,
		Width:            200,
		Height:           60,
		EffectiveWidth:   200,
		EffectiveHeight:  60,
		PendingResizes:   map[string][2]int{},
	}
	if tiling {
		m.AutoTiling = true
		m.UseBSPLayout = true
		m.TileAllWindows()
	}
	return m, a, b
}

// box4 is a pane's rectangle as one comparable value.
func box4(w *terminal.Window) [4]int {
	return [4]int{w.X, w.Y, w.Width, w.Height}
}

// TestZoomFollowsFocusUnderTiling is the regression for alt+n from a zoomed
// pane. The focus moved to the next pane in the cycle, the zoomed pane kept
// the box and hid it, and the cursor was drawn where the focused pane would
// have been. The zoom now goes with the focus: the pane the focus lands on
// takes the box, and the pane that had it goes back to its tile.
func TestZoomFollowsFocusUnderTiling(t *testing.T) {
	m, a, b := zoomFocusOS(t, true)
	tileA, tileB := box4(a), box4(b)

	m.ToggleZoom()
	if !a.Zoomed {
		t.Fatal("setup: the focused pane did not zoom")
	}
	box := box4(a)
	if box == tileA {
		t.Fatal("setup: the zoom box is the tile")
	}

	m.CycleToNextVisibleWindow()

	if m.GetFocusedWindow() != b {
		t.Fatalf("focus went to %q, want b", m.GetFocusedWindow().ID)
	}
	if !b.Zoomed || a.Zoomed {
		t.Fatalf("zoom did not follow the focus: a.Zoomed=%t b.Zoomed=%t", a.Zoomed, b.Zoomed)
	}
	if got := box4(b); got != box {
		t.Errorf("the newly focused pane holds %v, want the zoom box %v", got, box)
	}
	if got := box4(b); got[0] != box[0] || got[1] != box[1] {
		t.Errorf("the newly focused pane sits at %v, want the box's corner", got)
	}
	if got := box4(a); got == box {
		t.Errorf("the pane that lost the zoom still holds the box %v", got)
	}
	if got := box4(a); got != tileA {
		t.Errorf("the pane that lost the zoom sits at %v, want its tile %v", got, tileA)
	}
	if got := [4]int{b.PreZoomX, b.PreZoomY, b.PreZoomWidth, b.PreZoomHeight}; got != tileB {
		t.Errorf("the pre-zoom record of the newly zoomed pane is %v, want its tile %v", got, tileB)
	}
	if zw := m.zoomedWindow(); zw != b {
		t.Errorf("the workspace's zoomed pane is not the focused one")
	}

	// And back: the cycle wraps to a, which takes the box again, and b unzooms
	// to its tile.
	m.CycleToNextVisibleWindow()
	if m.GetFocusedWindow() != a || !a.Zoomed || b.Zoomed {
		t.Fatalf("after cycling back: focused=%q a.Zoomed=%t b.Zoomed=%t",
			m.GetFocusedWindow().ID, a.Zoomed, b.Zoomed)
	}
	if got := box4(a); got != box {
		t.Errorf("a holds %v after taking the zoom back, want %v", got, box)
	}
	if got := box4(b); got != tileB {
		t.Errorf("b holds %v after losing the zoom, want its tile %v", got, tileB)
	}

	// Unzooming leaves the layout as it was before any of this.
	m.ToggleZoom()
	if a.Zoomed || b.Zoomed {
		t.Fatal("unzoom left a pane zoomed")
	}
	if got := box4(a); got != tileA {
		t.Errorf("a is at %v after unzoom, want its tile %v", got, tileA)
	}
	if got := box4(b); got != tileB {
		t.Errorf("b is at %v after unzoom, want its tile %v", got, tileB)
	}
}

// TestZoomFollowsFocusFloating is the same move outside a tiling layout, where
// nothing retiles: the pane that loses the zoom is put back exactly where it
// was zoomed from.
func TestZoomFollowsFocusFloating(t *testing.T) {
	m, a, b := zoomFocusOS(t, false)
	rectA, rectB := box4(a), box4(b)

	m.ToggleZoom()
	box := box4(a)

	m.FocusWindow(1)

	if !b.Zoomed || a.Zoomed {
		t.Fatalf("zoom did not follow the focus: a.Zoomed=%t b.Zoomed=%t", a.Zoomed, b.Zoomed)
	}
	if got := box4(b); got != box {
		t.Errorf("b holds %v, want the zoom box %v", got, box)
	}
	if got := box4(a); got != rectA {
		t.Errorf("a holds %v after losing the zoom, want where it was zoomed from %v", got, rectA)
	}
	if got := [4]int{b.PreZoomX, b.PreZoomY, b.PreZoomWidth, b.PreZoomHeight}; got != rectB {
		t.Errorf("b's pre-zoom record is %v, want %v", got, rectB)
	}

	m.ToggleZoom()
	if got := box4(b); got != rectB {
		t.Errorf("b is at %v after unzoom, want %v", got, rectB)
	}
}

// TestZoomStaysUnderAFocusedPopup: a popup is drawn over the zoom and focused
// in front of it, so focusing one is not a request to see it fullscreen.
func TestZoomStaysUnderAFocusedPopup(t *testing.T) {
	m, a, b := zoomFocusOS(t, true)
	b.IsPopup = true
	m.TileAllWindows()

	m.ToggleZoom()
	box := box4(a)

	m.FocusWindow(1)

	if m.GetFocusedWindow() != b {
		t.Fatal("the popup was not focused")
	}
	if !a.Zoomed || b.Zoomed {
		t.Errorf("focusing a popup moved the zoom: a.Zoomed=%t b.Zoomed=%t", a.Zoomed, b.Zoomed)
	}
	if got := box4(a); got != box {
		t.Errorf("the zoomed pane holds %v, want the box %v", got, box)
	}
}

// TestZoomIgnoresAFocusAppliedBySync: a peer's focus arrives by assignment,
// not through FocusWindow, so a client whose focus sits beside a pane somebody
// else zoomed keeps drawing that zoom, as the render intends.
func TestZoomIgnoresAFocusAppliedBySync(t *testing.T) {
	m, a, b := zoomFocusOS(t, true)
	m.ToggleZoom()

	m.FocusedWindow = 1

	if !a.Zoomed || b.Zoomed {
		t.Errorf("a focus written by sync moved the zoom: a.Zoomed=%t b.Zoomed=%t", a.Zoomed, b.Zoomed)
	}
	if m.zoomedWindow() != a {
		t.Error("the workspace's zoomed pane changed under a synced focus")
	}
}
