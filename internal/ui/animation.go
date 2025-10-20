package ui

import (
	"math"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
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
	Window      *terminal.Window
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

// NewMinimizeAnimation creates a minimize animation for the specified window.
// dockX and dockY specify the target dock position for the minimized window.
func NewMinimizeAnimation(w *terminal.Window, dockX, dockY int, duration time.Duration) *Animation {
	return &Animation{
		Window:      w,
		Type:        AnimationMinimize,
		StartTime:   time.Now(),
		Duration:    duration,
		StartX:      w.X,
		StartY:      w.Y,
		StartWidth:  w.Width,
		StartHeight: w.Height,
		EndX:        dockX,
		EndY:        dockY,
		EndWidth:    5, // Small size when minimized
		EndHeight:   3,
		Progress:    0,
		Complete:    false,
	}
}

// NewRestoreAnimation creates a restore animation for the specified window.
// dockX and dockY specify the current dock position of the minimized window.
func NewRestoreAnimation(w *terminal.Window, dockX, dockY int, duration time.Duration) *Animation {
	return &Animation{
		Window:      w,
		Type:        AnimationRestore,
		StartTime:   time.Now(),
		Duration:    duration,
		StartX:      dockX,
		StartY:      dockY,
		StartWidth:  5,
		StartHeight: 3,
		EndX:        w.PreMinimizeX,
		EndY:        w.PreMinimizeY,
		EndWidth:    w.PreMinimizeWidth,
		EndHeight:   w.PreMinimizeHeight,
		Progress:    0,
		Complete:    false,
	}
}

// NewSnapAnimation creates a snap animation for moving a window to a target position.
// targetX, targetY, targetWidth, and targetHeight specify the final window bounds.
func NewSnapAnimation(w *terminal.Window, targetX, targetY, targetWidth, targetHeight int, duration time.Duration) *Animation {
	// Don't animate if already at target
	if w.X == targetX && w.Y == targetY &&
		w.Width == targetWidth && w.Height == targetHeight {
		return nil
	}

	return &Animation{
		Window:      w,
		Type:        AnimationSnap,
		StartTime:   time.Now(),
		Duration:    duration,
		StartX:      w.X,
		StartY:      w.Y,
		StartWidth:  w.Width,
		StartHeight: w.Height,
		EndX:        targetX,
		EndY:        targetY,
		EndWidth:    targetWidth,
		EndHeight:   targetHeight,
		Progress:    0,
		Complete:    false,
	}
}

// Update updates the animation progress and applies changes to the window.
// Returns true if the animation is complete, false otherwise.
func (a *Animation) Update() bool {
	if a.Complete {
		return true
	}

	now := time.Now()

	// Calculate progress (0.0 to 1.0)
	elapsed := now.Sub(a.StartTime)
	progress := float64(elapsed) / float64(a.Duration)

	if progress >= 1.0 {
		progress = 1.0
		a.Complete = true
	}

	// Apply easing function (smooth in/out)
	a.Progress = easeInOutCubic(progress)

	// Interpolate position and size
	newX := interpolate(a.StartX, a.EndX, a.Progress)
	newY := interpolate(a.StartY, a.EndY, a.Progress)
	newWidth := interpolate(a.StartWidth, a.EndWidth, a.Progress)
	newHeight := interpolate(a.StartHeight, a.EndHeight, a.Progress)

	// Update window position and size
	a.Window.X = newX
	a.Window.Y = newY

	// For snap animations, resize terminal during animation for smooth transition
	if a.Type == AnimationSnap && (a.Window.Width != newWidth || a.Window.Height != newHeight) {
		a.Window.Resize(newWidth, newHeight)
	} else {
		a.Window.Width = newWidth
		a.Window.Height = newHeight
	}

	// Mark window as dirty for re-rendering
	a.Window.MarkPositionDirty()
	a.Window.InvalidateCache()

	// If animation is complete, finalize the state
	if a.Complete {
		switch a.Type {
		case AnimationMinimize:
			// Actually minimize the window
			a.Window.Minimized = true
			a.Window.Minimizing = false // Clear minimizing flag
			a.Window.X = a.Window.PreMinimizeX
			a.Window.Y = a.Window.PreMinimizeY
			a.Window.Width = a.Window.PreMinimizeWidth
			a.Window.Height = a.Window.PreMinimizeHeight
		case AnimationRestore:
			// Window is already restored, just ensure proper state
			a.Window.Minimized = false
		case AnimationSnap:
			// Resize terminal to match final window size
			a.Window.Resize(a.Window.Width, a.Window.Height)
		}
	}

	return a.Complete
}

// easeInOutCubic applies cubic easing to the animation progress for smooth transitions.
func easeInOutCubic(t float64) float64 {
	if t < 0.5 {
		return 4 * t * t * t
	}
	p := 2*t - 2
	return 1 + p*p*p/2
}

// interpolate performs linear interpolation between start and end values.
func interpolate(start, end int, progress float64) int {
	return start + int(math.Round(float64(end-start)*progress))
}
