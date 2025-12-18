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

	m.LogInfo("BSP: TileAllWindows called with %d visible windows", len(visibleWindows))

	// Try to use BSP tree if available
	tree := m.WorkspaceTrees[m.CurrentWorkspace]

	// Check if tree is valid and in sync with visible windows
	if tree != nil && !tree.IsEmpty() {
		// First, check if tree has any stale windows (windows not in visibleWindows)
		treeIDs := tree.GetAllWindowIDs()
		visibleIDs := make(map[int]bool)
		for _, win := range visibleWindows {
			intID := m.getWindowIntID(win.ID)
			visibleIDs[intID] = true
			m.LogInfo("BSP: Visible window %s has int ID %d", win.ID[:8], intID)
		}
		m.LogInfo("BSP: Tree has IDs: %v, visible IDs: %v", treeIDs, visibleIDs)

		hasStaleWindows := false
		for _, id := range treeIDs {
			if !visibleIDs[id] {
				hasStaleWindows = true
				m.LogInfo("BSP: Tree has stale window ID %d, will rebuild", id)
				break
			}
		}

		// If tree has stale windows, clear it and rebuild
		if hasStaleWindows {
			m.LogInfo("BSP: Clearing stale tree and rebuilding")
			m.WorkspaceTrees[m.CurrentWorkspace] = nil
			tree = nil
		}
	}

	// If no tree or tree was cleared, create fresh one
	if tree == nil || tree.IsEmpty() {
		m.LogInfo("BSP: Creating fresh tree for %d windows", len(visibleWindows))
		tree = m.GetOrCreateBSPTree()

		bounds := m.GetBSPBounds()
		var lastInsertedID int = 0

		for i, win := range visibleWindows {
			windowIntID := m.getWindowIntID(win.ID)
			tree.InsertWindow(windowIntID, lastInsertedID, layout.SplitNone, 0.5, bounds)
			lastInsertedID = windowIntID
			m.LogInfo("BSP: Added window %d (int ID %d) with target %d", i+1, windowIntID, lastInsertedID)
		}

		m.ApplyBSPLayout()
		return
	}

	// Tree exists and is valid - check if all visible windows are in it
	allInTree := true
	for _, win := range visibleWindows {
		windowIntID := m.getWindowIntID(win.ID)
		if !tree.HasWindow(windowIntID) {
			allInTree = false
			break
		}
	}

	if allInTree {
		m.ApplyBSPLayout()
		return
	}

	// Some windows missing from tree - add them individually
	m.LogInfo("BSP: Adding missing windows to existing tree")

	for _, win := range visibleWindows {
		windowIntID := m.getWindowIntID(win.ID)
		if !tree.HasWindow(windowIntID) {
			existingIDs := tree.GetAllWindowIDs()
			targetIntID := 0
			if len(existingIDs) > 0 {
				targetIntID = existingIDs[len(existingIDs)-1]
			}

			bounds := m.GetBSPBounds()
			tree.InsertWindow(windowIntID, targetIntID, layout.SplitNone, 0.5, bounds)
			m.LogInfo("BSP: Added missing window (int ID %d) with target %d", windowIntID, targetIntID)
		}
	}
	m.ApplyBSPLayout()
}

