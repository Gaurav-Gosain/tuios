package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// ResizeMasterWidth adjusts the master window width ratio in tiling mode
func (m *OS) ResizeMasterWidth(delta float64) {
	if !m.AutoTiling {
		return
	}

	// Adjust ratio
	m.MasterRatio += delta

	// Clamp between 0.3 and 0.7 (30% to 70%)
	if m.MasterRatio < 0.3 {
		m.MasterRatio = 0.3
	} else if m.MasterRatio > 0.7 {
		m.MasterRatio = 0.7
	}

	// Retile all windows with new ratio
	m.TileAllWindows()
}

// ResizeFocusedWindowHeight resizes the focused window's height by moving the BOTTOM edge
// delta is in pixels (positive = grow, negative = shrink)
func (m *OS) ResizeFocusedWindowHeight(deltaPixels int) {
	if !m.AutoTiling || m.FocusedWindow < 0 || m.FocusedWindow >= len(m.Windows) {
		return
	}

	focusedWindow := m.Windows[m.FocusedWindow]
	if focusedWindow.Workspace != m.CurrentWorkspace || focusedWindow.Minimized {
		return
	}

	// Block resizing if bottom edge is at screen boundary
	maxY := m.GetUsableHeight()
	atBottomEdge := (focusedWindow.Y + focusedWindow.Height) >= (maxY - edgeTolerance)
	if atBottomEdge {
		return // Can't resize bottom edge when it's at the screen edge
	}

	// Calculate new dimensions (bottom edge moves)
	newX := focusedWindow.X
	newY := focusedWindow.Y
	newWidth := focusedWindow.Width
	newHeight := focusedWindow.Height + deltaPixels

	// Call the shared tiling adjustment logic
	m.AdjustTilingNeighbors(focusedWindow, newX, newY, newWidth, newHeight)
}

// ResizeFocusedWindowWidth resizes the focused window's width by moving the RIGHT edge
// delta is in pixels (positive = grow right, negative = shrink left)
func (m *OS) ResizeFocusedWindowWidth(deltaPixels int) {
	if !m.AutoTiling || m.FocusedWindow < 0 || m.FocusedWindow >= len(m.Windows) {
		return
	}

	focusedWindow := m.Windows[m.FocusedWindow]
	if focusedWindow.Workspace != m.CurrentWorkspace || focusedWindow.Minimized {
		return
	}

	// In scrolling mode, change the column's fixed width
	if m.UseScrollingLayout {
		m.scrollingResizeColumn(deltaPixels)
		return
	}

	// Block resizing if right edge is at screen boundary
	atRightEdge := (focusedWindow.X + focusedWindow.Width) >= (m.GetRenderWidth() - edgeTolerance)
	if atRightEdge {
		return
	}

	// Calculate new dimensions (right edge moves)
	newX := focusedWindow.X
	newY := focusedWindow.Y
	newWidth := focusedWindow.Width + deltaPixels
	newHeight := focusedWindow.Height

	// Call the shared tiling adjustment logic
	m.AdjustTilingNeighbors(focusedWindow, newX, newY, newWidth, newHeight)
}

// ResizeFocusedWindowWidthLeft resizes the focused window's width by moving the LEFT edge
// delta is in pixels (positive = shrink from left, negative = grow from left)
func (m *OS) ResizeFocusedWindowWidthLeft(deltaPixels int) {
	if !m.AutoTiling || m.FocusedWindow < 0 || m.FocusedWindow >= len(m.Windows) {
		return
	}

	focusedWindow := m.Windows[m.FocusedWindow]
	if focusedWindow.Workspace != m.CurrentWorkspace || focusedWindow.Minimized {
		return
	}

	// In scrolling mode, change the column's fixed width
	if m.UseScrollingLayout {
		m.scrollingResizeColumn(-deltaPixels)
		return
	}

	// Block resizing if left edge is at screen boundary
	atLeftEdge := focusedWindow.X <= edgeTolerance
	if atLeftEdge {
		return
	}

	// Calculate new dimensions (left edge moves)
	newX := focusedWindow.X + deltaPixels
	newY := focusedWindow.Y
	newWidth := focusedWindow.Width - deltaPixels
	newHeight := focusedWindow.Height

	// Call the shared tiling adjustment logic
	m.AdjustTilingNeighbors(focusedWindow, newX, newY, newWidth, newHeight)
}

