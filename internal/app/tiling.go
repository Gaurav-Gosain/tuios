package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/ui"
)

// tileLayout is a private type for compatibility with existing code
type tileLayout struct {
	x, y, width, height int
}

// calculateTilingLayout is a wrapper around layout.CalculateTilingLayout for internal use
func (m *OS) calculateTilingLayout(n int) []tileLayout {
	layouts := layout.CalculateTilingLayout(n, m.Width, m.GetUsableHeight(), m.GetTopMargin(), m.MasterRatio)
	result := make([]tileLayout, len(layouts))
	for i, l := range layouts {
		result[i] = tileLayout{
			x:      l.X,
			y:      l.Y,
			width:  l.Width,
			height: l.Height,
		}
	}
	return result
}

// TileAllWindows arranges all visible windows in a tiling layout
func (m *OS) TileAllWindows() {
	// Get list of visible windows in current workspace (not minimized)
	var visibleWindows []*terminal.Window
	var visibleIndices []int
	for i, w := range m.Windows {
		if w.Workspace == m.CurrentWorkspace && !w.Minimized && !w.Minimizing {
			visibleWindows = append(visibleWindows, w)
			visibleIndices = append(visibleIndices, i)
		}
	}

	if len(visibleWindows) == 0 {
		return
	}

	// Calculate tiling layout based on number of windows
	layouts := layout.CalculateTilingLayout(len(visibleWindows), m.Width, m.GetUsableHeight(), m.GetTopMargin(), m.MasterRatio)

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
			config.DefaultAnimationDuration,
		)

		if anim != nil {
			m.Animations = append(m.Animations, anim)
		}
	}
}

// ToggleAutoTiling toggles automatic tiling mode
func (m *OS) ToggleAutoTiling() {
	m.AutoTiling = !m.AutoTiling

	if m.AutoTiling {
		// When enabling, tile all existing windows
		m.TileAllWindows()
	}
}

// TileNewWindow arranges the new window in the tiling layout
func (m *OS) TileNewWindow() {
	if !m.AutoTiling {
		return
	}

	// Retile all windows including the new one
	m.TileAllWindows()
}

// RetileAfterClose handles window close in tiling mode
func (m *OS) RetileAfterClose() {
	if !m.AutoTiling {
		return
	}

	// Retile remaining windows
	m.TileAllWindows()
}

// SwapWindows swaps the positions of two windows with animation
func (m *OS) SwapWindows(index1, index2 int) {
	if index1 < 0 || index1 >= len(m.Windows) || index2 < 0 || index2 >= len(m.Windows) {
		return
	}

	window1 := m.Windows[index1]
	window2 := m.Windows[index2]

	// Store the positions for swapping
	x1, y1, width1, height1 := window1.X, window1.Y, window1.Width, window1.Height
	x2, y2, width2, height2 := window2.X, window2.Y, window2.Width, window2.Height

	// Create animations for both windows to swap positions
	anim1 := ui.NewSnapAnimation(
		window1,
		x2, y2, width2, height2,
		config.FastAnimationDuration,
	)

	anim2 := ui.NewSnapAnimation(
		window2,
		x1, y1, width1, height1,
		config.FastAnimationDuration,
	)

	m.Animations = append(m.Animations, anim1, anim2)
}

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
}

// SwapWindowsWithOriginal swaps windows where the dragged window's original position is provided
func (m *OS) SwapWindowsWithOriginal(draggedIndex, targetIndex int, origX, origY, origWidth, origHeight int) {
	if draggedIndex < 0 || draggedIndex >= len(m.Windows) || targetIndex < 0 || targetIndex >= len(m.Windows) {
		return
	}

	draggedWindow := m.Windows[draggedIndex]
	targetWindow := m.Windows[targetIndex]

	// Dragged window goes to target's position
	anim1 := ui.NewSnapAnimation(
		draggedWindow,
		targetWindow.X, targetWindow.Y, targetWindow.Width, targetWindow.Height,
		config.FastAnimationDuration,
	)

	// Target window goes to dragged window's ORIGINAL position
	anim2 := ui.NewSnapAnimation(
		targetWindow,
		origX, origY, origWidth, origHeight,
		config.FastAnimationDuration,
	)

	if anim1 != nil {
		m.Animations = append(m.Animations, anim1)
	}
	if anim2 != nil {
		m.Animations = append(m.Animations, anim2)
	}
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
	layouts := layout.CalculateTilingLayout(len(visibleWindows), m.Width, m.GetUsableHeight(), m.GetTopMargin(), m.MasterRatio)

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
			config.DefaultAnimationDuration,
		)

		if anim != nil {
			m.Animations = append(m.Animations, anim)
		}
	}
}

