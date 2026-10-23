package app

import (
	"time"

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

	return m.newSnapAnimation(window, targetX, targetY, targetWidth, targetHeight, m.Settings.GetAnimationDuration())
}

// newSnapAnimation is ui.NewSnapAnimation started on the layout clock. Every
// pane a message moves then starts on one clock: one key can lay the panes
// out several times, and panes started a few microseconds apart can round a
// shared edge a cell apart.
func (m *OS) newSnapAnimation(w *terminal.Window, x, y, width, height int, dur time.Duration) *ui.Animation {
	anim := ui.NewSnapAnimation(w, x, y, width, height, dur)
	if anim != nil {
		anim.StartTime = m.layoutClock()
	}
	return anim
}

// snapInFlight is the snap animation moving w, or nil.
func (m *OS) snapInFlight(w *terminal.Window) *ui.Animation {
	for _, a := range m.Animations {
		if a.Window == w && a.Type == ui.AnimationSnap && !a.Complete {
			return a
		}
	}
	return nil
}

// animateWindow moves w to a rectangle over dur, and returns the animation
// that does it, or nil when the window was placed at once.
//
// It is the one way a layout moves a pane: see addAnimation for what happens
// to a window that is already sliding.
func (m *OS) animateWindow(w *terminal.Window, x, y, width, height int, dur time.Duration) *ui.Animation {
	if a := m.snapInFlight(w); a != nil {
		if dur > 0 {
			a.Retarget(x, y, width, height, dur, m.layoutClock())
			return a
		}
		m.CancelSnapAnimation(w)
	}
	anim := m.newSnapAnimation(w, x, y, width, height, dur)
	m.addAnimation(anim)
	return anim
}

// addAnimation starts an animation, keeping to one snap per window.
//
// A window already sliding is retargeted rather than given a second snap: it
// bends from the rectangle drawn on screen toward the new one, at the speed
// it already has, instead of stopping, restarting from rest or jumping. Two
// snaps on one window would each stamp their own rectangle on it every frame,
// and the window would flicker between them until the first one landed and
// threw it to its own target.
func (m *OS) addAnimation(anim *ui.Animation) {
	if anim == nil {
		return
	}
	if old := m.snapInFlight(anim.Window); old != nil && old != anim {
		if anim.Type == ui.AnimationSnap {
			old.Retarget(anim.EndX, anim.EndY, anim.EndWidth, anim.EndHeight, anim.Duration, anim.StartTime)
			return
		}
		m.CancelSnapAnimation(anim.Window)
	}
	m.Animations = append(m.Animations, anim)
}

// HasActiveAnimations returns true if there are any active animations
func (m *OS) HasActiveAnimations() bool {
	return len(m.Animations) > 0
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
		// Landed through the animation's own last step, so the pane gets the
		// state its type leaves behind and its guest gets the one resize,
		// exactly as running the slide out would have given it.
		m.Animations[i].Finish()
	}

	// Clear all animations
	m.Animations = m.Animations[:0]
}

// UpdateAnimations updates all active animations and applies their effects.
func (m *OS) UpdateAnimations() {
	// One clock reading for every animation on this frame, so panes that
	// started together are drawn at the same progress. See UpdateAt.
	now := time.Now()
	// Update animations in reverse order so we can safely remove completed ones
	for i := len(m.Animations) - 1; i >= 0; i-- {
		anim := m.Animations[i]

		// Update the animation and check if it's complete
		isComplete := anim.UpdateAt(now)

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

// layoutAnimationDuration is how long a tiler slides panes into a new layout:
// the configured duration, or none while layoutInstant is set.
func (m *OS) layoutAnimationDuration() time.Duration {
	if m.layoutInstant {
		return 0
	}
	return m.Settings.GetAnimationDuration()
}

// layoutClock is the start time for a slide armed while handling the current
// message: one reading for the whole message, so every pane a layout moves in
// response to one key starts on the same clock. Outside a message it is now.
func (m *OS) layoutClock() time.Time {
	if m.msgClock.IsZero() {
		return time.Now()
	}
	return m.msgClock
}