// ResizeFocusedWindowHeightTop resizes the focused window's height by moving the TOP edge
// delta is in pixels (positive = shrink from top, negative = grow from top)
func (m *OS) ResizeFocusedWindowHeightTop(deltaPixels int) {
	if !m.AutoTiling || m.FocusedWindow < 0 || m.FocusedWindow >= len(m.Windows) {
		return
	}

	focusedWindow := m.Windows[m.FocusedWindow]
	if focusedWindow.Workspace != m.CurrentWorkspace || focusedWindow.Minimized {
		return
	}

	// Block resizing if top edge is at screen boundary
	atTopEdge := focusedWindow.Y <= edgeTolerance
	if atTopEdge {
		return // Can't resize top edge when it's at the screen edge
	}

	// Calculate new dimensions (top edge moves)
	newX := focusedWindow.X
	newY := focusedWindow.Y + deltaPixels
	newWidth := focusedWindow.Width
	newHeight := focusedWindow.Height - deltaPixels // Height decreases when Y increases

	// Call the shared tiling adjustment logic
	m.AdjustTilingNeighbors(focusedWindow, newX, newY, newWidth, newHeight)
}

// resizeOp defines how a window should be resized during tiling adjustments
type resizeOp func(m *OS, win *terminal.Window, width, height int)

// resizeImmediate performs an immediate resize with PTY update
func resizeImmediate(_ *OS, win *terminal.Window, width, height int) {
	win.Resize(width, height)
}

// resizeVisual performs a visual-only resize, deferring PTY update
func resizeVisual(m *OS, win *terminal.Window, width, height int) {
	win.ResizeVisual(width, height)
	win.IsBeingManipulated = true
	m.PendingResizes[win.ID] = [2]int{width, height}
}

