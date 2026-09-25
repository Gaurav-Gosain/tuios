package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestKittyPlacementDeletedWhenWindowGone verifies the info==nil path still
// tears down tracking for windows genuinely removed from the model (closed),
// so tracking does not leak.
func TestKittyPlacementDeletedWhenWindowGone(t *testing.T) {
	kp := newTestKittyPassthrough(t)
	winID := "test-window-id-abcdef12"

	kp.placements[winID] = map[uint32]*PassthroughPlacement{
		1: {HostImageID: 1, WindowID: winID, Cols: 5, Rows: 3, Hidden: false},
	}

	// No windows in the model -> the window is genuinely gone.
	m := &OS{Settings: config.Global, Width: 80, Height: 24, CurrentWorkspace: 1, KittyPassthrough: kp}
	m.GetKittyGraphicsCmd()

	if _, ok := kp.placements[winID]; ok {
		t.Error("expected placement tracking removed for a closed (absent) window")
	}
}