// SwapWindowLeft swaps the focused window with the window to its left
func (o *OS) SwapWindowLeft() {
	if o.FocusedWindow < 0 || o.FocusedWindow >= len(o.Windows) {
		return
	}

	// Don't swap if animations are in progress
	if o.HasActiveAnimations() {
		return
	}

	focusedWindow := o.Windows[o.FocusedWindow]

	// Find the window to the left in current workspace
	targetIndex := -1
	minDistance := o.Width

	for i, window := range o.Windows {
		if i == o.FocusedWindow || window.Workspace != o.CurrentWorkspace || window.Minimized || window.Minimizing {
			continue
		}

		// Check if window is to the left
		if window.X+window.Width <= focusedWindow.X+5 {
			// Check if it overlaps vertically
			if window.Y < focusedWindow.Y+focusedWindow.Height &&
				window.Y+window.Height > focusedWindow.Y {
				// Find the closest one
				distance := focusedWindow.X - (window.X + window.Width)
				if distance < minDistance {
					minDistance = distance
					targetIndex = i
				}
			}
		}
	}

	if targetIndex >= 0 {
		// Swap instantly without animation for keyboard shortcuts
		o.SwapWindowsInstant(o.FocusedWindow, targetIndex)
	}
}

// SwapWindowRight swaps the focused window with the window to its right
func (o *OS) SwapWindowRight() {
	if o.FocusedWindow < 0 || o.FocusedWindow >= len(o.Windows) {
		return
	}

	// Don't swap if animations are in progress
	if o.HasActiveAnimations() {
		return
	}

	focusedWindow := o.Windows[o.FocusedWindow]

	// Find the window to the right in current workspace
	targetIndex := -1
	minDistance := o.Width

	for i, window := range o.Windows {
		if i == o.FocusedWindow || window.Workspace != o.CurrentWorkspace || window.Minimized || window.Minimizing {
			continue
		}

		// Check if window is to the right
		if window.X >= focusedWindow.X+focusedWindow.Width-5 {
			// Check if it overlaps vertically
			if window.Y < focusedWindow.Y+focusedWindow.Height &&
				window.Y+window.Height > focusedWindow.Y {
				// Find the closest one
				distance := window.X - (focusedWindow.X + focusedWindow.Width)
				if distance < minDistance {
					minDistance = distance
					targetIndex = i
				}
			}
		}
	}

	if targetIndex >= 0 {
		// Swap instantly without animation for keyboard shortcuts
		o.SwapWindowsInstant(o.FocusedWindow, targetIndex)
	}
}

// SwapWindowUp swaps the focused window with the window above it
func (o *OS) SwapWindowUp() {
	if o.FocusedWindow < 0 || o.FocusedWindow >= len(o.Windows) {
		return
	}

	// Don't swap if animations are in progress
	if o.HasActiveAnimations() {
		return
	}

	focusedWindow := o.Windows[o.FocusedWindow]

	// Find the window above in current workspace
	targetIndex := -1
	minDistance := o.Height

	for i, window := range o.Windows {
		if i == o.FocusedWindow || window.Workspace != o.CurrentWorkspace || window.Minimized || window.Minimizing {
			continue
		}

		// Check if window is above
		if window.Y+window.Height <= focusedWindow.Y+5 {
			// Check if it overlaps horizontally
			if window.X < focusedWindow.X+focusedWindow.Width &&
				window.X+window.Width > focusedWindow.X {
				// Find the closest one
				distance := focusedWindow.Y - (window.Y + window.Height)
				if distance < minDistance {
					minDistance = distance
					targetIndex = i
				}
			}
		}
	}

	if targetIndex >= 0 {
		// Swap instantly without animation for keyboard shortcuts
		o.SwapWindowsInstant(o.FocusedWindow, targetIndex)
	}
}

