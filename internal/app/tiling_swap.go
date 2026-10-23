package app

import (
	"fmt"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/ui"
)

// SwapWindowsInstant swaps the positions of two windows instantly without animation
func (m *OS) SwapWindowsInstant(index1, index2 int) {
	m.swapWindows(index1, index2, 0)
}

// destinationOf is the rectangle a window is on its way to: the target of
// the snap moving it, or where it is when nothing is.
func (m *OS) destinationOf(w *terminal.Window) (x, y, width, height int) {
	if a := m.snapInFlight(w); a != nil {
		return a.EndX, a.EndY, a.EndWidth, a.EndHeight
	}
	return w.X, w.Y, w.Width, w.Height
}

// swapWindows trades the slots of two windows, sliding each to the other's
// over dur, or at once when dur is zero.
func (m *OS) swapWindows(index1, index2 int, dur time.Duration) {
	if index1 < 0 || index1 >= len(m.Windows) || index2 < 0 || index2 >= len(m.Windows) {
		return
	}

	window1 := m.Windows[index1]
	window2 := m.Windows[index2]

	// The slots are where the windows are going, not where a slide in flight
	// happens to have them on this frame.
	x1, y1, w1, h1 := m.destinationOf(window1)
	x2, y2, w2, h2 := m.destinationOf(window2)

	if dur > 0 {
		m.animateWindow(window1, x2, y2, w2, h2, dur)
		m.animateWindow(window2, x1, y1, w1, h1, dur)
		m.finishSwap(index1, index2)
		return
	}
	m.CancelSnapAnimation(window1)
	m.CancelSnapAnimation(window2)

	// Swap positions instantly
	window1.X = x2
	window1.Y = y2
	window1.Width = w2
	window1.Height = h2
	window1.Resize(w2, h2)
	window1.MarkPositionDirty()
	window1.InvalidateCache()

	window2.X = x1
	window2.Y = y1
	window2.Width = w1
	window2.Height = h1
	window2.Resize(w1, h1)
	window2.MarkPositionDirty()
	window2.InvalidateCache()

	m.finishSwap(index1, index2)
}

// finishSwap records a swap of two windows' slots: in the BSP tree, in the
// window order, in the focus, and on the daemon.
func (m *OS) finishSwap(index1, index2 int) {
	window1, window2 := m.Windows[index1], m.Windows[index2]
	// Swap windows in the BSP tree so the layout is preserved on retile
	m.SwapWindowsInBSPTree(window1, window2)

	// Swap windows in the slice so the order is persisted.
	// MultifocusSet is keyed by window ID, so the swap needs no remap.
	m.Windows[index1], m.Windows[index2] = m.Windows[index2], m.Windows[index1]

	// Update focused window index if needed
	switch m.FocusedWindow {
	case index1:
		m.FocusedWindow = index2
	case index2:
		m.FocusedWindow = index1
	}

	// Sync state to daemon
	m.SyncStateToDaemon()
}

// SwapWindowsWithOriginal swaps windows where the dragged window's original position is provided
func (m *OS) SwapWindowsWithOriginal(draggedIndex, targetIndex int, origX, origY, origWidth, origHeight int) {
	if draggedIndex < 0 || draggedIndex >= len(m.Windows) || targetIndex < 0 || targetIndex >= len(m.Windows) {
		return
	}

	draggedWindow := m.Windows[draggedIndex]
	targetWindow := m.Windows[targetIndex]

	// Store target's current position before any modifications
	targetX, targetY, targetW, targetH := targetWindow.X, targetWindow.Y, targetWindow.Width, targetWindow.Height

	// Swap windows in the BSP tree FIRST so the layout is preserved on retile
	m.SwapWindowsInBSPTree(draggedWindow, targetWindow)

	// Swap windows in the slice.
	// MultifocusSet is keyed by window ID, so the swap needs no remap.
	m.Windows[draggedIndex], m.Windows[targetIndex] = m.Windows[targetIndex], m.Windows[draggedIndex]

	// Update focused window index if needed
	switch m.FocusedWindow {
	case draggedIndex:
		m.FocusedWindow = targetIndex
	case targetIndex:
		m.FocusedWindow = draggedIndex
	}

	// Now create animations. After the slice swap the indices are swapped:
	// draggedWindow is now at targetIndex, targetWindow is now at draggedIndex.

	// Dragged window goes to target's original position (with animation)
	anim1 := ui.NewSnapAnimation(
		draggedWindow,
		targetX, targetY, targetW, targetH,
		m.Settings.GetFastAnimationDuration(),
	)

	// Target window goes to dragged window's ORIGINAL position (with animation)
	anim2 := ui.NewSnapAnimation(
		targetWindow,
		origX, origY, origWidth, origHeight,
		m.Settings.GetFastAnimationDuration(),
	)

	if anim1 != nil {
		m.addAnimation(anim1)
	}
	if anim2 != nil {
		m.addAnimation(anim2)
	}

	// Sync state to daemon after animations are set up
	// The animation will update positions, and we sync again when complete
	m.SyncStateToDaemon()
}

