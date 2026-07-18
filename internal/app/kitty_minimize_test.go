package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// TestKittyPlacementSurvivesMinimize drives the real render-path callback
// (GetKittyGraphicsCmd) and verifies that minimizing a window showing a kitty
// image HIDES its placement but keeps tracking it, so the image reappears on
// restore without the guest retransmitting. Previously minimized/off-workspace
// windows were omitted from the callback, RefreshAllPlacements saw info==nil,
// and it permanently destroyed the placement.
func TestKittyPlacementSurvivesMinimize(t *testing.T) {
	kp := newTestKittyPassthrough(t)
	kp.screenWidth = 80
	kp.screenHeight = 24

	win := &terminal.Window{
		ID:        "test-window-id-abcdef12",
		X:         0,
		Y:         0,
		Width:     80,
		Height:    24,
		Workspace: 1,
		Tiled:     true, // BorderOffset 0
	}

	const hostID uint32 = 1
	kp.placements[win.ID] = map[uint32]*PassthroughPlacement{
		hostID: {
			GuestImageID: 1,
			HostImageID:  hostID,
			WindowID:     win.ID,
			GuestX:       0,
			AbsoluteLine: 0,
			Cols:         5,
			Rows:         3,
			DisplayRows:  3,
			Hidden:       true, // freshly transmitted; refresh places it
		},
	}

	m := &OS{
		Width:            80,
		Height:           24,
		CurrentWorkspace: 1,
		KittyPassthrough: kp,
		Windows:          []*terminal.Window{win},
	}

	// Visible: placement becomes active.
	m.GetKittyGraphicsCmd()
	if p := kp.placements[win.ID][hostID]; p == nil || p.Hidden {
		t.Fatalf("after visible refresh: expected placed (not hidden), got %+v", p)
	}

	// Minimize the window, then refresh: hide but KEEP tracking.
	win.Minimized = true
	m.GetKittyGraphicsCmd()
	p := kp.placements[win.ID][hostID]
	if p == nil {
		t.Fatal("BUG: minimizing destroyed the placement; image cannot reappear on restore")
	}
	if !p.Hidden {
		t.Fatal("minimized placement should be hidden")
	}

	// Restore: re-placed from surviving tracking.
	win.Minimized = false
	m.GetKittyGraphicsCmd()
	if p := kp.placements[win.ID][hostID]; p == nil || p.Hidden {
		t.Fatalf("after restore refresh: expected re-placed (not hidden), got %+v", p)
	}
}

// TestKittyPlacementSurvivesWorkspaceSwitch is the workspace-switch analogue:
// a window on a non-current workspace must keep its placement tracking.
func TestKittyPlacementSurvivesWorkspaceSwitch(t *testing.T) {
	kp := newTestKittyPassthrough(t)
	kp.screenWidth = 80
	kp.screenHeight = 24

	win := &terminal.Window{
		ID: "test-window-id-abcdef12", X: 0, Y: 0, Width: 80, Height: 24,
		Workspace: 1, Tiled: true,
	}
	const hostID uint32 = 1
	kp.placements[win.ID] = map[uint32]*PassthroughPlacement{
		hostID: {HostImageID: hostID, WindowID: win.ID, Cols: 5, Rows: 3, DisplayRows: 3, Hidden: true},
	}
	m := &OS{Width: 80, Height: 24, CurrentWorkspace: 1, KittyPassthrough: kp, Windows: []*terminal.Window{win}}

	m.GetKittyGraphicsCmd()
	if kp.placements[win.ID][hostID].Hidden {
		t.Fatal("expected placed while on current workspace")
	}

	// Switch to another workspace: window is now off-current; keep tracking.
	m.CurrentWorkspace = 2
	m.GetKittyGraphicsCmd()
	if kp.placements[win.ID] == nil || kp.placements[win.ID][hostID] == nil {
		t.Fatal("BUG: switching workspace destroyed the placement")
	}
	if !kp.placements[win.ID][hostID].Hidden {
		t.Fatal("off-workspace placement should be hidden")
	}
}

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
	m := &OS{Width: 80, Height: 24, CurrentWorkspace: 1, KittyPassthrough: kp}
	m.GetKittyGraphicsCmd()

	if _, ok := kp.placements[winID]; ok {
		t.Error("expected placement tracking removed for a closed (absent) window")
	}
}