// SwapWindowDown swaps the focused window with the window below it
func (o *OS) SwapWindowDown() {
	if o.FocusedWindow < 0 || o.FocusedWindow >= len(o.Windows) {
		return
	}

	// Don't swap if animations are in progress
	if o.HasActiveAnimations() {
		return
	}

	focusedWindow := o.Windows[o.FocusedWindow]

	// Find the window below in current workspace
	targetIndex := -1
	minDistance := o.Height

	for i, window := range o.Windows {
		if i == o.FocusedWindow || window.Workspace != o.CurrentWorkspace || window.Minimized || window.Minimizing {
			continue
		}

		// Check if window is below
		if window.Y >= focusedWindow.Y+focusedWindow.Height-5 {
			// Check if it overlaps horizontally
			if window.X < focusedWindow.X+focusedWindow.Width &&
				window.X+window.Width > focusedWindow.X {
				// Find the closest one
				distance := window.Y - (focusedWindow.Y + focusedWindow.Height)
				if distance < minDistance {
					minDistance = distance
					targetIndex = i
				}
			}
		}
	}

	if targetIndex >= 0 {
		// Swap instantly without animation for keyboard shortcuts
		o.SwapWindowsInstant(o.FocusedWindow, targetIndex)
	}
}

// ResizeMasterWidth adjusts the master window width ratio in tiling mode
func (o *OS) ResizeMasterWidth(delta float64) {
	if !o.AutoTiling {
		return
	}

	// Adjust ratio
	o.MasterRatio += delta

	// Clamp between 0.3 and 0.7 (30% to 70%)
	if o.MasterRatio < 0.3 {
		o.MasterRatio = 0.3
	} else if o.MasterRatio > 0.7 {
		o.MasterRatio = 0.7
	}

	// Retile all windows with new ratio
	o.TileAllWindows()
}

// SaveCurrentLayout saves the current window layout for the active workspace
func (o *OS) SaveCurrentLayout() {
	if !o.AutoTiling {
		return
	}

	layouts := make([]WindowLayout, 0, len(o.Windows))
	for _, win := range o.Windows {
		if win.Workspace == o.CurrentWorkspace && !win.Minimized {
			layouts = append(layouts, WindowLayout{
				WindowID: win.ID,
				X:        win.X,
				Y:        win.Y,
				Width:    win.Width,
				Height:   win.Height,
			})
		}
	}

	o.WorkspaceLayouts[o.CurrentWorkspace] = layouts
	o.WorkspaceMasterRatio[o.CurrentWorkspace] = o.MasterRatio
}

// RestoreWorkspaceLayout restores saved layout when switching to a workspace
func (o *OS) RestoreWorkspaceLayout(workspace int) {
	if !o.AutoTiling {
		return
	}

	// Restore master ratio for this workspace (or use default)
	if ratio, exists := o.WorkspaceMasterRatio[workspace]; exists {
		o.MasterRatio = ratio
	} else {
		o.MasterRatio = 0.5 // Default
	}

	// Check if we have a saved layout for this workspace
	savedLayouts, hasCustom := o.WorkspaceLayouts[workspace]
	if !hasCustom || len(savedLayouts) == 0 {
		// No custom layout - use default tiling
		o.WorkspaceHasCustom[workspace] = false
		return
	}

	// Apply saved layout
	for _, saved := range savedLayouts {
		// Find window by ID
		for _, win := range o.Windows {
			if win.ID == saved.WindowID && win.Workspace == workspace {
				// Restore saved position/size
				win.X = saved.X
				win.Y = saved.Y
				win.Width = saved.Width
				win.Height = saved.Height
				win.Resize(win.Width, win.Height)
				win.MarkPositionDirty()
				break
			}
		}
	}

	o.WorkspaceHasCustom[workspace] = true
}

