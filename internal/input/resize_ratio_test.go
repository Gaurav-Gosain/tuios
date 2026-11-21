package input

import (
	"fmt"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

func ratioOS(t *testing.T) *app.OS {
	t.Helper()

	m := isoOS(t, 4)
	buildTree(m, func(leaf func(int) *layout.TileNode) *layout.TileNode {
		return h(0.35, leaf(1), v(0.5, leaf(2), h(0.5, leaf(3), leaf(4))))
	})
	return m
}

func nestedRatio(m *app.OS) float64 {
	return m.WorkspaceTrees[1].Root.Right.Right.SplitRatio
}

func pair(m *app.OS) (int, int) {
	return m.Windows[2].Height, m.Windows[3].Height
}

func startDrag(m *app.OS) (startX, startY int) {
	win := m.Windows[0]
	m.FocusedWindow = 0
	m.DraggedWindowIndex = 0
	m.Resizing = true
	m.InteractionMode = true
	m.ResizeCorner = app.BottomRight
	m.PreResizeState = terminal.Window{
		ID: win.ID, X: win.X, Y: win.Y, Z: win.Z,
		Width: win.Width, Height: win.Height,
	}
	startX, startY = win.X+win.Width, win.Y+win.Height
	m.ResizeStartX, m.ResizeStartY = startX, startY
	return startX, startY
}

// TestNestedRatioSurvivesAnOutAndBackDrag is the strongest statement of the
// bug: a drag that ends where it started must be a no-op, in the geometry the
// user sees and in the ratios a later retile rebuilds it from.
func TestNestedRatioSurvivesAnOutAndBackDrag(t *testing.T) {
	app.SetInputHandler(HandleInput)

	for _, shared := range []bool{false, true} {
		t.Run(fmt.Sprintf("shared=%v", shared), func(t *testing.T) {
			prev := config.Global.SharedBorders
			config.Global.SharedBorders = shared
			t.Cleanup(func() { config.Global.SharedBorders = prev })

			m := ratioOS(t)
			wantRatio := nestedRatio(m)
			wantGeom := geomOf(m)

			startX, startY := startDrag(m)
			const reach = 6
			for y := startY; y >= startY-reach; y-- {
				_, _ = m.Update(motionAt(startX, y))
				_ = m.View()
			}
			for y := startY - reach; y <= startY; y++ {
				_, _ = m.Update(motionAt(startX, y))
				_ = m.View()
			}
			_, _ = m.Update(releaseAt(startX, startY))

			if got := nestedRatio(m); got != wantRatio {
				t.Errorf("the untouched nested split moved: ratio %.6f -> %.6f", wantRatio, got)
			}
			for id, want := range wantGeom {
				if got := geomOf(m)[id]; got != want {
					t.Errorf("pane %s did not return to where the drag started: %v -> %v", id, want, got)
				}
			}

			m.TileAllWindows()
			for id, want := range wantGeom {
				if got := geomOf(m)[id]; got != want {
					t.Errorf("pane %s moved on the retile after the drag: %v -> %v", id, want, got)
				}
			}
		})
	}
}

// TestGrowingARegionDistributesItAcrossTheNestedSplit is the direct property:
// the two panes in the right column start even, so as the region they sit in
// grows they have to stay even. One of them absorbing the whole change is the
// user-visible form of the bug.
func TestGrowingARegionDistributesItAcrossTheNestedSplit(t *testing.T) {
	app.SetInputHandler(HandleInput)

	for _, shared := range []bool{false, true} {
		t.Run(fmt.Sprintf("shared=%v", shared), func(t *testing.T) {
			prev := config.Global.SharedBorders
			config.Global.SharedBorders = shared
			t.Cleanup(func() { config.Global.SharedBorders = prev })

			m := ratioOS(t)
			startTop, startBottom := pair(m)
			if startTop != startBottom {
				t.Fatalf("the nested pair is not even to begin with (%d, %d); "+
					"this test has nothing to measure", startTop, startBottom)
			}

			startX, startY := startDrag(m)
			grew := false
			for y := startY; y >= startY-8; y-- {
				_, _ = m.Update(motionAt(startX, y))
				_ = m.View()

				top, bottom := pair(m)
				if top+bottom > startTop+startBottom {
					grew = true
				}
				if d := top - bottom; d > 1 || d < -1 {
					t.Fatalf("region grown to %d rows was not shared: top=%d bottom=%d",
						top+bottom, top, bottom)
				}
			}
			if !grew {
				t.Fatal("the drag never grew the region, so nothing was measured")
			}
		})
	}
}
