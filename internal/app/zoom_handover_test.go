package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// A zoomed workspace shows one pane, and focus used to move underneath it: the
// next-pane key focused the pane after it, the zoomed pane kept the box, and
// keys went to a pane nobody could see.

// TestFocusCarriesTheZoomWithIt pins the handover.
func TestFocusCarriesTheZoomWithIt(t *testing.T) {
	m, wins := zoomPeekOS(t)
	m.Settings.ZoomAnimation = false
	m.AutoTiling = false
	m.FocusedWindow = 0

	m.ToggleZoom()
	if !wins[0].Zoomed {
		t.Fatal("the pane did not zoom")
	}

	m.FocusWindow(1)

	if wins[0].Zoomed {
		t.Error("the pane that lost the focus kept the zoom")
	}
	if !wins[1].Zoomed {
		t.Error("the pane the focus reached did not take the zoom")
	}
	if zw := m.zoomedWindow(); zw != wins[1] {
		t.Error("the workspace's zoomed pane is not the focused one")
	}
}

// TestTheHandoverSlidesBothPanes pins the animation the report asked for: the
// pane that had the box goes home and the pane that takes it comes up, both at
// once, so the frame says what happened rather than cutting between two
// arrangements.
func TestTheHandoverSlidesBothPanes(t *testing.T) {
	m, wins := zoomPeekOS(t)
	m.Settings.ZoomAnimation = true
	m.AutoTiling = false
	m.FocusedWindow = 0

	m.ToggleZoom()
	m.CompleteAllAnimations()

	m.FocusWindow(1)

	moving := map[*terminal.Window]bool{}
	for _, a := range m.Animations {
		moving[a.Window] = true
	}
	if len(m.Animations) != 2 {
		t.Fatalf("the handover armed %d animations, want two: one going home and one coming up", len(m.Animations))
	}
	if !moving[wins[0]] {
		t.Error("the pane that lost the zoom did not slide home")
	}
	if !moving[wins[1]] {
		t.Error("the pane that took the zoom did not slide up")
	}
}

// TestTheHandoverCanBeTurnedOff pins the setting, for anyone who would rather
// the zoom stayed on the pane they put it on.
func TestTheHandoverCanBeTurnedOff(t *testing.T) {
	m, wins := zoomPeekOS(t)
	m.Settings.ZoomAnimation = false
	m.Settings.ZoomFollowsFocus = false
	m.AutoTiling = false
	m.FocusedWindow = 0

	m.ToggleZoom()
	m.FocusWindow(1)

	if !wins[0].Zoomed {
		t.Error("the zoom moved with the setting off")
	}
	if wins[1].Zoomed {
		t.Error("the focused pane took the zoom with the setting off")
	}
}
