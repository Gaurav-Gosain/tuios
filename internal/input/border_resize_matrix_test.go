package input

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

func borderMatrixOS(t *testing.T, tiling, shared bool) (*app.OS, *terminal.Window, *terminal.Window) {
	t.Helper()
	app.SetInputHandler(HandleInput)

	prevAnim := config.Global.Motion
	prevShared := config.Global.SharedBorders
	config.Global.Motion = config.MotionNone
	config.Global.SharedBorders = shared
	t.Cleanup(func() {
		config.Global.Motion = prevAnim
		config.Global.SharedBorders = prevShared
	})

	const cols, rows = 120, 40
	m := &app.OS{
		Settings:             config.Global,
		NumWorkspaces:        9,
		CurrentWorkspace:     1,
		WorkspaceFocus:       make(map[int]int),
		Width:                cols,
		Height:               rows,
		AutoTiling:           true,
		UseBSPLayout:         true,
		SharedBorders:        shared,
		FocusedWindow:        0,
		PendingResizes:       make(map[string][2]int),
		WorkspaceHasCustom:   map[int]bool{},
		WorkspaceLayouts:     map[int][]app.WindowLayout{},
		WorkspaceMasterRatio: map[int]float64{},
	}
	for i := range 2 {
		id := fmt.Sprintf("matrix-%d", i)
		ptyData := make(chan struct{}, 1)
		done := make(chan struct{})
		go func() {
			for {
				select {
				case <-ptyData:
				case <-done:
					return
				}
			}
		}()
		t.Cleanup(func() { close(done) })
		win := terminal.NewDaemonWindow(id, "test", 0, 0, cols, rows, 0, "pty-"+id, ptyData, config.DefaultScrollbackLines)
		if win == nil {
			t.Fatal("NewDaemonWindow returned nil")
		}
		t.Cleanup(win.Close)
		win.Workspace = 1
		m.Windows = append(m.Windows, win)
	}
	m.TileAllWindows()
	if !tiling {
		m.ToggleAutoTiling()
	}

	a, b := m.Windows[0], m.Windows[1]
	if a.X == b.X {
		t.Fatalf("panes were not split side by side: both at x=%d", a.X)
	}
	if a.X < b.X {
		return m, a, b
	}
	return m, b, a
}

func dividerColumn(w *terminal.Window) int {
	return w.X + w.Width - w.BorderOffset()
}

func send(m *app.OS, msg tea.Msg) *app.OS {
	next, _ := m.Update(msg)
	return next.(*app.OS)
}

// TestBorderDragResizesInEveryBorderMode is the 2x2 matrix for the drag half of
// the pair: whichever way tiling and shared borders are set, a press on the
// division between two panes must arm a resize and a drag must move it.
func TestBorderDragResizesInEveryBorderMode(t *testing.T) {
	for _, tiling := range []bool{true, false} {
		for _, shared := range []bool{false, true} {
			name := fmt.Sprintf("tiling=%v/shared=%v", tiling, shared)
			t.Run(name, func(t *testing.T) {
				m, left, right := borderMatrixOS(t, tiling, shared)

				grabX := dividerColumn(left)
				rowY := left.Y + left.Height/2
				beforeLeft, beforeRight := left.Width, right.Width

				m = send(m, clickMsg(grabX, rowY))
				if !m.BorderResizing || m.BorderResizeEdge != app.BorderEdgeRight {
					t.Fatalf("press at x=%d on the division armed no right-edge resize (resizing=%v edge=%v)",
						grabX, m.BorderResizing, m.BorderResizeEdge)
				}

				m = send(m, motionMsg(grabX-10, rowY))
				if left.Width >= beforeLeft {
					t.Errorf("left pane did not shrink: %d -> %d", beforeLeft, left.Width)
				}
				if tiling && right.Width <= beforeRight {
					t.Errorf("right pane did not take the space: %d -> %d", beforeRight, right.Width)
				}

				m = send(m, releaseMsg(grabX-10, rowY))
				if m.BorderResizing {
					t.Error("border resize still armed after release")
				}
				if len(m.PendingResizes) != 0 {
					t.Errorf("deferred PTY resizes not flushed on release: %d left", len(m.PendingResizes))
				}
			})
		}
	}
}

// TestBorderDragGrabPointIsNeutral pins the other half of the off-by-one: the
// press itself must not move the division. A grab column read as one cell past
// the pane resizes it the moment it is touched, before the pointer has moved.
func TestBorderDragGrabPointIsNeutral(t *testing.T) {
	for _, tiling := range []bool{true, false} {
		for _, shared := range []bool{false, true} {
			name := fmt.Sprintf("tiling=%v/shared=%v", tiling, shared)
			t.Run(name, func(t *testing.T) {
				m, left, _ := borderMatrixOS(t, tiling, shared)

				grabX := dividerColumn(left)
				rowY := left.Y + left.Height/2
				before := left.Width

				m = send(m, clickMsg(grabX, rowY))
				m = send(m, motionMsg(grabX, rowY))
				if left.Width != before {
					t.Errorf("grabbing the division at x=%d resized the pane on its own: %d -> %d",
						grabX, before, left.Width)
				}
				send(m, releaseMsg(grabX, rowY))
			})
		}
	}
}

// TestBorderDragHoldsAcrossSettingChanges walks the settings the way a user
// does, flipping one at a time in both directions, because a setting changed at
// runtime is what leaves a pane's drawn border and its geometry disagreeing.
func TestBorderDragHoldsAcrossSettingChanges(t *testing.T) {
	m, left, right := borderMatrixOS(t, true, true)

	drag := func(label string) {
		t.Helper()
		grabX := dividerColumn(left)
		rowY := left.Y + left.Height/2
		beforeLeft := left.Width

		m = send(m, clickMsg(grabX, rowY))
		if !m.BorderResizing {
			t.Fatalf("%s: press at x=%d armed no border resize", label, grabX)
		}
		m = send(m, motionMsg(grabX-8, rowY))
		if left.Width >= beforeLeft {
			t.Errorf("%s: left pane did not shrink: %d -> %d", label, beforeLeft, left.Width)
		}
		m = send(m, releaseMsg(grabX-8, rowY))
	}

	drag("shared+tiled")

	config.Global.SharedBorders = false
	m.SharedBorders = false
	m.TileAllWindows()
	drag("own-borders+tiled")

	config.Global.SharedBorders = true
	m.SharedBorders = true
	m.TileAllWindows()
	drag("shared+tiled-again")

	m.ToggleAutoTiling()
	drag("shared+floating")

	m.ToggleAutoTiling()
	drag("shared+tiled-restored")

	if right.Width <= 0 {
		t.Fatalf("neighbour pane collapsed to %d columns", right.Width)
	}
}
