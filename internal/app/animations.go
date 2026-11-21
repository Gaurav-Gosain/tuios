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

	dockX, dockY := m.calculateDockPosition(i)

	return ui.NewRestoreAnimation(window, dockX, dockY, m.Settings.GetAnimationDuration())
}

// CreateSnapAnimation creates a snap animation for the window at index i
func (m *OS) CreateSnapAnimation(i int, quarter SnapQuarter) *ui.Animation {
	if i < 0 || i >= len(m.Windows) {
		return nil
	}

	window := m.Windows[i]

	targetX, targetY, targetWidth, targetHeight := m.calculateSnapBounds(quarter)

	targetWidth = max(targetWidth, config.DefaultWindowWidth)
	targetHeight = max(targetHeight, config.DefaultWindowHeight)

	return m.newSnapAnimation(window, targetX, targetY, targetWidth, targetHeight, m.Settings.GetAnimationDuration())
}

func (m *OS) newSnapAnimation(w *terminal.Window, x, y, width, height int, dur time.Duration) *ui.Animation {
	anim := ui.NewSnapAnimation(w, x, y, width, height, dur)
	if anim != nil {
		anim.StartTime = m.layoutClock()
	}
	return anim
}

func (m *OS) snapInFlight(w *terminal.Window) *ui.Animation {
	for _, a := range m.Animations {
		if a.Window == w && a.Type == ui.AnimationSnap && !a.Complete {
			return a
		}
	}
	return nil
}

func (m *OS) animateWindow(w *terminal.Window, x, y, width, height int, dur time.Duration) *ui.Animation {
	if m.wsSlide.running() || m.arriving {
		dur = 0
	}
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
	for i := len(m.Animations) - 1; i >= 0; i-- {
		m.Animations[i].Finish()
	}

	m.Animations = m.Animations[:0]
}

// UpdateAnimations updates all active animations and applies their effects.
func (m *OS) UpdateAnimations() {
	now := time.Now()
	for i := len(m.Animations) - 1; i >= 0; i-- {
		anim := m.Animations[i]

		isComplete := anim.UpdateAt(now)

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

			m.Animations = append(m.Animations[:i], m.Animations[i+1:]...)
		}
	}
}

func (m *OS) calculateDockPosition(windowIndex int) (int, int) {
	for _, h := range m.dockItemHits {
		if h.WindowIndex == windowIndex {
			return (h.X0 + h.X1) / 2, h.Y
		}
	}
	return m.GetRenderWidth() / 2, m.GetDockbarContentYPosition()
}

func (m *OS) layoutAnimationDuration() time.Duration {
	if m.layoutInstant {
		return 0
	}
	return m.Settings.GetAnimationDuration()
}

func (m *OS) layoutClock() time.Time {
	if m.msgClock.IsZero() {
		return time.Now()
	}
	return m.msgClock
}
