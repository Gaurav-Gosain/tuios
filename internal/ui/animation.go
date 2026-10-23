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

// springStiffness is the spring's angular frequency times the duration. A
// critically damped spring released from rest has (1+k)e^-k of its distance
// left at the end of the duration: at 7 that is 0.7%, under half a cell for a
// move across a 60-column pane, so landing exactly on the target when the
// clock runs out is not a visible step.
const springStiffness = 7.0

// The four edges a rectangle is animated as: left, top, right, bottom.
const (
	edgeLeft = iota
	edgeTop
	edgeRight
	edgeBottom
	edgeCount
)

// Animation represents an animated transition for a window.
//
// Each edge of the rectangle moves as a critically damped spring toward the
// target. A spring is the curve that can change its mind: Retarget hands the
// spring a new target from where the edge is and how fast it is moving, so a
// layout that changes again mid-flight bends the motion instead of restarting
// it from rest or snapping. The position is a closed form of the time since
// the last (re)start, so it does not depend on how often frames are drawn.
type Animation struct {
	Window    *terminal.Window
	Type      AnimationType
	StartTime time.Time
	Duration  time.Duration
	// The rectangle the motion last started from, and the one it ends at.
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

	// The spring's state at StartTime: each edge's offset from its target and
	// its velocity in cells per second. A fresh animation starts at rest at
	// the Start rectangle; Retarget carries the velocity across.
	offset [edgeCount]float64
	vel    [edgeCount]float64
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

	a := &Animation{
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
	}
	a.restFromStart()
	return a
}

// NewSnapAnimation creates a snap animation for moving a window to a target position.
// targetX, targetY, targetWidth, and targetHeight specify the final window bounds.
//
// It starts from the rectangle the window holds now, which is the rectangle
// last drawn for it. A window that already has a snap in flight should be
// retargeted instead (see Retarget), which keeps its speed.
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

	a := &Animation{
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
	}
	a.restFromStart()
	return a
}

// restFromStart sets the spring at rest on the Start rectangle.
func (a *Animation) restFromStart() {
	start := edgesOf(a.StartX, a.StartY, a.StartWidth, a.StartHeight)
	end := a.endEdges()
	for i := range edgeCount {
		a.offset[i] = start[i] - end[i]
		a.vel[i] = 0
	}
}

// HeadsTo reports whether the animation is already on its way to the given
// rectangle.
func (a *Animation) HeadsTo(x, y, width, height int) bool {
	return a.EndX == x && a.EndY == y && a.EndWidth == width && a.EndHeight == height
}

// Retarget points an animation in flight at a new rectangle.
//
// The motion continues from where each edge is at now, at the speed it is
// moving, so the window bends toward the new target with no jump and no
// stall. The clock restarts at now, so the window gets the full duration to
// arrive. Retargeting to the rectangle it is already heading for changes
// nothing: a layout pass that repeats itself must not restart the motion.
func (a *Animation) Retarget(x, y, width, height int, duration time.Duration, now time.Time) {
	if a.Complete || a.HeadsTo(x, y, width, height) {
		return
	}
	pos, vel := a.springAt(now)
	if duration > 0 {
		a.Duration = duration
	}
	end := edgesOf(x, y, width, height)
	for i := range edgeCount {
		a.offset[i] = pos[i] - end[i]
		a.vel[i] = vel[i]
	}
	a.StartX, a.StartY = a.Window.X, a.Window.Y
	a.StartWidth, a.StartHeight = a.Window.Width, a.Window.Height
	a.EndX, a.EndY, a.EndWidth, a.EndHeight = x, y, width, height
	a.StartTime = now
	a.Progress = 0
}

// Update updates the animation progress and applies changes to the window.
// Returns true if the animation is complete, false otherwise.
func (a *Animation) Update() bool {
	return a.UpdateAt(time.Now())
}

