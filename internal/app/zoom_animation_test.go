package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// Zoom was a cut: the pane was at its tile in one frame and filling the region
// in the next, with nothing to say which pane had grown.

// TestZoomSlidesBothWays pins that a zoom and an unzoom each arm a slide, from
// where the pane is to where it is going.
func TestZoomSlidesBothWays(t *testing.T) {
	m, wins := zoomPeekOS(t)
	m.Settings.ZoomAnimation = true
	m.FocusedWindow = 1
	target := wins[1]
	tile := [4]int{target.X, target.Y, target.Width, target.Height}

	m.ToggleZoom()
	if len(m.Animations) != 1 {
		t.Fatalf("zooming armed %d animations, want one", len(m.Animations))
	}
	in := m.Animations[0]
	if in.Window != target {
		t.Error("the slide is on some other pane")
	}
	if got := [4]int{in.StartX, in.StartY, in.StartWidth, in.StartHeight}; got != tile {
		t.Errorf("the slide starts at %v, want the pane's tile %v", got, tile)
	}
	if in.EndWidth <= in.StartWidth && in.EndHeight <= in.StartHeight {
		t.Errorf("the pane does not grow: %dx%d to %dx%d",
			in.StartWidth, in.StartHeight, in.EndWidth, in.EndHeight)
	}
	m.CompleteAllAnimations()

	m.ToggleZoom()
	if len(m.Animations) != 1 {
		t.Fatalf("unzooming armed %d animations, want one", len(m.Animations))
	}
	out := m.Animations[0]
	if got := [4]int{out.EndX, out.EndY, out.EndWidth, out.EndHeight}; got != tile {
		t.Errorf("the slide back ends at %v, want the pane's tile %v", got, tile)
	}
	m.CompleteAllAnimations()
	if got := [4]int{target.X, target.Y, target.Width, target.Height}; got != tile {
		t.Errorf("the pane landed at %v, want its tile %v", got, tile)
	}
	if target.Zoomed {
		t.Error("the pane is still marked zoomed after sliding home")
	}
}

// TestZoomAnimationCanBeTurnedOff pins the setting: with it off the pane is put
// in the box in one frame, which is what zoom always did.
func TestZoomAnimationCanBeTurnedOff(t *testing.T) {
	m, wins := zoomPeekOS(t)
	m.Settings.ZoomAnimation = false
	m.FocusedWindow = 1

	m.ToggleZoom()
	if len(m.Animations) != 0 {
		t.Errorf("zooming armed %d animations with the slide off", len(m.Animations))
	}
	if !wins[1].Zoomed {
		t.Fatal("the pane did not zoom")
	}
	if wins[1].Width != m.GetContentWidth() {
		t.Errorf("the pane is %d wide, want the region's %d: it was not put in the box",
			wins[1].Width, m.GetContentWidth())
	}

	m.ToggleZoom()
	if len(m.Animations) != 0 {
		t.Errorf("unzooming armed %d animations with the slide off", len(m.Animations))
	}
}

// TestASlidingZoomDoesNotHideTheLayout pins that the panes underneath are drawn
// while the zoom is in flight, or the pane grows over a blank screen.
func TestASlidingZoomDoesNotHideTheLayout(t *testing.T) {
	m, wins := zoomPeekOS(t)
	m.Settings.ZoomAnimation = true
	m.Settings.ZoomSize = config.ZoomSizeDefault
	m.FocusedWindow = 2

	m.ToggleZoom()
	if m.zoomCoversRegion(wins[2]) {
		t.Error("a pane still sliding into the zoom box reports that it covers the region")
	}
	m.CompleteAllAnimations()
	if !m.zoomCoversRegion(wins[2]) {
		t.Error("a landed full zoom does not report that it covers the region")
	}
}

// TestALandedSlideResizesTheGuest pins the promise a slide has to keep. Landing
// early used to stamp the rectangle on the pane and skip the resize, so the app
// inside stayed reflowed for the size it had when the slide began.
func TestALandedSlideResizesTheGuest(t *testing.T) {
	m, wins := zoomPeekOS(t)
	m.Settings.ZoomAnimation = true
	m.FocusedWindow = 0
	target := wins[0]

	m.ToggleZoom()
	m.CompleteAllAnimations()

	if target.Terminal == nil {
		t.Skip("no emulator on the test window")
	}
	if got := target.Terminal.Width(); got != target.ContentWidth() {
		t.Errorf("the guest is %d columns after the slide landed, want the pane's %d",
			got, target.ContentWidth())
	}
	if got := target.Terminal.Height(); got != target.ContentHeight() {
		t.Errorf("the guest is %d rows after the slide landed, want the pane's %d",
			got, target.ContentHeight())
	}
}