// MarkLayoutCustom marks the current workspace as having a custom layout
func (o *OS) MarkLayoutCustom() {
	if o.AutoTiling {
		o.WorkspaceHasCustom[o.CurrentWorkspace] = true
		o.SaveCurrentLayout()
	}
}

// ResizeFocusedWindowHeight resizes the focused window's height by moving the BOTTOM edge
// delta is in pixels (positive = grow, negative = shrink)
func (o *OS) ResizeFocusedWindowHeight(deltaPixels int) {
	if !o.AutoTiling || o.FocusedWindow < 0 || o.FocusedWindow >= len(o.Windows) {
		return
	}

	focusedWindow := o.Windows[o.FocusedWindow]
	if focusedWindow.Workspace != o.CurrentWorkspace || focusedWindow.Minimized {
		return
	}

	// Block resizing if bottom edge is at screen boundary
	const edgeTolerance = 2
	maxY := o.GetUsableHeight()
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
	o.AdjustTilingNeighbors(focusedWindow, newX, newY, newWidth, newHeight)
}

// ResizeFocusedWindowWidth resizes the focused window's width by moving the RIGHT edge
// delta is in pixels (positive = grow right, negative = shrink left)
func (o *OS) ResizeFocusedWindowWidth(deltaPixels int) {
	if !o.AutoTiling || o.FocusedWindow < 0 || o.FocusedWindow >= len(o.Windows) {
		return
	}

	focusedWindow := o.Windows[o.FocusedWindow]
	if focusedWindow.Workspace != o.CurrentWorkspace || focusedWindow.Minimized {
		return
	}

	// Block resizing if right edge is at screen boundary
	const edgeTolerance = 2
	atRightEdge := (focusedWindow.X + focusedWindow.Width) >= (o.Width - edgeTolerance)
	if atRightEdge {
		return // Can't resize right edge when it's at the screen edge
	}

	// Calculate new dimensions (right edge moves)
	newX := focusedWindow.X
	newY := focusedWindow.Y
	newWidth := focusedWindow.Width + deltaPixels
	newHeight := focusedWindow.Height

	// Call the shared tiling adjustment logic
	o.AdjustTilingNeighbors(focusedWindow, newX, newY, newWidth, newHeight)
}

// ResizeFocusedWindowWidthLeft resizes the focused window's width by moving the LEFT edge
// delta is in pixels (positive = shrink from left, negative = grow from left)
func (o *OS) ResizeFocusedWindowWidthLeft(deltaPixels int) {
	if !o.AutoTiling || o.FocusedWindow < 0 || o.FocusedWindow >= len(o.Windows) {
		return
	}

	focusedWindow := o.Windows[o.FocusedWindow]
	if focusedWindow.Workspace != o.CurrentWorkspace || focusedWindow.Minimized {
		return
	}

	// Block resizing if left edge is at screen boundary
	const edgeTolerance = 2
	atLeftEdge := focusedWindow.X <= edgeTolerance
	if atLeftEdge {
		return // Can't resize left edge when it's at the screen edge
	}

	// Calculate new dimensions (left edge moves)
	newX := focusedWindow.X + deltaPixels
	newY := focusedWindow.Y
	newWidth := focusedWindow.Width - deltaPixels // Width decreases when X increases
	newHeight := focusedWindow.Height

	// Call the shared tiling adjustment logic
	o.AdjustTilingNeighbors(focusedWindow, newX, newY, newWidth, newHeight)
}

// ResizeFocusedWindowHeightTop resizes the focused window's height by moving the TOP edge
// delta is in pixels (positive = shrink from top, negative = grow from top)
func (o *OS) ResizeFocusedWindowHeightTop(deltaPixels int) {
	if !o.AutoTiling || o.FocusedWindow < 0 || o.FocusedWindow >= len(o.Windows) {
		return
	}

	focusedWindow := o.Windows[o.FocusedWindow]
	if focusedWindow.Workspace != o.CurrentWorkspace || focusedWindow.Minimized {
		return
	}

	// Block resizing if top edge is at screen boundary
	const edgeTolerance = 2
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
	o.AdjustTilingNeighbors(focusedWindow, newX, newY, newWidth, newHeight)
}