// ToggleAutoTiling toggles automatic tiling mode
func (m *OS) ToggleAutoTiling() {
	m.AutoTiling = !m.AutoTiling

	if m.AutoTiling {
		m.LogInfo("BSP: Enabling tiling mode")

		// Initialize the workspace trees map if needed
		if m.WorkspaceTrees == nil {
			m.WorkspaceTrees = make(map[int]*layout.BSPTree)
		}

		// When enabling, create a fresh BSP tree and add all visible windows
		// This ensures proper spiral layout instead of guessing from geometry
		m.WorkspaceTrees[m.CurrentWorkspace] = nil // Clear any existing tree
		tree := m.GetOrCreateBSPTree()

		// Add all visible windows to the tree in order
		var visibleWindows []*terminal.Window
		for _, w := range m.Windows {
			if w.Workspace == m.CurrentWorkspace && !w.Minimized && !w.Minimizing {
				visibleWindows = append(visibleWindows, w)
			}
		}

		bounds := m.GetBSPBounds()
		var lastInsertedID int = 0 // Track the last inserted window for proper chaining

		for i, win := range visibleWindows {
			windowIntID := m.getWindowIntID(win.ID)
			// Pass the last inserted ID as target so windows chain properly
			// This ensures spiral pattern: first window is root, subsequent windows split the previous one
			tree.InsertWindow(windowIntID, lastInsertedID, layout.SplitNone, 0.5, bounds)
			lastInsertedID = windowIntID // Update for next iteration
			m.LogInfo("BSP: Added window %d (int ID %d) with target %d, split count now: %d",
				i+1, windowIntID, lastInsertedID, tree.WindowCount())
		}

		m.ApplyBSPLayout()
		m.LogInfo("BSP: Tiling enabled with %d windows", len(visibleWindows))
	} else {
		m.LogInfo("BSP: Disabling tiling mode")
		// Clear preselection when disabling tiling
		m.PreselectionDir = layout.PreselectionNone
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
		config.GetFastAnimationDuration(),
	)

	anim2 := ui.NewSnapAnimation(
		window2,
		x1, y1, width1, height1,
		config.GetFastAnimationDuration(),
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
		config.GetFastAnimationDuration(),
	)

	// Target window goes to dragged window's ORIGINAL position
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
			config.GetAnimationDuration(),
		)

		if anim != nil {
			m.Animations = append(m.Animations, anim)
		}
	}
}