// TileRemainingWindows tiles all windows except the one being minimized
func (m *OS) TileRemainingWindows(excludeIndex int) {
	// Get list of visible windows in current workspace (not minimized and not the one being minimized)
	var visibleWindows []*terminal.Window
	var visibleIndices []int
	for i, w := range m.Windows {
		if i != excludeIndex && w.Workspace == m.CurrentWorkspace && !w.Minimized {
			visibleWindows = append(visibleWindows, w)
			visibleIndices = append(visibleIndices, i)
		}
	}

	if len(visibleWindows) == 0 {
		return
	}

	// Calculate tiling layout based on number of remaining windows, inside the
	// content region beside any reserved sidebar band.
	layouts := m.contentTileLayouts(len(visibleWindows))

	// Apply layout with animations
	for i, idx := range visibleIndices {
		if i >= len(layouts) {
			break
		}

		l := layouts[i]

		// Create animation for smooth transition
		anim := ui.NewSnapAnimation(
			m.Windows[idx],
			l.X, l.Y, l.Width, l.Height,
			m.Settings.GetAnimationDuration(),
		)

		if anim != nil {
			m.addAnimation(anim)
		}
	}
}

// SwapWindow swaps the focused window with the adjacent window in the given direction
func (m *OS) SwapWindow(dir Direction) {
	if m.FocusedWindow < 0 || m.FocusedWindow >= len(m.Windows) {
		return
	}

	focusedWindow := m.Windows[m.FocusedWindow]

	// Don't swap floating windows
	if focusedWindow.IsFloating {
		return
	}
	targetIndex := m.findAdjacentWindow(focusedWindow, dir)

	if targetIndex >= 0 {
		// The two panes trade places with the fast slide a dropped pane
		// uses, so the eye can follow which pane went where. A swap pressed
		// again while they slide trades their destinations.
		m.swapWindows(m.FocusedWindow, targetIndex, m.Settings.GetFastAnimationDuration())
	}
}

// findAdjacentWindow finds the closest window in the given direction
func (m *OS) findAdjacentWindow(focused *terminal.Window, dir Direction) int {
	// Judged by where the panes are going, so a swap pressed while they
	// still slide picks the neighbour the layout gives them.
	fx, fy, fw, fh := m.destinationOf(focused)
	targetIndex := -1
	var minDistance int

	// Set initial distance based on direction (horizontal or vertical)
	if dir == DirLeft || dir == DirRight {
		minDistance = m.Width
	} else {
		minDistance = m.Height
	}

	for i, window := range m.Windows {
		if i == m.FocusedWindow || window.Workspace != m.CurrentWorkspace || window.Minimized || window.IsFloating {
			continue
		}

		wx, wy, ww, wh := m.destinationOf(window)
		var isInDirection, overlaps bool
		var distance int

		switch dir {
		case DirLeft:
			isInDirection = wx+ww <= fx+swapTolerance
			overlaps = wy < fy+fh && wy+wh > fy
			distance = fx - (wx + ww)
		case DirRight:
			isInDirection = wx >= fx+fw-swapTolerance
			overlaps = wy < fy+fh && wy+wh > fy
			distance = wx - (fx + fw)
		case DirUp:
			isInDirection = wy+wh <= fy+swapTolerance
			overlaps = wx < fx+fw && wx+ww > fx
			distance = fy - (wy + wh)
		case DirDown:
			isInDirection = wy >= fy+fh-swapTolerance
			overlaps = wx < fx+fw && wx+ww > fx
			distance = wy - (fy + fh)
		}

		if isInDirection && overlaps && distance < minDistance {
			minDistance = distance
			targetIndex = i
		}
	}

	return targetIndex
}

// SwapWindowLeft swaps the focused window with the window to its left
func (m *OS) SwapWindowLeft() {
	m.SwapWindow(DirLeft)
}

// SwapWindowRight swaps the focused window with the window to its right
func (m *OS) SwapWindowRight() {
	m.SwapWindow(DirRight)
}

// SwapWindowUp swaps the focused window with the window above it
func (m *OS) SwapWindowUp() {
	m.SwapWindow(DirUp)
}

// SwapWindowDown swaps the focused window with the window below it
func (m *OS) SwapWindowDown() {
	m.SwapWindow(DirDown)
}

// SwapWindowsByID swaps two panes by window id and leaves the focus on the
// first, at the place the second held. It is herdr's pane.swap, which the
// daemon routes here because the split tree lives in the client: the daemon
// picks the pair, and the client moves them.
func (m *OS) SwapWindowsByID(source, target string) error {
	si, ti := m.windowIndexByID(source), m.windowIndexByID(target)
	if si < 0 {
		return fmt.Errorf("no pane %s to swap", source)
	}
	if ti < 0 {
		return fmt.Errorf("no pane %s to swap", target)
	}
	if si == ti {
		return nil
	}
	// A pane opened a moment ago may still be sliding to its tile. The swap
	// trades the rectangles the layout gave, not the in-between ones a slide
	// has on this frame (see swapWindows), so the client draws the order it
	// tells the daemon.
	m.swapWindows(si, ti, m.Settings.GetFastAnimationDuration())
	m.FocusWindow(ti)
	return nil
}