// AdjustTilingNeighbors adjusts ALL windows on affected split lines with constraint-based positioning
// This is the core tiling resize algorithm used by both mouse and keyboard resize operations
func (o *OS) AdjustTilingNeighbors(resized *terminal.Window, newX, newY, newWidth, newHeight int) {
	oldX := resized.X
	oldY := resized.Y
	oldRight := resized.X + resized.Width
	oldBottom := resized.Y + resized.Height
	newRight := newX + newWidth
	newBottom := newY + newHeight

	const minWidth = config.DefaultWindowWidth
	const minHeight = config.DefaultWindowHeight
	minY := o.GetTopMargin()
	maxY := minY + o.GetUsableHeight()

	// Handle right edge movement (vertical split line)
	if newRight != oldRight {
		// Find all windows on this split line
		leftWindows, rightWindows := findWindowsOnVerticalSplitAll(o, oldRight)

		// Remove resized window from the lists if present
		leftWindows = removeWindowFromList(leftWindows, resized)
		rightWindows = removeWindowFromList(rightWindows, resized)

		// Calculate the valid range for the split line position
		// This MUST ensure all affected windows can maintain minimum width
		constrainedRight := newRight

		// Find minimum valid X (leftmost position split can move to)
		minValidX := 0
		for _, win := range leftWindows {
			// Left windows need: constrainedRight >= win.X + minWidth
			minRequired := win.X + minWidth
			if minRequired > minValidX {
				minValidX = minRequired
			}
		}

		// Find maximum valid X (rightmost position split can move to)
		maxValidX := o.Width
		for _, win := range rightWindows {
			// Right windows need: constrainedRight <= (win.X + win.Width) - minWidth
			maxAllowed := win.X + win.Width - minWidth
			if maxAllowed < maxValidX {
				maxValidX = maxAllowed
			}
		}

		// If no left windows, split can go to left screen edge
		// If no right windows, split can go to right screen edge
		// Clamp requested position to valid range
		constrainedRight = max(minValidX, min(constrainedRight, maxValidX))

		// Apply constrained position to all windows - NO CLAMPING!
		// This maintains exact adjacency: leftWindow.Right == rightWindow.Left == constrainedRight
		for _, win := range leftWindows {
			win.Width = constrainedRight - win.X
			win.Resize(win.Width, win.Height)
			win.MarkPositionDirty()
		}
		for _, win := range rightWindows {
			oldWinRight := win.X + win.Width
			win.X = constrainedRight
			win.Width = oldWinRight - constrainedRight
			win.Resize(win.Width, win.Height)
			win.MarkPositionDirty()
		}

		// Update newRight to constrained value
		newRight = constrainedRight
	}

	// Handle left edge movement (vertical split line)
	if newX != oldX {
		// Find all windows on this split line
		leftWindows, rightWindows := findWindowsOnVerticalSplitAll(o, oldX)

		// Remove resized window from the lists if present
		leftWindows = removeWindowFromList(leftWindows, resized)
		rightWindows = removeWindowFromList(rightWindows, resized)

		// Calculate the valid range for the split line position
		constrainedX := newX

		// Find minimum valid X
		minValidX := 0
		for _, win := range leftWindows {
			minRequired := win.X + minWidth
			if minRequired > minValidX {
				minValidX = minRequired
			}
		}

		// Find maximum valid X
		maxValidX := o.Width
		for _, win := range rightWindows {
			maxAllowed := win.X + win.Width - minWidth
			if maxAllowed < maxValidX {
				maxValidX = maxAllowed
			}
		}

		// Clamp requested position to valid range
		constrainedX = max(minValidX, min(constrainedX, maxValidX))

		// Apply constrained position - NO CLAMPING!
		for _, win := range leftWindows {
			win.Width = constrainedX - win.X
			win.Resize(win.Width, win.Height)
			win.MarkPositionDirty()
		}
		for _, win := range rightWindows {
			oldWinRight := win.X + win.Width
			win.X = constrainedX
			win.Width = oldWinRight - constrainedX
			win.Resize(win.Width, win.Height)
			win.MarkPositionDirty()
		}

		// Update newX to constrained value
		newX = constrainedX
	}

	// Handle bottom edge movement (horizontal split line)
	if newBottom != oldBottom {
		// Find all windows on this split line
		topWindows, bottomWindows := findWindowsOnHorizontalSplitAll(o, oldBottom)

		// Remove resized window from the lists if present
		topWindows = removeWindowFromList(topWindows, resized)
		bottomWindows = removeWindowFromList(bottomWindows, resized)

		// Calculate the valid range for the split line position
		constrainedBottom := newBottom

		// Find minimum valid Y
		minValidY := minY
		for _, win := range topWindows {
			minRequired := win.Y + minHeight
			if minRequired > minValidY {
				minValidY = minRequired
			}
		}

		// Find maximum valid Y
		maxValidY := maxY
		for _, win := range bottomWindows {
			maxAllowed := win.Y + win.Height - minHeight
			if maxAllowed < maxValidY {
				maxValidY = maxAllowed
			}
		}

		// Clamp requested position to valid range
		constrainedBottom = max(minValidY, min(constrainedBottom, maxValidY))

		// Apply constrained position - NO CLAMPING!
		for _, win := range topWindows {
			win.Height = constrainedBottom - win.Y
			win.Resize(win.Width, win.Height)
			win.MarkPositionDirty()
		}
		for _, win := range bottomWindows {
			oldWinBottom := win.Y + win.Height
			win.Y = constrainedBottom
			win.Height = oldWinBottom - constrainedBottom
			win.Resize(win.Width, win.Height)
			win.MarkPositionDirty()
		}

		// Update newBottom to constrained value
		newBottom = constrainedBottom
	}

	// Handle top edge movement (horizontal split line)
	if newY != oldY {
		// Find all windows on this split line
		topWindows, bottomWindows := findWindowsOnHorizontalSplitAll(o, oldY)

		// Remove resized window from the lists if present
		topWindows = removeWindowFromList(topWindows, resized)
		bottomWindows = removeWindowFromList(bottomWindows, resized)

		// Calculate the valid range for the split line position
		constrainedY := newY

		// Find minimum valid Y
		minValidY := minY
		for _, win := range topWindows {
			minRequired := win.Y + minHeight
			if minRequired > minValidY {
				minValidY = minRequired
			}
		}

		// Find maximum valid Y
		maxValidY := maxY
		for _, win := range bottomWindows {
			maxAllowed := win.Y + win.Height - minHeight
			if maxAllowed < maxValidY {
				maxValidY = maxAllowed
			}
		}

		// Clamp requested position to valid range
		constrainedY = max(minValidY, min(constrainedY, maxValidY))

		// Apply constrained position - NO CLAMPING!
		for _, win := range topWindows {
			win.Height = constrainedY - win.Y
			win.Resize(win.Width, win.Height)
			win.MarkPositionDirty()
		}
		for _, win := range bottomWindows {
			oldWinBottom := win.Y + win.Height
			win.Y = constrainedY
			win.Height = oldWinBottom - constrainedY
			win.Resize(win.Width, win.Height)
			win.MarkPositionDirty()
		}

		// Update newY to constrained value
		newY = constrainedY
	}

	// Update the resized window with constrained values - NO CLAMPING!
	resized.X = newX
	resized.Y = newY
	resized.Width = newRight - newX
	resized.Height = newBottom - newY

	// Final validation: ensure dimensions are valid (should NEVER fail if constraint calculation is correct)
	if resized.Width < minWidth || resized.Height < minHeight ||
		resized.X < 0 || resized.Y < 0 ||
		resized.X+resized.Width > o.Width || resized.Y+resized.Height > maxY {
		// Constraint calculation failed - clamp as last resort to prevent panic
		resized.Width = max(minWidth, min(resized.Width, o.Width-resized.X))
		resized.Height = max(minHeight, min(resized.Height, maxY-resized.Y))
		resized.X = max(0, min(resized.X, o.Width-minWidth))
		resized.Y = max(minY, min(resized.Y, maxY-minHeight))
	}

	resized.Resize(resized.Width, resized.Height)
	resized.MarkPositionDirty()

	// Mark layout as custom
	o.MarkLayoutCustom()
}