// SwapWindowLeft swaps the focused window with the window to its left
func (m *OS) SwapWindowLeft() {
	if m.FocusedWindow < 0 || m.FocusedWindow >= len(m.Windows) {
		return
	}

	// Don't swap if animations are in progress
	if m.HasActiveAnimations() {
		return
	}

	focusedWindow := m.Windows[m.FocusedWindow]

	// Find the window to the left in current workspace
	targetIndex := -1
	minDistance := m.Width

	for i, window := range m.Windows {
		if i == m.FocusedWindow || window.Workspace != m.CurrentWorkspace || window.Minimized || window.Minimizing {
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
		m.SwapWindowsInstant(m.FocusedWindow, targetIndex)
	}
}

// SwapWindowRight swaps the focused window with the window to its right
func (m *OS) SwapWindowRight() {
	if m.FocusedWindow < 0 || m.FocusedWindow >= len(m.Windows) {
		return
	}

	// Don't swap if animations are in progress
	if m.HasActiveAnimations() {
		return
	}

	focusedWindow := m.Windows[m.FocusedWindow]

	// Find the window to the right in current workspace
	targetIndex := -1
	minDistance := m.Width

	for i, window := range m.Windows {
		if i == m.FocusedWindow || window.Workspace != m.CurrentWorkspace || window.Minimized || window.Minimizing {
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
		m.SwapWindowsInstant(m.FocusedWindow, targetIndex)
	}
}

// SwapWindowUp swaps the focused window with the window above it
func (m *OS) SwapWindowUp() {
	if m.FocusedWindow < 0 || m.FocusedWindow >= len(m.Windows) {
		return
	}

	// Don't swap if animations are in progress
	if m.HasActiveAnimations() {
		return
	}

	focusedWindow := m.Windows[m.FocusedWindow]

	// Find the window above in current workspace
	targetIndex := -1
	minDistance := m.Height

	for i, window := range m.Windows {
		if i == m.FocusedWindow || window.Workspace != m.CurrentWorkspace || window.Minimized || window.Minimizing {
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
		m.SwapWindowsInstant(m.FocusedWindow, targetIndex)
	}
}

// SwapWindowDown swaps the focused window with the window below it
func (m *OS) SwapWindowDown() {
	if m.FocusedWindow < 0 || m.FocusedWindow >= len(m.Windows) {
		return
	}

	// Don't swap if animations are in progress
	if m.HasActiveAnimations() {
		return
	}

	focusedWindow := m.Windows[m.FocusedWindow]

	// Find the window below in current workspace
	targetIndex := -1
	minDistance := m.Height

	for i, window := range m.Windows {
		if i == m.FocusedWindow || window.Workspace != m.CurrentWorkspace || window.Minimized || window.Minimizing {
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
		m.SwapWindowsInstant(m.FocusedWindow, targetIndex)
	}
}

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

// SaveCurrentLayout saves the current window layout for the active workspace
func (m *OS) SaveCurrentLayout() {
	if !m.AutoTiling {
		return
	}

	layouts := make([]WindowLayout, 0, len(m.Windows))
	for _, win := range m.Windows {
		if win.Workspace == m.CurrentWorkspace && !win.Minimized {
			layouts = append(layouts, WindowLayout{
				WindowID: win.ID,
				X:        win.X,
				Y:        win.Y,
				Width:    win.Width,
				Height:   win.Height,
			})
		}
	}

	m.WorkspaceLayouts[m.CurrentWorkspace] = layouts
	m.WorkspaceMasterRatio[m.CurrentWorkspace] = m.MasterRatio
}

// RestoreWorkspaceLayout restores saved layout when switching to a workspace
func (m *OS) RestoreWorkspaceLayout(workspace int) {
	if !m.AutoTiling {
		return
	}

	// Restore master ratio for this workspace (or use default)
	if ratio, exists := m.WorkspaceMasterRatio[workspace]; exists {
		m.MasterRatio = ratio
	} else {
		m.MasterRatio = 0.5 // Default
	}

	// Check if we have a saved layout for this workspace
	savedLayouts, hasCustom := m.WorkspaceLayouts[workspace]
	if !hasCustom || len(savedLayouts) == 0 {
		// No custom layout - use default tiling
		m.WorkspaceHasCustom[workspace] = false
		return
	}

	// Apply saved layout
	for _, saved := range savedLayouts {
		// Find window by ID
		for _, win := range m.Windows {
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

	m.WorkspaceHasCustom[workspace] = true
}

// MarkLayoutCustom marks the current workspace as having a custom layout
func (m *OS) MarkLayoutCustom() {
	if m.AutoTiling {
		m.WorkspaceHasCustom[m.CurrentWorkspace] = true
		m.SaveCurrentLayout()
	}
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
	const edgeTolerance = 2
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

	// Block resizing if right edge is at screen boundary
	const edgeTolerance = 2
	atRightEdge := (focusedWindow.X + focusedWindow.Width) >= (m.Width - edgeTolerance)
	if atRightEdge {
		return // Can't resize right edge when it's at the screen edge
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
	m.AdjustTilingNeighbors(focusedWindow, newX, newY, newWidth, newHeight)
}

// AdjustTilingNeighbors adjusts ALL windows on affected split lines with constraint-based positioning
// This is the core tiling resize algorithm used by both mouse and keyboard resize operations
func (m *OS) AdjustTilingNeighbors(resized *terminal.Window, newX, newY, newWidth, newHeight int) {
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

	// Handle right edge movement (vertical split line)
	if newRight != oldRight {
		// Find all windows on this split line
		leftWindows, rightWindows := findWindowsOnVerticalSplitAll(m, oldRight)

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
		maxValidX := m.Width
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
		leftWindows, rightWindows := findWindowsOnVerticalSplitAll(m, oldX)

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
		maxValidX := m.Width
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
		topWindows, bottomWindows := findWindowsOnHorizontalSplitAll(m, oldBottom)

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
		topWindows, bottomWindows := findWindowsOnHorizontalSplitAll(m, oldY)

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
		resized.X+resized.Width > m.Width || resized.Y+resized.Height > maxY {
		// Constraint calculation failed - clamp as last resort to prevent panic
		resized.Width = max(minWidth, min(resized.Width, m.Width-resized.X))
		resized.Height = max(minHeight, min(resized.Height, maxY-resized.Y))
		resized.X = max(0, min(resized.X, m.Width-minWidth))
		resized.Y = max(minY, min(resized.Y, maxY-minHeight))
	}

	resized.Resize(resized.Width, resized.Height)
	resized.MarkPositionDirty()

	// Mark layout as custom
	m.MarkLayoutCustom()
}

// AdjustTilingNeighborsVisual is like AdjustTilingNeighbors but uses visual-only resize.
// This defers PTY resize operations until the drag completes, improving responsiveness
// during mouse resize operations while still constraining window sizes appropriately.
func (m *OS) AdjustTilingNeighborsVisual(resized *terminal.Window, newX, newY, newWidth, newHeight int) {
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

	// Handle right edge movement (vertical split line)
	if newRight != oldRight {
		leftWindows, rightWindows := findWindowsOnVerticalSplitAll(m, oldRight)
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

		maxValidX := m.Width
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
			m.PendingResizes[win.ID] = [2]int{constrainedRight - win.X, win.Height}
		}
		for _, win := range rightWindows {
			oldWinRight := win.X + win.Width
			win.X = constrainedRight
			win.ResizeVisual(oldWinRight-constrainedRight, win.Height)
			win.MarkPositionDirty()
			win.IsBeingManipulated = true // Show resize indicator for neighbor windows
			m.PendingResizes[win.ID] = [2]int{oldWinRight - constrainedRight, win.Height}
		}

		newRight = constrainedRight
	}

	// Handle left edge movement (vertical split line)
	if newX != oldX {
		leftWindows, rightWindows := findWindowsOnVerticalSplitAll(m, oldX)
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

		maxValidX := m.Width
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
			m.PendingResizes[win.ID] = [2]int{constrainedX - win.X, win.Height}
		}
		for _, win := range rightWindows {
			oldWinRight := win.X + win.Width
			win.X = constrainedX
			win.ResizeVisual(oldWinRight-constrainedX, win.Height)
			win.MarkPositionDirty()
			win.IsBeingManipulated = true // Show resize indicator for neighbor windows
			m.PendingResizes[win.ID] = [2]int{oldWinRight - constrainedX, win.Height}
		}

		newX = constrainedX
	}

	// Handle bottom edge movement (horizontal split line)
	if newBottom != oldBottom {
		topWindows, bottomWindows := findWindowsOnHorizontalSplitAll(m, oldBottom)
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
			m.PendingResizes[win.ID] = [2]int{win.Width, constrainedBottom - win.Y}
		}
		for _, win := range bottomWindows {
			oldWinBottom := win.Y + win.Height
			win.Y = constrainedBottom
			win.ResizeVisual(win.Width, oldWinBottom-constrainedBottom)
			win.MarkPositionDirty()
			win.IsBeingManipulated = true // Show resize indicator for neighbor windows
			m.PendingResizes[win.ID] = [2]int{win.Width, oldWinBottom - constrainedBottom}
		}

		newBottom = constrainedBottom
	}

	// Handle top edge movement (horizontal split line)
	if newY != oldY {
		topWindows, bottomWindows := findWindowsOnHorizontalSplitAll(m, oldY)
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
			m.PendingResizes[win.ID] = [2]int{win.Width, constrainedY - win.Y}
		}
		for _, win := range bottomWindows {
			oldWinBottom := win.Y + win.Height
			win.Y = constrainedY
			win.ResizeVisual(win.Width, oldWinBottom-constrainedY)
			win.MarkPositionDirty()
			win.IsBeingManipulated = true // Show resize indicator for neighbor windows
			m.PendingResizes[win.ID] = [2]int{win.Width, oldWinBottom - constrainedY}
		}

		newY = constrainedY
	}

	resized.X = newX
	resized.Y = newY
	resized.Width = newRight - newX
	resized.Height = newBottom - newY

	if resized.Width < minWidth || resized.Height < minHeight ||
		resized.X < 0 || resized.Y < 0 ||
		resized.X+resized.Width > m.Width || resized.Y+resized.Height > maxY {
		resized.Width = max(minWidth, min(resized.Width, m.Width-resized.X))
		resized.Height = max(minHeight, min(resized.Height, maxY-resized.Y))
		resized.X = max(0, min(resized.X, m.Width-minWidth))
		resized.Y = max(minY, min(resized.Y, maxY-minHeight))
	}

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

// ============================================================================
// BSP (Binary Space Partitioning) Tiling Functions
// ============================================================================

// GetOrCreateBSPTree returns the BSP tree for the current workspace, creating it if needed
func (m *OS) GetOrCreateBSPTree() *layout.BSPTree {
	if m.WorkspaceTrees == nil {
		m.WorkspaceTrees = make(map[int]*layout.BSPTree)
	}

	tree, exists := m.WorkspaceTrees[m.CurrentWorkspace]
	if !exists || tree == nil {
		tree = layout.NewBSPTree()
		// Use SchemeSpiral (alternating V,H,V,H) as default if TilingScheme not set
		if m.TilingScheme == layout.SchemeLongestSide {
			// SchemeLongestSide is the zero value, which means it wasn't explicitly set
			// Default to SchemeSpiral for proper alternating behavior
			tree.AutoScheme = layout.SchemeSpiral
		} else {
			tree.AutoScheme = m.TilingScheme
		}
		m.WorkspaceTrees[m.CurrentWorkspace] = tree
		m.LogInfo("BSP: Created new tree for workspace %d with scheme %s", m.CurrentWorkspace, tree.AutoScheme.String())
	}

	return tree
}

// GetBSPBounds returns the bounds for BSP layout calculation
func (m *OS) GetBSPBounds() layout.Rect {
	return layout.Rect{
		X: 0,
		Y: m.GetTopMargin(),
		W: m.Width,
		H: m.GetUsableHeight(),
	}
}

// BuildBSPTreeFromCurrentLayout creates a BSP tree from the current window geometry.
// This is used when enabling tiling mode to preserve the existing layout structure.
func (m *OS) BuildBSPTreeFromCurrentLayout() {
	var windows []layout.Rect
	var windowIDs []int

	for _, w := range m.Windows {
		if w.Workspace == m.CurrentWorkspace && !w.Minimized && !w.Minimizing {
			windows = append(windows, layout.Rect{
				X: w.X,
				Y: w.Y,
				W: w.Width,
				H: w.Height,
			})
			// Use window index as ID for BSP tree (we'll use a lookup map)
			windowIDs = append(windowIDs, m.getWindowIntID(w.ID))
		}
	}

	if len(windows) == 0 {
		return
	}

	bounds := m.GetBSPBounds()
	tree := layout.BuildTreeFromWindows(windows, windowIDs, bounds, m.TilingScheme)

	if m.WorkspaceTrees == nil {
		m.WorkspaceTrees = make(map[int]*layout.BSPTree)
	}
	m.WorkspaceTrees[m.CurrentWorkspace] = tree
}

// getWindowIntID returns a stable integer ID for a window string ID.
// Uses a direct map lookup for reliable ID assignment.
func (m *OS) getWindowIntID(stringID string) int {
	if stringID == "" {
		return 0
	}

	// Initialize the map if needed
	if m.WindowToBSPID == nil {
		m.WindowToBSPID = make(map[string]int)
	}

	// Check if we already have an ID for this window
	if id, exists := m.WindowToBSPID[stringID]; exists {
		return id
	}

	// Assign a new ID
	if m.NextBSPWindowID == 0 {
		m.NextBSPWindowID = 1 // Start from 1, 0 is reserved for "no window"
	}
	newID := m.NextBSPWindowID
	m.NextBSPWindowID++
	m.WindowToBSPID[stringID] = newID

	return newID
}

// getWindowByIntID returns the window for a given integer ID
func (m *OS) getWindowByIntID(intID int) *terminal.Window {
	if intID <= 0 {
		return nil
	}

	// Search through the map to find the string ID, then find the window
	for stringID, id := range m.WindowToBSPID {
		if id == intID {
			// Found the string ID, now find the window
			for _, w := range m.Windows {
				if w.ID == stringID {
					return w
				}
			}
			break
		}
	}
	return nil
}

// ApplyBSPLayout applies the BSP tree layout to all windows in the current workspace
func (m *OS) ApplyBSPLayout() {
	tree := m.GetOrCreateBSPTree()
	if tree == nil || tree.IsEmpty() {
		return
	}

	bounds := m.GetBSPBounds()
	layouts := tree.ApplyLayout(bounds)

	for windowIntID, rect := range layouts {
		win := m.getWindowByIntID(windowIntID)
		if win == nil || win.Workspace != m.CurrentWorkspace || win.Minimized {
			continue
		}

		// Create animation for smooth transition
		anim := ui.NewSnapAnimation(
			win,
			rect.X, rect.Y, rect.W, rect.H,
			config.GetAnimationDuration(),
		)

		if anim != nil {
			m.Animations = append(m.Animations, anim)
		}
	}
}

// AddWindowToBSPTree adds a window to the BSP tree and applies the layout.
// This should be called when a new window is created in tiling mode.
func (m *OS) AddWindowToBSPTree(window *terminal.Window) {
	tree := m.GetOrCreateBSPTree()
	windowIntID := m.getWindowIntID(window.ID)

	m.LogInfo("BSP: AddWindowToBSPTree for window %s (int ID %d)", window.ID[:8], windowIntID)

	// Determine the target window for splitting
	targetIntID := 0

	// If SplitTargetWindowID is set (for explicit splits like Ctrl+B, -), use that
	if m.SplitTargetWindowID != "" {
		targetIntID = m.getWindowIntID(m.SplitTargetWindowID)
		m.LogInfo("BSP: Using explicit split target (int ID %d)", targetIntID)
	} else {
		// Use the last window in the BSP tree as the target
		// This ensures proper spiral pattern
		existingIDs := tree.GetAllWindowIDs()
		if len(existingIDs) > 0 {
			targetIntID = existingIDs[len(existingIDs)-1]
			m.LogInfo("BSP: Using last tree window as target (int ID %d)", targetIntID)
		}
	}

	bounds := m.GetBSPBounds()

	// Check for preselection
	if m.PreselectionDir != layout.PreselectionNone {
		m.LogInfo("BSP: Inserting with preselection %d", m.PreselectionDir)
		tree.InsertWindowWithPreselection(windowIntID, targetIntID, m.PreselectionDir, bounds)
		m.PreselectionDir = layout.PreselectionNone // Clear preselection
	} else {
		tree.InsertWindow(windowIntID, targetIntID, layout.SplitNone, 0.5, bounds)
	}

	m.LogInfo("BSP: Tree now has %d windows", tree.WindowCount())

	// Apply the new layout
	m.ApplyBSPLayout()
}

// RemoveWindowFromBSPTree removes a window from the BSP tree and reapplies the layout.
// This should be called when a window is closed in tiling mode.
func (m *OS) RemoveWindowFromBSPTree(window *terminal.Window) {
	tree := m.WorkspaceTrees[m.CurrentWorkspace]
	if tree == nil {
		return
	}

	windowIntID := m.getWindowIntID(window.ID)
	tree.RemoveWindow(windowIntID)

	// Apply the new layout
	m.ApplyBSPLayout()
}

// SyncBSPTreeFromGeometry updates the BSP tree's split ratios to match current window positions.
// This should be called after mouse resize operations complete.
func (m *OS) SyncBSPTreeFromGeometry() {
	tree := m.WorkspaceTrees[m.CurrentWorkspace]
	if tree == nil || tree.IsEmpty() {
		return
	}

	// Build geometry map from current window positions
	geometry := make(map[int]layout.Rect)
	for _, win := range m.Windows {
		if win.Workspace == m.CurrentWorkspace && !win.Minimized && !win.Minimizing {
			windowIntID := m.getWindowIntID(win.ID)
			geometry[windowIntID] = layout.Rect{
				X: win.X,
				Y: win.Y,
				W: win.Width,
				H: win.Height,
			}
		}
	}

	bounds := m.GetBSPBounds()
	tree.SyncRatiosFromGeometry(geometry, bounds)
}

// SplitFocusedHorizontal splits the focused window horizontally (top/bottom) and creates a new terminal
func (m *OS) SplitFocusedHorizontal() {
	if !m.AutoTiling {
		return
	}

	focusedWin := m.GetFocusedWindow()
	if focusedWin == nil {
		return
	}

	// Store the target window ID BEFORE creating new window (which will change focus)
	m.SplitTargetWindowID = focusedWin.ID

	// Set preselection direction for the next window
	m.PreselectionDir = layout.PreselectionDown

	// Create a new window - it will be added with the preselection
	m.AddWindow("")

	// Clear the split target
	m.SplitTargetWindowID = ""
}

// SplitFocusedVertical splits the focused window vertically (left/right) and creates a new terminal
func (m *OS) SplitFocusedVertical() {
	if !m.AutoTiling {
		return
	}

	focusedWin := m.GetFocusedWindow()
	if focusedWin == nil {
		return
	}

	// Store the target window ID BEFORE creating new window (which will change focus)
	m.SplitTargetWindowID = focusedWin.ID

	// Set preselection direction for the next window
	m.PreselectionDir = layout.PreselectionRight

	// Create a new window - it will be added with the preselection
	m.AddWindow("")

	// Clear the split target
	m.SplitTargetWindowID = ""
}

// SetPreselection sets the preselection direction for the next window insertion
func (m *OS) SetPreselection(dir layout.PreselectionDir) {
	m.PreselectionDir = dir
	// Show notification about preselection
	var dirName string
	switch dir {
	case layout.PreselectionLeft:
		dirName = "left"
	case layout.PreselectionRight:
		dirName = "right"
	case layout.PreselectionUp:
		dirName = "up"
	case layout.PreselectionDown:
		dirName = "down"
	default:
		m.PreselectionDir = layout.PreselectionNone
		return
	}
	m.ShowNotification("Preselection: "+dirName, "info", config.NotificationDuration)
}

// ClearPreselection clears any active preselection
func (m *OS) ClearPreselection() {
	m.PreselectionDir = layout.PreselectionNone
}

// RotateFocusedSplit toggles the split direction at the focused window's parent
func (m *OS) RotateFocusedSplit() {
	if !m.AutoTiling {
		m.LogInfo("BSP: RotateSplit ignored - tiling not active")
		return
	}

	tree := m.WorkspaceTrees[m.CurrentWorkspace]
	if tree == nil {
		m.LogInfo("BSP: RotateSplit ignored - no tree for workspace %d", m.CurrentWorkspace)
		return
	}

	focusedWin := m.GetFocusedWindow()
	if focusedWin == nil {
		m.LogInfo("BSP: RotateSplit ignored - no focused window")
		return
	}

	windowIntID := m.getWindowIntID(focusedWin.ID)

	// Check if window is in the tree
	if !tree.HasWindow(windowIntID) {
		m.LogInfo("BSP: RotateSplit - window %d not in tree, has %d windows", windowIntID, tree.WindowCount())
		// Window not in tree - this can happen if tiling was enabled after windows were created
		// but the tree wasn't properly built. Let's rebuild it.
		m.LogInfo("BSP: Rebuilding tree to include all windows")
		m.TileAllWindows()
		return
	}

	node := tree.FindNode(windowIntID)
	if node == nil || node.Parent == nil {
		m.LogInfo("BSP: RotateSplit - window has no parent (is root), cannot rotate")
		m.ShowNotification("Cannot rotate: window has no parent split", "warning", 2000000000)
		return
	}

	tree.RotateSplit(windowIntID)
	m.LogInfo("BSP: Rotated split for window %d", windowIntID)

	// Reapply layout
	m.ApplyBSPLayout()
}

// EqualizeSplits resets all split ratios to 0.5 (equal splits)
func (m *OS) EqualizeSplits() {
	if !m.AutoTiling {
		return
	}

	tree := m.WorkspaceTrees[m.CurrentWorkspace]
	if tree == nil {
		return
	}

	tree.EqualizeRatios()

	// Reapply layout
	m.ApplyBSPLayout()
}

// SwapWindowsInBSPTree swaps two windows in the BSP tree
func (m *OS) SwapWindowsInBSPTree(window1, window2 *terminal.Window) {
	tree := m.WorkspaceTrees[m.CurrentWorkspace]
	if tree == nil {
		return
	}

	id1 := m.getWindowIntID(window1.ID)
	id2 := m.getWindowIntID(window2.ID)
	tree.SwapWindows(id1, id2)
}
