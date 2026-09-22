package app

import (
	"slices"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/ui"
)

// CreateRestoreAnimation creates a restore animation for the window at index i
func (m *OS) CreateRestoreAnimation(i int) *ui.Animation {
	if i < 0 || i >= len(m.Windows) {
		return nil
	}

	window := m.Windows[i]

	// Calculate dock position for this window
	dockX, dockY := m.calculateDockPosition(i)

	return ui.NewRestoreAnimation(window, dockX, dockY, m.Settings.GetAnimationDuration())
}

// CreateSnapAnimation creates a snap animation for the window at index i
func (m *OS) CreateSnapAnimation(i int, quarter SnapQuarter) *ui.Animation {
	if i < 0 || i >= len(m.Windows) {
		return nil
	}

	window := m.Windows[i]

	// Calculate target bounds for the snap
	targetX, targetY, targetWidth, targetHeight := m.calculateSnapBounds(quarter)

	// Enforce minimum size
	targetWidth = max(targetWidth, config.DefaultWindowWidth)
	targetHeight = max(targetHeight, config.DefaultWindowHeight)

	return ui.NewSnapAnimation(window, targetX, targetY, targetWidth, targetHeight, m.Settings.GetAnimationDuration())
}

// HasActiveAnimations returns true if there are any active animations
func (m *OS) HasActiveAnimations() bool {
	return len(m.Animations) > 0
}

// CompleteWindowAnimations immediately completes all animations for a specific window
// This is used when starting a new drag to avoid conflicts with pending animations
func (m *OS) CompleteWindowAnimations(windowIndex int) {
	if windowIndex < 0 || windowIndex >= len(m.Windows) {
		return
	}

	window := m.Windows[windowIndex]

	// Find and complete all animations for this window
	for i := len(m.Animations) - 1; i >= 0; i-- {
		anim := m.Animations[i]
		if anim.Window == window {
			// Landed through the animation's own last step, so the pane gets
			// the state its type leaves behind and its guest gets the one
			// resize, exactly as running the slide out would have given it.
			anim.Finish()
			m.Animations = slices.Delete(m.Animations, i, i+1)
		}
	}
}

// CancelAnimationsForWindow removes all pending animations for a window
// without completing them (the caller will set the new position).
func (m *OS) CancelAnimationsForWindow(w *terminal.Window) {
	for i := len(m.Animations) - 1; i >= 0; i-- {
		if m.Animations[i].Window == w {
			m.Animations = slices.Delete(m.Animations, i, i+1)
		}
	}
}

// CompleteAllAnimations immediately completes all active animations
// This is used in tiling mode to prevent state conflicts when starting a new drag
func (m *OS) CompleteAllAnimations() {
	// Complete all animations by snapping windows to their final positions
	for i := len(m.Animations) - 1; i >= 0; i-- {
		// See CompleteWindowAnimations: landing goes through the animation's
		// own last step so the guest is resized.
		m.Animations[i].Finish()
	}

	// Clear all animations
	m.Animations = m.Animations[:0]
}

// UpdateAnimations updates all active animations and applies their effects.
func (m *OS) UpdateAnimations() {
	// Update animations in reverse order so we can safely remove completed ones
	for i := len(m.Animations) - 1; i >= 0; i-- {
		anim := m.Animations[i]

		// Update the animation and check if it's complete
		isComplete := anim.Update()

		// If animation is complete, handle post-animation logic
		if isComplete {
			if m.KittyPassthrough != nil && anim.Window != nil && anim.Window.Terminal != nil {
				scrollbackLen := anim.Window.Terminal.ScrollbackLen()
				viewportHeight := anim.Window.ContentHeight()
				m.KittyPassthrough.OnWindowMove(
					anim.Window.ID,
					anim.EndX, anim.EndY,
					1, 1,
					scrollbackLen, anim.Window.ScrollbackOffset, viewportHeight,
				)
			}

			// Remove completed animation
			m.Animations = append(m.Animations[:i], m.Animations[i+1:]...)
		}
	}
}

// calculateDockPosition returns the cell a restore animation starts from: the
// centre of the entry the dock drew for the window on the last frame. The
// renderer records those rectangles in dockItemHits, so the animation starts
// where the user saw the entry, on whichever row the dock sits. A window with
// no drawn entry (dock hidden, or the entry overflowed) starts from the
// middle of the dock row.
func (m *OS) calculateDockPosition(windowIndex int) (int, int) {
	for _, h := range m.dockItemHits {
		if h.WindowIndex == windowIndex {
			return (h.X0 + h.X1) / 2, h.Y
		}
	}
	return m.GetRenderWidth() / 2, m.GetDockbarContentYPosition()
}
