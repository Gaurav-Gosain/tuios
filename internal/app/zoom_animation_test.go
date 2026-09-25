package app

import (
	"testing"
)

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
