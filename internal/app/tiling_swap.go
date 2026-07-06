package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/ui"
)

// SwapWindowsInstant swaps the positions of two windows instantly without animation
func (m *OS) SwapWindowsInstant(index1, index2 int) {
	if index1 < 0 || index1 >= len(m.Windows) || index2 < 0 || index2 >= len(m.Windows) {
		return
	}

	window1 := m.Windows[index1]
	window2 := m.Windows[index2]

	// Store the positions for swapping
	x1, y1, w1, h1 := window1.X, window1.Y, window1.Width, window1.Height
	x2, y2, w2, h2 := window2.X, window2.Y, window2.Width, window2.Height

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

	// Now create animations - note: after slice swap, indices are swapped
	// draggedWindow is now at targetIndex, targetWindow is now at draggedIndex

	// Dragged window goes to target's original position (with animation)
	anim1 := ui.NewSnapAnimation(
		draggedWindow,
		targetX, targetY, targetW, targetH,
		config.GetFastAnimationDuration(),
	)

	// Target window goes to dragged window's ORIGINAL position (with animation)
	anim2 := ui.NewSnapAnimation(
		targetWindow,
		origX, origY, origWidth, origHeight,
		config.GetFastAnimationDuration(),
	)

	if anim1 != nil {
		m.Animations = append(m.Animations, anim1)
	}
	if anim2 != nil {
		m.Animations = append(m.Animations, anim2)
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
		if i != excludeIndex && w.Workspace == m.CurrentWorkspace && !w.Minimized && !w.Minimizing {
			visibleWindows = append(visibleWindows, w)
			visibleIndices = append(visibleIndices, i)
		}
	}

	if len(visibleWindows) == 0 {
		return
	}

	// Calculate tiling layout based on number of remaining windows
	layouts := layout.CalculateTilingLayout(len(visibleWindows), m.GetRenderWidth(), m.GetUsableHeight(), m.GetTopMargin(), m.MasterRatio)

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
			config.GetAnimationDuration(),
		)

		if anim != nil {
			m.Animations = append(m.Animations, anim)
		}
	}
}

// SwapWindow swaps the focused window with the adjacent window in the given direction
func (m *OS) SwapWindow(dir Direction) {
	if m.FocusedWindow < 0 || m.FocusedWindow >= len(m.Windows) {
		return
	}

	// Don't swap if animations are in progress
	if m.HasActiveAnimations() {
		return
	}

	focusedWindow := m.Windows[m.FocusedWindow]

	// Don't swap floating windows
	if focusedWindow.IsFloating {
		return
	}
	targetIndex := m.findAdjacentWindow(focusedWindow, dir)

	if targetIndex >= 0 {
		// Swap instantly without animation for keyboard shortcuts
		m.SwapWindowsInstant(m.FocusedWindow, targetIndex)
	}
}

// findAdjacentWindow finds the closest window in the given direction
func (m *OS) findAdjacentWindow(focused *terminal.Window, dir Direction) int {
	targetIndex := -1
	var minDistance int

	// Set initial distance based on direction (horizontal or vertical)
	if dir == DirLeft || dir == DirRight {
		minDistance = m.Width
	} else {
		minDistance = m.Height
	}

	for i, window := range m.Windows {
		if i == m.FocusedWindow || window.Workspace != m.CurrentWorkspace || window.Minimized || window.Minimizing || window.IsFloating {
			continue
		}

		var isInDirection, overlaps bool
		var distance int

		switch dir {
		case DirLeft:
			isInDirection = window.X+window.Width <= focused.X+swapTolerance
			overlaps = window.Y < focused.Y+focused.Height && window.Y+window.Height > focused.Y
			distance = focused.X - (window.X + window.Width)
		case DirRight:
			isInDirection = window.X >= focused.X+focused.Width-swapTolerance
			overlaps = window.Y < focused.Y+focused.Height && window.Y+window.Height > focused.Y
			distance = window.X - (focused.X + focused.Width)
		case DirUp:
			isInDirection = window.Y+window.Height <= focused.Y+swapTolerance
			overlaps = window.X < focused.X+focused.Width && window.X+window.Width > focused.X
			distance = focused.Y - (window.Y + window.Height)
		case DirDown:
			isInDirection = window.Y >= focused.Y+focused.Height-swapTolerance
			overlaps = window.X < focused.X+focused.Width && window.X+window.Width > focused.X
			distance = window.Y - (focused.Y + focused.Height)
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
