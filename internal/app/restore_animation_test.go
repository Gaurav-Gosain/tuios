package app

import "testing"

// TestRestoreAnimationStartsOnTheDrawnEntry checks that a restore animation
// starts from the entry the dock drew for the window, on the row the dock
// sits on. The start used to be recomputed from fixed region widths and a
// bottom dock, so with the dock at the top the pane flew in from the last row.
func TestRestoreAnimationStartsOnTheDrawnEntry(t *testing.T) {
	for _, pos := range []string{"bottom", "top"} {
		t.Run(pos, func(t *testing.T) {
			m := dockCrowdedOS(t, 120, 1, 2)
			m.Settings.DockbarPosition = pos
			m.Settings.AnimationsEnabled = true
			m.Settings.AnimationsSuppressed = false

			// Draw the dock once so its entry rectangles are recorded, as
			// the last frame would have before the user asked to restore.
			m.renderDockString()

			idx := len(m.Windows) - 1
			var hit *dockItemHit
			for i := range m.dockItemHits {
				if m.dockItemHits[i].WindowIndex == idx {
					hit = &m.dockItemHits[i]
				}
			}
			if hit == nil {
				t.Fatalf("the dock drew no entry for window %d", idx)
			}

			anim := m.CreateRestoreAnimation(idx)
			if anim == nil {
				t.Fatal("no restore animation with animations enabled")
			}
			if want := m.GetDockbarContentYPosition(); anim.StartY != want {
				t.Errorf("the animation starts on row %d, the dock is on row %d", anim.StartY, want)
			}
			if anim.StartX < hit.X0 || anim.StartX >= hit.X1 {
				t.Errorf("the animation starts at column %d, the entry covers %d..%d", anim.StartX, hit.X0, hit.X1-1)
			}
		})
	}
}

// TestRestoreAnimationWithoutADrawnEntry checks the fallback when the dock
// drew nothing for the window: the animation still starts on the dock row.
func TestRestoreAnimationWithoutADrawnEntry(t *testing.T) {
	m := dockCrowdedOS(t, 120, 1, 1)
	m.Settings.DockbarPosition = "top"
	m.Settings.AnimationsEnabled = true
	m.Settings.AnimationsSuppressed = false

	anim := m.CreateRestoreAnimation(len(m.Windows) - 1)
	if anim == nil {
		t.Fatal("no restore animation with animations enabled")
	}
	if anim.StartY != m.GetDockbarContentYPosition() || anim.StartX != m.GetRenderWidth()/2 {
		t.Errorf("the animation starts at (%d, %d), want the middle of the dock row (%d, %d)",
			anim.StartX, anim.StartY, m.GetRenderWidth()/2, m.GetDockbarContentYPosition())
	}
}
