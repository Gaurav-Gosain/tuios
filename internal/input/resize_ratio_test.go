package input

import (
	"fmt"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// Resizing one divider changes the rectangle every split below it is laid out
// in. Those splits were not dragged, so their proportions have to survive the
// change: a nested pair that was even stays even as the region it lives in
// grows or shrinks, and a drag that returns to where it began leaves the whole
// layout where it began.
//
// The failure this pins is not a single wrong frame. Every path that changes
// geometry without going through the tree asks for a ratio sync, and the sync
// re-derives every split in the tree from integer geometry. Integer geometry
// cannot represent most ratios, so each pass rounds the ratio towards the cell
// boundary below it, and the loss is in the same direction every time. Over one
// drag that is dozens of passes, and the nested pair walks off centre and stays
// there.

// ratioOS is the maintainer's layout: a full-width pane across the top, and
// below it a left pane beside a right column split into two. Dragging the
// divider under the top pane resizes the lower region, which is the parent of
// the nested pair nobody touched.
func ratioOS(t *testing.T) *app.OS {
	t.Helper()

	m := isoOS(t, 4)
	buildTree(m, func(leaf func(int) *layout.TileNode) *layout.TileNode {
		return h(0.35, leaf(1), v(0.5, leaf(2), h(0.5, leaf(3), leaf(4))))
	})
	return m
}

// nestedRatio returns the stored ratio of the split between the two panes in
// the right column, which is the one no drag in these tests targets.
func nestedRatio(m *app.OS) float64 {
	return m.WorkspaceTrees[1].Root.Right.Right.SplitRatio
}

// pair returns the heights of the two panes in the right column.
func pair(m *app.OS) (int, int) {
	return m.Windows[2].Height, m.Windows[3].Height
}

// startDrag puts the model into the state a mouse press on the bottom edge of
// the top pane leaves behind, so the motion events that follow are the real
// drag path rather than a direct call into the resize code.
func startDrag(m *app.OS) (startX, startY int) {
	win := m.Windows[0]
	m.FocusedWindow = 0
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
			prev := config.SharedBorders
			config.SharedBorders = shared
			t.Cleanup(func() { config.SharedBorders = prev })

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

			// A retile rebuilds every pane from the ratios, so a ratio that drifted
			// shows up here even when the geometry happened to land back on its
			// starting cells.
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
			prev := config.SharedBorders
			config.SharedBorders = shared
			t.Cleanup(func() { config.SharedBorders = prev })

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
				// An even split of an odd number of rows is off by one and cannot be
				// anything else. More than that is the region being handed to one
				// pane rather than shared.
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