// AdjustTilingNeighborsVisual is like AdjustTilingNeighbors but uses visual-only resize.
// This defers PTY resize operations until the drag completes, improving responsiveness
// during mouse resize operations while still constraining window sizes appropriately.
func (o *OS) AdjustTilingNeighborsVisual(resized *terminal.Window, newX, newY, newWidth, newHeight int) {
	oldX := resized.X
	oldY := resized.Y
	oldRight := resized.X + resized.Width
	oldBottom := resized.Y + resized.Height
	newRight := newX + newWidth
	newBottom := newY + newHeight

	const minWidth = config.DefaultWindowWidth
	const minHeight = config.DefaultWindowHeight
	minY := o.GetTopMargin()
	maxY := minY + o.GetUsableHeight()

	// Handle right edge movement (vertical split line)
	if newRight != oldRight {
		leftWindows, rightWindows := findWindowsOnVerticalSplitAll(o, oldRight)
		leftWindows = removeWindowFromList(leftWindows, resized)
		rightWindows = removeWindowFromList(rightWindows, resized)

		constrainedRight := newRight
		minValidX := 0
		for _, win := range leftWindows {
			minRequired := win.X + minWidth
			if minRequired > minValidX {
				minValidX = minRequired
			}
		}

		maxValidX := o.Width
		for _, win := range rightWindows {
			maxAllowed := win.X + win.Width - minWidth
			if maxAllowed < maxValidX {
				maxValidX = maxAllowed
			}
		}

		constrainedRight = max(minValidX, min(constrainedRight, maxValidX))

		for _, win := range leftWindows {
			win.ResizeVisual(constrainedRight-win.X, win.Height)
			win.MarkPositionDirty()
			win.IsBeingManipulated = true // Show resize indicator for neighbor windows
			o.PendingResizes[win.ID] = [2]int{constrainedRight - win.X, win.Height}
		}
		for _, win := range rightWindows {
			oldWinRight := win.X + win.Width
			win.X = constrainedRight
			win.ResizeVisual(oldWinRight-constrainedRight, win.Height)
			win.MarkPositionDirty()
			win.IsBeingManipulated = true // Show resize indicator for neighbor windows
			o.PendingResizes[win.ID] = [2]int{oldWinRight - constrainedRight, win.Height}
		}

		newRight = constrainedRight
	}

	// Handle left edge movement (vertical split line)
	if newX != oldX {
		leftWindows, rightWindows := findWindowsOnVerticalSplitAll(o, oldX)
		leftWindows = removeWindowFromList(leftWindows, resized)
		rightWindows = removeWindowFromList(rightWindows, resized)

		constrainedX := newX
		minValidX := 0
		for _, win := range leftWindows {
			minRequired := win.X + minWidth
			if minRequired > minValidX {
				minValidX = minRequired
			}
		}

		maxValidX := o.Width
		for _, win := range rightWindows {
			maxAllowed := win.X + win.Width - minWidth
			if maxAllowed < maxValidX {
				maxValidX = maxAllowed
			}
		}

		constrainedX = max(minValidX, min(constrainedX, maxValidX))

		for _, win := range leftWindows {
			win.ResizeVisual(constrainedX-win.X, win.Height)
			win.MarkPositionDirty()
			win.IsBeingManipulated = true // Show resize indicator for neighbor windows
			o.PendingResizes[win.ID] = [2]int{constrainedX - win.X, win.Height}
		}
		for _, win := range rightWindows {
			oldWinRight := win.X + win.Width
			win.X = constrainedX
			win.ResizeVisual(oldWinRight-constrainedX, win.Height)
			win.MarkPositionDirty()
			win.IsBeingManipulated = true // Show resize indicator for neighbor windows
			o.PendingResizes[win.ID] = [2]int{oldWinRight - constrainedX, win.Height}
		}

		newX = constrainedX
	}

	// Handle bottom edge movement (horizontal split line)
	if newBottom != oldBottom {
		topWindows, bottomWindows := findWindowsOnHorizontalSplitAll(o, oldBottom)
		topWindows = removeWindowFromList(topWindows, resized)
		bottomWindows = removeWindowFromList(bottomWindows, resized)

		constrainedBottom := newBottom
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

		constrainedBottom = max(minValidY, min(constrainedBottom, maxValidY))

		for _, win := range topWindows {
			win.ResizeVisual(win.Width, constrainedBottom-win.Y)
			win.MarkPositionDirty()
			win.IsBeingManipulated = true // Show resize indicator for neighbor windows
			o.PendingResizes[win.ID] = [2]int{win.Width, constrainedBottom - win.Y}
		}
		for _, win := range bottomWindows {
			oldWinBottom := win.Y + win.Height
			win.Y = constrainedBottom
			win.ResizeVisual(win.Width, oldWinBottom-constrainedBottom)
			win.MarkPositionDirty()
			win.IsBeingManipulated = true // Show resize indicator for neighbor windows
			o.PendingResizes[win.ID] = [2]int{win.Width, oldWinBottom - constrainedBottom}
		}

		newBottom = constrainedBottom
	}

	// Handle top edge movement (horizontal split line)
	if newY != oldY {
		topWindows, bottomWindows := findWindowsOnHorizontalSplitAll(o, oldY)
		topWindows = removeWindowFromList(topWindows, resized)
		bottomWindows = removeWindowFromList(bottomWindows, resized)

		constrainedY := newY
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

		constrainedY = max(minValidY, min(constrainedY, maxValidY))

		for _, win := range topWindows {
			win.ResizeVisual(win.Width, constrainedY-win.Y)
			win.MarkPositionDirty()
			win.IsBeingManipulated = true // Show resize indicator for neighbor windows
			o.PendingResizes[win.ID] = [2]int{win.Width, constrainedY - win.Y}
		}
		for _, win := range bottomWindows {
			oldWinBottom := win.Y + win.Height
			win.Y = constrainedY
			win.ResizeVisual(win.Width, oldWinBottom-constrainedY)
			win.MarkPositionDirty()
			win.IsBeingManipulated = true // Show resize indicator for neighbor windows
			o.PendingResizes[win.ID] = [2]int{win.Width, oldWinBottom - constrainedY}
		}

		newY = constrainedY
	}

	resized.X = newX
	resized.Y = newY
	resized.Width = newRight - newX
	resized.Height = newBottom - newY

	if resized.Width < minWidth || resized.Height < minHeight ||
		resized.X < 0 || resized.Y < 0 ||
		resized.X+resized.Width > o.Width || resized.Y+resized.Height > maxY {
		resized.Width = max(minWidth, min(resized.Width, o.Width-resized.X))
		resized.Height = max(minHeight, min(resized.Height, maxY-resized.Y))
		resized.X = max(0, min(resized.X, o.Width-minWidth))
		resized.Y = max(minY, min(resized.Y, maxY-minHeight))
	}

	resized.ResizeVisual(resized.Width, resized.Height)
	o.PendingResizes[resized.ID] = [2]int{resized.Width, resized.Height}
	resized.MarkPositionDirty()
}

// findWindowsOnVerticalSplitAll finds all windows on a vertical split line (not excluding any window)
func findWindowsOnVerticalSplitAll(o *OS, splitX int) (leftWindows, rightWindows []*terminal.Window) {
	const tolerance = 1

	for _, win := range o.Windows {
		if win.Workspace != o.CurrentWorkspace || win.Minimized {
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
func findWindowsOnHorizontalSplitAll(o *OS, splitY int) (topWindows, bottomWindows []*terminal.Window) {
	const tolerance = 1

	for _, win := range o.Windows {
		if win.Workspace != o.CurrentWorkspace || win.Minimized {
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
