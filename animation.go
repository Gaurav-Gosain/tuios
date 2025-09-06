package main

import (
	"math"
	"time"
)

// AnimationType represents the type of animation being performed.
type AnimationType int

const (
	// AnimationMinimize represents a window minimize animation.
	AnimationMinimize AnimationType = iota
	// AnimationRestore represents a window restore animation.
	AnimationRestore
	// AnimationSnap represents a window snap animation.
	AnimationSnap
)

// Animation represents an animated transition for a window.
type Animation struct {
	WindowIndex int
	Type        AnimationType
	StartTime   time.Time
	Duration    time.Duration
	StartX      int
	StartY      int
	StartWidth  int
	StartHeight int
	EndX        int
	EndY        int
	EndWidth    int
	EndHeight   int
	Progress    float64
	Complete    bool
}

// CreateMinimizeAnimation creates a minimize animation for the specified window.
func (m *OS) CreateMinimizeAnimation(windowIndex int) *Animation {
	if windowIndex < 0 || windowIndex >= len(m.Windows) {
		return nil
	}

	window := m.Windows[windowIndex]

	// Calculate dock position for this window
	dockX, dockY := m.GetDockPositionForWindow(windowIndex)

	return &Animation{
		WindowIndex: windowIndex,
		Type:        AnimationMinimize,
		StartTime:   time.Now(),
		Duration:    time.Duration(DefaultAnimationDuration) * time.Millisecond,
		StartX:      window.X,
		StartY:      window.Y,
		StartWidth:  window.Width,
		StartHeight: window.Height,
		EndX:        dockX,
		EndY:        dockY,
		EndWidth:    5, // Small size when minimized
		EndHeight:   3,
		Progress:    0,
		Complete:    false,
	}
}

// CreateRestoreAnimation creates a restore animation for the specified window.
func (m *OS) CreateRestoreAnimation(windowIndex int) *Animation {
	if windowIndex < 0 || windowIndex >= len(m.Windows) {
		return nil
	}

	window := m.Windows[windowIndex]

	// Calculate dock position for this window
	dockX, dockY := m.GetDockPositionForWindow(windowIndex)

	return &Animation{
		WindowIndex: windowIndex,
		Type:        AnimationRestore,
		StartTime:   time.Now(),
		Duration:    time.Duration(DefaultAnimationDuration) * time.Millisecond,
		StartX:      dockX,
		StartY:      dockY,
		StartWidth:  5,
		StartHeight: 3,
		EndX:        window.PreMinimizeX,
		EndY:        window.PreMinimizeY,
		EndWidth:    window.PreMinimizeWidth,
		EndHeight:   window.PreMinimizeHeight,
		Progress:    0,
		Complete:    false,
	}
}

// GetDockPositionForWindow returns the dock position for a window based on its index.
func (m *OS) GetDockPositionForWindow(windowIndex int) (int, int) {
	// Count minimized AND minimizing windows before this one
	minimizedCount := 0
	for i := 0; i < windowIndex && i < len(m.Windows); i++ {
		if m.Windows[i].Minimized || m.Windows[i].Minimizing {
			minimizedCount++
		}
	}

	// Calculate position in dock for pill-style items
	itemWidth := 6 // Width of each pill item including space
	totalMinimized := 0
	for _, w := range m.Windows {
		if w.Minimized || w.Minimizing {
			totalMinimized++
		}
	}

	// If this window would be minimized and isn't already counted, count it
	if !m.Windows[windowIndex].Minimized && !m.Windows[windowIndex].Minimizing {
		totalMinimized++
	}

	totalWidth := totalMinimized*itemWidth - 1
	// Calculate center position considering system info
	leftInfoWidth := 15
	rightInfoWidth := 30
	availableCenter := m.Width - leftInfoWidth - rightInfoWidth
	startX := leftInfoWidth + (availableCenter-totalWidth)/2

	x := startX + minimizedCount*itemWidth + 2 // Center of the pill
	y := m.Height - 1                          // Single line dock at bottom

	return x, y
}