// adjustTilingNeighborsGeneric is the core tiling resize algorithm.
// It adjusts ALL windows on affected split lines with constraint-based positioning.
// The resize parameter controls whether to use immediate or visual-only resize.
func (m *OS) adjustTilingNeighborsGeneric(resized *terminal.Window, newX, newY, newWidth, newHeight int, resize resizeOp) (finalX, finalY, finalRight, finalBottom int) {
	oldX := resized.X
	oldY := resized.Y
	oldRight := resized.X + resized.Width
	oldBottom := resized.Y + resized.Height
	newRight := newX + newWidth
	newBottom := newY + newHeight

	const minWidth = config.DefaultWindowWidth
	const minHeight = config.DefaultWindowHeight
	minY := m.GetTopMargin()
	maxY := minY + m.GetUsableHeight()
	renderWidth := m.GetRenderWidth()

	// Handle right edge movement (vertical split line)
	if newRight != oldRight {
		leftWindows, rightWindows := findWindowsOnVerticalSplitAll(m, oldRight)
		leftWindows = removeWindowFromList(leftWindows, resized)
		rightWindows = removeWindowFromList(rightWindows, resized)

		constrainedRight := m.constrainVerticalSplit(newRight, leftWindows, rightWindows, minWidth, renderWidth)

		for _, win := range leftWindows {
			resize(m, win, constrainedRight-win.X, win.Height)
			win.MarkPositionDirty()
		}
		for _, win := range rightWindows {
			oldWinRight := win.X + win.Width
			win.X = constrainedRight
			resize(m, win, oldWinRight-constrainedRight, win.Height)
			win.MarkPositionDirty()
		}

		newRight = constrainedRight
	}

	// Handle left edge movement (vertical split line)
	if newX != oldX {
		leftWindows, rightWindows := findWindowsOnVerticalSplitAll(m, oldX)
		leftWindows = removeWindowFromList(leftWindows, resized)
		rightWindows = removeWindowFromList(rightWindows, resized)

		constrainedX := m.constrainVerticalSplit(newX, leftWindows, rightWindows, minWidth, renderWidth)

		for _, win := range leftWindows {
			resize(m, win, constrainedX-win.X, win.Height)
			win.MarkPositionDirty()
		}
		for _, win := range rightWindows {
			oldWinRight := win.X + win.Width
			win.X = constrainedX
			resize(m, win, oldWinRight-constrainedX, win.Height)
			win.MarkPositionDirty()
		}

		newX = constrainedX
	}

	// Handle bottom edge movement (horizontal split line)
	if newBottom != oldBottom {
		topWindows, bottomWindows := findWindowsOnHorizontalSplitAll(m, oldBottom)
		topWindows = removeWindowFromList(topWindows, resized)
		bottomWindows = removeWindowFromList(bottomWindows, resized)

		constrainedBottom := m.constrainHorizontalSplit(newBottom, topWindows, bottomWindows, minHeight, minY, maxY)

		for _, win := range topWindows {
			resize(m, win, win.Width, constrainedBottom-win.Y)
			win.MarkPositionDirty()
		}
		for _, win := range bottomWindows {
			oldWinBottom := win.Y + win.Height
			win.Y = constrainedBottom
			resize(m, win, win.Width, oldWinBottom-constrainedBottom)
			win.MarkPositionDirty()
		}

		newBottom = constrainedBottom
	}

	// Handle top edge movement (horizontal split line)
	if newY != oldY {
		topWindows, bottomWindows := findWindowsOnHorizontalSplitAll(m, oldY)
		topWindows = removeWindowFromList(topWindows, resized)
		bottomWindows = removeWindowFromList(bottomWindows, resized)

		constrainedY := m.constrainHorizontalSplit(newY, topWindows, bottomWindows, minHeight, minY, maxY)

		for _, win := range topWindows {
			resize(m, win, win.Width, constrainedY-win.Y)
			win.MarkPositionDirty()
		}
		for _, win := range bottomWindows {
			oldWinBottom := win.Y + win.Height
			win.Y = constrainedY
			resize(m, win, win.Width, oldWinBottom-constrainedY)
			win.MarkPositionDirty()
		}

		newY = constrainedY
	}

	return newX, newY, newRight, newBottom
}

// constrainVerticalSplit calculates the valid position for a vertical split line
func (m *OS) constrainVerticalSplit(requested int, leftWindows, rightWindows []*terminal.Window, minWidth, maxX int) int {
	minValidX := 0
	for _, win := range leftWindows {
		minRequired := win.X + minWidth
		if minRequired > minValidX {
			minValidX = minRequired
		}
	}

	maxValidX := maxX
	for _, win := range rightWindows {
		maxAllowed := win.X + win.Width - minWidth
		if maxAllowed < maxValidX {
			maxValidX = maxAllowed
		}
	}

	return max(minValidX, min(requested, maxValidX))
}

// constrainHorizontalSplit calculates the valid position for a horizontal split line
func (m *OS) constrainHorizontalSplit(requested int, topWindows, bottomWindows []*terminal.Window, minHeight, minY, maxY int) int {
	minValidY := minY
	for _, win := range topWindows {
		minRequired := win.Y + minHeight
		if minRequired > minValidY {
			minValidY = minRequired
		}
	}

	maxValidY := maxY
	for _, win := range bottomWindows {
		maxAllowed := win.Y + win.Height - minHeight
		if maxAllowed < maxValidY {
			maxValidY = maxAllowed
		}
	}

	return max(minValidY, min(requested, maxValidY))
}

