package app

import (
	"fmt"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// TestSixDaemonWindowsTileWithoutOverlap drives the exact scenario the owner reported:
// a tiling daemon session in which six windows are created one at a time through
// the daemon path, each pushed to the attached client as an Unplaced window
// alongside the daemon's copy of the tiling tree. It asserts the resulting
// geometries partition the screen with no overlap and no large gap.
func TestSixDaemonWindowsTileWithoutOverlap(t *testing.T) {
	prevAnim := config.AnimationsEnabled
	config.AnimationsEnabled = false
	defer func() { config.AnimationsEnabled = prevAnim }()

	const width, height = 120, 40

	// The attached client: tiling on, BSP layout, a known viewport.
	m := &OS{
		NumWorkspaces:    9,
		CurrentWorkspace: 1,
		WorkspaceFocus:   make(map[int]int),
		Width:            width,
		Height:           height,
		AutoTiling:       true,
		UseBSPLayout:     true,
	}

	// daemonState is the canonical state the daemon holds. It starts as a client
	// would have last synced it: tiling on, no windows yet.
	daemonState := &session.SessionState{
		Name:             "tiling",
		CurrentWorkspace: 1,
		AutoTiling:       true,
		WorkspaceFocus:   map[int]string{},
		Version:          1,
	}

	for i := 0; i < 6; i++ {
		// Daemon creates a window (AddDaemonWindow): a full-size Unplaced box is
		// appended, focus moves to it, and the daemon does NOT touch its tiling
		// tree (it has no viewport). Everything else in daemonState is whatever the
		// client last synced back.
		id := fmt.Sprintf("win-%036d", i+1)
		daemonState.Windows = append(daemonState.Windows, session.WindowState{
			ID:        id,
			PTYID:     fmt.Sprintf("pty-%d", i+1),
			Title:     id,
			X:         0,
			Y:         0,
			Width:     width,
			Height:    height,
			Workspace: 1,
			Unplaced:  true,
		})
		daemonState.FocusedWindowID = id
		daemonState.Version++

		// Push to the attached client.
		if err := m.ApplyStateSync(daemonState); err != nil {
			t.Fatalf("window %d: ApplyStateSync: %v", i+1, err)
		}

		// The client absorbed the window and retiled; it syncs the result back to
		// the daemon (adoptSyncedWindows -> SyncStateToDaemon). Model that round
		// trip so the next create carries the client's tree, exactly as the daemon
		// would have stored it.
		daemonState = m.BuildSessionState()
		daemonState.Version = i + 2
	}

	if len(m.Windows) != 6 {
		t.Fatalf("client holds %d windows, want 6", len(m.Windows))
	}

	// Resolve the geometry the layout actually produced. Read it from the windows
	// themselves (animations disabled, so it is applied directly).
	type rect struct{ x, y, w, h int }
	rects := make([]rect, 0, 6)
	for _, w := range m.Windows {
		rects = append(rects, rect{w.X, w.Y, w.Width, w.Height})
		t.Logf("window %s: (%d,%d) %dx%d", w.ID, w.X, w.Y, w.Width, w.Height)
	}

	// No window may span the full width: that is the signature of an unplaced box
	// covering the screen.
	for _, r := range rects {
		if r.w >= width {
			t.Errorf("window at (%d,%d) spans the full width %d: it was never tiled", r.x, r.y, r.w)
		}
	}

	// No two windows may overlap.
	for a := 0; a < len(rects); a++ {
		for b := a + 1; b < len(rects); b++ {
			if rectsOverlap(rects[a].x, rects[a].y, rects[a].w, rects[a].h,
				rects[b].x, rects[b].y, rects[b].w, rects[b].h) {
				t.Errorf("windows overlap: (%d,%d %dx%d) and (%d,%d %dx%d)",
					rects[a].x, rects[a].y, rects[a].w, rects[a].h,
					rects[b].x, rects[b].y, rects[b].w, rects[b].h)
			}
		}
	}

	// The tiled area should cover the available viewport (allow the top margin).
	top := m.GetTopMargin()
	area := 0
	for _, r := range rects {
		area += r.w * r.h
	}
	want := width * (height - top)
	if area < want*9/10 {
		t.Errorf("tiled area = %d, want about %d (the windows leave a large gap)", area, want)
	}
}