// UpdateAt is Update against a given clock reading. A caller stepping several
// animations for one frame passes them all the same reading, so panes that
// started together stay at the same progress and their shared edges agree.
func (a *Animation) UpdateAt(now time.Time) bool {
	if a.Complete {
		return true
	}

	// Don't resize the VT emulator during animation. Wait until it completes.
	// This prevents content overflow and size mismatch issues

	elapsed := now.Sub(a.StartTime)
	if a.Duration <= 0 || elapsed >= a.Duration {
		a.Complete = true
	}
	a.Progress = min(max(float64(elapsed)/float64(max(a.Duration, 1)), 0), 1)

	var newX, newY, newWidth, newHeight int
	if a.Complete {
		newX, newY, newWidth, newHeight = a.EndX, a.EndY, a.EndWidth, a.EndHeight
	} else {
		pos, _ := a.springAt(now)
		// The edges are rounded rather than the sizes. Two panes that share an
		// edge (a column and its neighbour on the scrolling strip, two tiles in
		// a split) then round that edge to the same cell on every frame.
		// Rounding the width on its own let the edge of one and the start of
		// the next land a cell apart, which drew a one-column gap or overlap
		// that flickered along the seam.
		left := roundEdge(pos[edgeLeft])
		top := roundEdge(pos[edgeTop])
		newX, newY = left, top
		newWidth = max(roundEdge(pos[edgeRight])-left, 1)
		newHeight = max(roundEdge(pos[edgeBottom])-top, 1)
		// A spring that has come within half a cell of the target draws the
		// target, and there is nothing left to show: land it now rather than
		// spend frames redrawing the same cells until the clock runs out.
		if newX == a.EndX && newY == a.EndY && newWidth == a.EndWidth && newHeight == a.EndHeight {
			a.Complete = true
		}
	}

	a.Window.X = newX
	a.Window.Y = newY
	a.Window.Width = newWidth
	a.Window.Height = newHeight

	// Mark window as dirty for re-rendering
	a.Window.MarkPositionDirty()
	a.Window.InvalidateCache()

	// If animation is complete, finalize the state
	if a.Complete {
		a.Progress = 1
		a.settle()
	}

	return a.Complete
}

// springAt returns each edge's position, in cells, and velocity, in cells per
// second, at now.
//
// A critically damped spring with offset e and velocity v at time zero is at
// target + (e + (v + w e) t) e^(-w t) at time t: it closes on the target as
// fast as a spring can without passing it when released from rest.
func (a *Animation) springAt(now time.Time) (pos, vel [edgeCount]float64) {
	end := a.endEdges()
	t := max(now.Sub(a.StartTime).Seconds(), 0)
	omega := springStiffness / math.Max(a.Duration.Seconds(), 1e-3)
	decay := math.Exp(-omega * t)
	for i := range edgeCount {
		e, v := a.offset[i], a.vel[i]
		b := v + omega*e
		off := (e + b*t) * decay
		// A spring handed a velocity toward the target can carry past it.
		// A pane edge never overshoots its slot: it stops there.
		if e != 0 && math.Signbit(off) != math.Signbit(e) {
			off = 0
			v = 0
		} else {
			v = (v - omega*b*t) * decay
		}
		pos[i] = end[i] + off
		vel[i] = v
	}
	return pos, vel
}

func (a *Animation) endEdges() [edgeCount]float64 {
	return edgesOf(a.EndX, a.EndY, a.EndWidth, a.EndHeight)
}

func edgesOf(x, y, width, height int) [edgeCount]float64 {
	return [edgeCount]float64{float64(x), float64(y), float64(x + width), float64(y + height)}
}

// roundEdge rounds half away from zero, so an edge shared by two panes lands
// on one cell for both.
func roundEdge(v float64) int {
	return int(math.Round(v))
}

// Finish lands an animation now, exactly as running it to its last frame would.
//
// It is what a caller that has to cut an animation short uses: starting a drag
// on a pane that is still sliding, or a layout change that has to happen at
// once. Stamping the end rectangle on the window by hand and marking the
// animation complete would skip the resize below, leaving the guest reflowed
// for the size the pane had when the slide began until something else resized
// it.
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