// applyTilingResult updates the resized window with constrained values from adjustTilingNeighborsGeneric
// and validates that the dimensions remain within bounds, clamping as a last resort.
func (m *OS) applyTilingResult(resized *terminal.Window, finalX, finalY, finalRight, finalBottom int) {
	const minWidth = config.DefaultWindowWidth
	const minHeight = config.DefaultWindowHeight
	minY := m.GetTopMargin()
	maxY := minY + m.GetUsableHeight()
	renderWidth := m.GetRenderWidth()

	resized.X = finalX
	resized.Y = finalY
	resized.Width = finalRight - finalX
	resized.Height = finalBottom - finalY

	// Fallback clamp if constraint calculation produced invalid values
	if resized.Width < minWidth || resized.Height < minHeight ||
		resized.X < 0 || resized.Y < 0 ||
		resized.X+resized.Width > renderWidth || resized.Y+resized.Height > maxY {
		resized.Width = max(minWidth, min(resized.Width, renderWidth-resized.X))
		resized.Height = max(minHeight, min(resized.Height, maxY-resized.Y))
		resized.X = max(0, min(resized.X, renderWidth-minWidth))
		resized.Y = max(minY, min(resized.Y, maxY-minHeight))
	}
}

// AdjustTilingNeighbors adjusts ALL windows on affected split lines with constraint-based positioning.
// This is the core tiling resize algorithm used by both mouse and keyboard resize operations.
func (m *OS) AdjustTilingNeighbors(resized *terminal.Window, newX, newY, newWidth, newHeight int) {
	finalX, finalY, finalRight, finalBottom := m.adjustTilingNeighborsGeneric(resized, newX, newY, newWidth, newHeight, resizeImmediate)
	m.applyTilingResult(resized, finalX, finalY, finalRight, finalBottom)

	resized.Resize(resized.Width, resized.Height)
	resized.MarkPositionDirty()
	m.MarkLayoutCustom()

	// This is the keyboard resize path, where every press is a finished resize.
	// The mouse path goes through AdjustTilingNeighborsVisual and announces
	// itself once on release instead of once per motion event.
	m.FireResized(resized)
}

// AdjustTilingNeighborsVisual is like AdjustTilingNeighbors but uses visual-only resize.
// This defers PTY resize operations until the drag completes, improving responsiveness
// during mouse resize operations while still constraining window sizes appropriately.
func (m *OS) AdjustTilingNeighborsVisual(resized *terminal.Window, newX, newY, newWidth, newHeight int) {
	finalX, finalY, finalRight, finalBottom := m.adjustTilingNeighborsGeneric(resized, newX, newY, newWidth, newHeight, resizeVisual)
	m.applyTilingResult(resized, finalX, finalY, finalRight, finalBottom)

	resized.ResizeVisual(resized.Width, resized.Height)
	m.PendingResizes[resized.ID] = [2]int{resized.Width, resized.Height}
	resized.MarkPositionDirty()
}

// findWindowsOnVerticalSplitAll finds all windows on a vertical split line (not excluding any window)
func findWindowsOnVerticalSplitAll(m *OS, splitX int) (leftWindows, rightWindows []*terminal.Window) {
	const tolerance = 1

	for _, win := range m.Windows {
		if win.Workspace != m.CurrentWorkspace || win.Minimized {
			continue
		}

		winRight := win.X + win.Width
		if abs(winRight-splitX) <= tolerance {
			leftWindows = append(leftWindows, win)
		} else if abs(win.X-splitX) <= tolerance {
			rightWindows = append(rightWindows, win)
		}
	}

	return leftWindows, rightWindows
}

// findWindowsOnHorizontalSplitAll finds all windows on a horizontal split line (not excluding any window)
func findWindowsOnHorizontalSplitAll(m *OS, splitY int) (topWindows, bottomWindows []*terminal.Window) {
	const tolerance = 1

	for _, win := range m.Windows {
		if win.Workspace != m.CurrentWorkspace || win.Minimized {
			continue
		}

		winBottom := win.Y + win.Height
		if abs(winBottom-splitY) <= tolerance {
			topWindows = append(topWindows, win)
		} else if abs(win.Y-splitY) <= tolerance {
			bottomWindows = append(bottomWindows, win)
		}
	}

	return topWindows, bottomWindows
}

// removeWindowFromList removes a window from a slice
func removeWindowFromList(windows []*terminal.Window, toRemove *terminal.Window) []*terminal.Window {
	result := make([]*terminal.Window, 0, len(windows))
	for _, win := range windows {
		if win != toRemove {
			result = append(result, win)
		}
	}
	return result
}

// abs returns the absolute value of an integer
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
