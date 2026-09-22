// Package ui provides UI component animations and visual effects.
package ui

import (
	"math"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// AnimationType represents the type of animation being performed.
type AnimationType int

// The zero AnimationType is not a valid type, so a zero Animation settles to
// nothing rather than restoring or resizing its window.
const (
	// AnimationRestore represents a window restore animation.
	AnimationRestore AnimationType = iota + 1
	// AnimationSnap represents a window snap animation.
	AnimationSnap
)

const (
	// MinAnimatedWidth and MinAnimatedHeight are the smallest box an animation
	// is allowed to shrink a window to. It is the size a restore grows from,
	// so it is the smallest box the renderer is known to still draw a border
	// into; anything an animation scales down has to stop here.
	MinAnimatedWidth  = 5
	MinAnimatedHeight = 3
)

// Animation represents an animated transition for a window.
type Animation struct {
	Window         *terminal.Window
	Type           AnimationType
	StartTime      time.Time
	Duration       time.Duration
	StartX         int
	StartY         int
	StartWidth     int
	StartHeight    int
	EndX           int
	EndY           int
	EndWidth       int
	EndHeight      int
	Progress       float64
	Complete       bool
	InitialResized bool // Track if we've done the initial resize
}

// NewRestoreAnimation creates a restore animation for the specified window.
// dockX and dockY specify the current dock position of the minimized window.
func NewRestoreAnimation(w *terminal.Window, dockX, dockY int, duration time.Duration) *Animation {
	// If duration is 0, instantly restore without animation
	if duration == 0 {
		w.Minimized = false
		w.X = w.PreMinimizeX
		w.Y = w.PreMinimizeY
		w.Resize(w.PreMinimizeWidth, w.PreMinimizeHeight)
		w.MarkPositionDirty()
		w.InvalidateCache()
		return nil
	}

	return &Animation{
		Window:      w,
		Type:        AnimationRestore,
		StartTime:   time.Now(),
		Duration:    duration,
		StartX:      dockX,
		StartY:      dockY,
		StartWidth:  MinAnimatedWidth,
		StartHeight: MinAnimatedHeight,
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
		// Even if position matches, ensure PTY dimensions are synced
		// This handles the case where daemon PTY has stale dimensions after reattach
		if w.DaemonMode {
			w.Resize(targetWidth, targetHeight)
		}
		return nil
	}

	// If duration is 0, instantly apply the position without animation
	if duration == 0 {
		w.X = targetX
		w.Y = targetY
		w.Resize(targetWidth, targetHeight)
		w.MarkPositionDirty()
		w.InvalidateCache()
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

	// Don't resize the VT emulator during animation - wait until complete
	// This prevents content overflow and size mismatch issues

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

	// Animate position and visual size for smooth animations
	newX := interpolate(a.StartX, a.EndX, a.Progress)
	newY := interpolate(a.StartY, a.EndY, a.Progress)
	newWidth := interpolate(a.StartWidth, a.EndWidth, a.Progress)
	newHeight := interpolate(a.StartHeight, a.EndHeight, a.Progress)

	a.Window.X = newX
	a.Window.Y = newY
	a.Window.Width = newWidth
	a.Window.Height = newHeight

	// Mark window as dirty for re-rendering
	a.Window.MarkPositionDirty()
	a.Window.InvalidateCache()

	// If animation is complete, finalize the state
	if a.Complete {
		a.settle()
	}

	return a.Complete
}

// Finish lands an animation now, exactly as running it to its last frame would.
//
// It is what a caller that has to cut an animation short uses: starting a drag
// on a pane that is still sliding, or a layout change that has to happen at
// once. Those used to stamp the end rectangle on the window by hand and mark
// the animation complete, which skipped the resize below, so the pane arrived
// at its destination with its guest still reflowed for the size it had when the
// slide began. Nothing corrected it until something else resized that pane.
func (a *Animation) Finish() {
	if a.Complete {
		return
	}
	a.Progress = 1
	a.Complete = true
	a.Window.X = a.EndX
	a.Window.Y = a.EndY
	a.Window.Width = a.EndWidth
	a.Window.Height = a.EndHeight
	a.Window.MarkPositionDirty()
	a.Window.InvalidateCache()
	a.settle()
}

// settle is the last step of an animation: the state its type leaves behind,
// and the one resize the guest is told about. Called from Update when the clock
// runs out and from Finish when a caller lands it early, so the two cannot come
// to mean different things.
func (a *Animation) settle() {
	switch a.Type {
	case AnimationRestore, AnimationSnap:
		// Resize at completion for clean, one-time resize
		a.Window.Resize(a.EndWidth, a.EndHeight)
		// Ensure final position is exact
		a.Window.X = a.EndX
		a.Window.Y = a.EndY
		if a.Type == AnimationRestore {
			a.Window.Minimized = false
		}
	}
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