// UpdateAnimations updates all active animations and applies their effects.
func (m *OS) UpdateAnimations() {
	now := time.Now()

	for i := len(m.Animations) - 1; i >= 0; i-- {
		anim := m.Animations[i]

		// Calculate progress (0.0 to 1.0)
		elapsed := now.Sub(anim.StartTime)
		progress := float64(elapsed) / float64(anim.Duration)

		if progress >= 1.0 {
			progress = 1.0
			anim.Complete = true
		}

		// Apply easing function (smooth in/out)
		anim.Progress = easeInOutCubic(progress)

		// Update window position/size based on animation
		if anim.WindowIndex >= 0 && anim.WindowIndex < len(m.Windows) {
			window := m.Windows[anim.WindowIndex]

			// Interpolate position and size
			newX := interpolate(anim.StartX, anim.EndX, anim.Progress)
			newY := interpolate(anim.StartY, anim.EndY, anim.Progress)
			newWidth := interpolate(anim.StartWidth, anim.EndWidth, anim.Progress)
			newHeight := interpolate(anim.StartHeight, anim.EndHeight, anim.Progress)

			// Update window position and size
			window.X = newX
			window.Y = newY

			// For snap animations, resize terminal during animation for smooth transition
			if anim.Type == AnimationSnap && (window.Width != newWidth || window.Height != newHeight) {
				window.Resize(newWidth, newHeight)
			} else {
				window.Width = newWidth
				window.Height = newHeight
			}

			// Mark window as dirty for re-rendering
			window.MarkPositionDirty()
			window.InvalidateCache()

			// If animation is complete, finalize the state
			if anim.Complete {
				switch anim.Type {
				case AnimationMinimize:
					// Actually minimize the window
					window.Minimized = true
					window.Minimizing = false // Clear minimizing flag
					window.X = window.PreMinimizeX
					window.Y = window.PreMinimizeY
					window.Width = window.PreMinimizeWidth
					window.Height = window.PreMinimizeHeight

					// NOW change focus after animation completes
					if anim.WindowIndex == m.FocusedWindow {
						m.FocusNextVisibleWindow()
					}
				case AnimationRestore:
					// Window is already restored, just ensure proper state
					window.Minimized = false
				case AnimationSnap:
					// Resize terminal to match final window size
					window.Resize(window.Width, window.Height)
				}

				// Remove completed animation
				m.Animations = append(m.Animations[:i], m.Animations[i+1:]...)
			}
		}
	}
}

// HasActiveAnimations returns true if there are any active animations.
func (m *OS) HasActiveAnimations() bool {
	return len(m.Animations) > 0
}

// Easing function for smooth animation
func easeInOutCubic(t float64) float64 {
	if t < 0.5 {
		return 4 * t * t * t
	}
	p := 2*t - 2
	return 1 + p*p*p/2
}

// Linear interpolation
func interpolate(start, end int, progress float64) int {
	return start + int(math.Round(float64(end-start)*progress))
}

// CreateSnapAnimation creates a snap animation for moving a window to a target position.
func (m *OS) CreateSnapAnimation(windowIndex int, snapType SnapQuarter) *Animation {
	if windowIndex < 0 || windowIndex >= len(m.Windows) {
		return nil
	}

	window := m.Windows[windowIndex]

	// Calculate target position and size for the snap
	targetX, targetY, targetWidth, targetHeight := m.calculateSnapBounds(snapType)

	// Don't animate if already at target
	if window.X == targetX && window.Y == targetY &&
		window.Width == targetWidth && window.Height == targetHeight {
		return nil
	}

	return &Animation{
		WindowIndex: windowIndex,
		Type:        AnimationSnap,
		StartTime:   time.Now(),
		Duration:    time.Duration(FastAnimationDuration) * time.Millisecond, // Faster for snapping
		StartX:      window.X,
		StartY:      window.Y,
		StartWidth:  window.Width,
		StartHeight: window.Height,
		EndX:        targetX,
		EndY:        targetY,
		EndWidth:    targetWidth,
		EndHeight:   targetHeight,
		Progress:    0,
		Complete:    false,
	}
}
