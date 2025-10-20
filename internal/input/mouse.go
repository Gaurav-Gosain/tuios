// Package input implements mouse event handling for TUIOS.
package input

import (
	"fmt"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	tea "github.com/charmbracelet/bubbletea/v2"
)

// handleMouseClick handles mouse click events
func handleMouseClick(msg tea.MouseClickMsg, o *app.OS) (*app.OS, tea.Cmd) {
	mouse := msg.Mouse()
	X := mouse.X
	Y := mouse.Y

	// Note: Mouse forwarding to terminals removed to prevent corruption
	// Applications that need mouse support (vim, less) will handle it themselves
	// when they enable mouse tracking modes

	// Check if click is in the dock area (always reserved)
	if Y >= o.Height-config.DockHeight {
		// Handle dock click only if there are minimized windows
		if o.HasMinimizedWindows() {
			dockIndex := findDockItemClicked(X, Y, o)
			if dockIndex != -1 {
				o.RestoreWindow(dockIndex)
				// Retile if in tiling mode
				if o.AutoTiling {
					o.TileAllWindows()
				}
			}
		}
		return o, nil
	}

	// Fast hit testing - find which window was clicked without expensive canvas generation
	clickedWindowIndex := findClickedWindow(X, Y, o)
	if clickedWindowIndex == -1 {
		// Consume the event even if no window is hit to prevent leaking
		return o, nil
	}

	// IMMEDIATELY focus the clicked window and bring to front Z-index
	// This ensures instant visual feedback when clicking
	o.FocusWindow(clickedWindowIndex)
	if o.Mode == app.TerminalMode {
		o.Mode = app.WindowManagementMode
	}

	// Now set interaction mode to prevent expensive rendering during drag/resize
	o.InteractionMode = true

	clickedWindow := o.Windows[clickedWindowIndex]
	leftMost := clickedWindow.X + clickedWindow.Width

	// cross (close button) - rightmost button
	if mouse.Button == tea.MouseLeft && X >= leftMost-5 && X <= leftMost-3 && Y == clickedWindow.Y {
		o.DeleteWindow(clickedWindowIndex)
		o.InteractionMode = false
		return o, nil
	}

	// square (maximize button) - middle button
	// In tiling mode, buttons are positioned differently (no maximize button)
	if o.AutoTiling {
		// Tiling mode: only dash (minimize) and cross (close) buttons
		// dash (minimize button) - leftmost button
		if mouse.Button == tea.MouseLeft && X >= leftMost-8 && X <= leftMost-6 && Y == clickedWindow.Y {
			o.MinimizeWindow(clickedWindowIndex)
			o.InteractionMode = false
			return o, nil
		}
	} else {
		// Non-tiling mode: dash, square (maximize), and cross buttons
		// square (maximize button) - middle button
		if mouse.Button == tea.MouseLeft && X >= leftMost-8 && X <= leftMost-6 && Y == clickedWindow.Y {
			// Toggle fullscreen for now (maximize functionality)
			o.Snap(clickedWindowIndex, app.SnapFullScreen)
			o.InteractionMode = false
			return o, nil
		}

		// dash (minimize button) - leftmost button
		if mouse.Button == tea.MouseLeft && X >= leftMost-11 && X <= leftMost-9 && Y == clickedWindow.Y {
			o.MinimizeWindow(clickedWindowIndex)
			o.InteractionMode = false
			return o, nil
		}
	}

	// Calculate drag offset based on the clicked window
	o.DragOffsetX = X - clickedWindow.X
	o.DragOffsetY = Y - clickedWindow.Y

	switch mouse.Button {
	case tea.MouseRight:
		// Prevent resizing in tiling mode
		if o.AutoTiling {
			o.InteractionMode = false
			return o, nil
		}

		// Already in interaction mode, now set resize-specific flags
		o.Resizing = true
		o.Windows[clickedWindowIndex].IsBeingManipulated = true
		o.ResizeStartX = mouse.X
		o.ResizeStartY = mouse.Y
		// Save state for resize calculations (avoid mutex copying)
		o.PreResizeState = terminal.Window{
			Title:  clickedWindow.Title,
			Width:  clickedWindow.Width,
			Height: clickedWindow.Height,
			X:      clickedWindow.X,
			Y:      clickedWindow.Y,
			Z:      clickedWindow.Z,
			ID:     clickedWindow.ID,
		}
		minX := clickedWindow.X
		midX := clickedWindow.X + (clickedWindow.Width / 2)

		minY := clickedWindow.Y
		midY := clickedWindow.Y + (clickedWindow.Height / 2)

		if mouse.X < midX && mouse.X >= minX {
			o.ResizeCorner = app.BottomLeft
			if mouse.Y < midY && mouse.Y >= minY {
				o.ResizeCorner = app.TopLeft
			}
		} else {
			o.ResizeCorner = app.BottomRight
			if mouse.Y < midY && mouse.Y >= minY {
				o.ResizeCorner = app.TopRight
			}
		}

	case tea.MouseLeft:
		// Check if we're in selection mode
		if o.SelectionMode {
			// Calculate terminal coordinates relative to window content
			terminalX := X - clickedWindow.X - 1 // Account for border
			terminalY := Y - clickedWindow.Y - 1 // Account for border

			// Start text selection
			if terminalX >= 0 && terminalY >= 0 &&
				terminalX < clickedWindow.Width-2 && terminalY < clickedWindow.Height-2 {
				clickedWindow.IsSelecting = true
				clickedWindow.SelectionStart.X = terminalX
				clickedWindow.SelectionStart.Y = terminalY
				clickedWindow.SelectionEnd = clickedWindow.SelectionStart
				o.InteractionMode = false
				return o, nil
			}
		}

		// Already in interaction mode, now set drag-specific flags
		o.Dragging = true
		o.DragStartX = mouse.X
		o.DragStartY = mouse.Y
		o.Windows[clickedWindowIndex].IsBeingManipulated = true
		o.DraggedWindowIndex = clickedWindowIndex

		// Store original position for tiling mode swaps
		if o.AutoTiling {
			o.TiledX = clickedWindow.X
			o.TiledY = clickedWindow.Y
			o.TiledWidth = clickedWindow.Width
			o.TiledHeight = clickedWindow.Height
		}
	}
	return o, nil
}

// handleMouseMotion handles mouse motion events
func handleMouseMotion(msg tea.MouseMotionMsg, o *app.OS) (*app.OS, tea.Cmd) {
	mouse := msg.Mouse()

	o.X = mouse.X
	o.Y = mouse.Y
	o.LastMouseX = mouse.X
	o.LastMouseY = mouse.Y

	// Mouse motion forwarding removed to prevent terminal corruption
	// Terminal applications will handle their own mouse tracking when needed

	// Handle text selection motion
	if o.SelectionMode {
		focusedWindow := o.GetFocusedWindow()
		if focusedWindow != nil && focusedWindow.IsSelecting {
			// Calculate terminal coordinates
			terminalX := mouse.X - focusedWindow.X - 1
			terminalY := mouse.Y - focusedWindow.Y - 1

			// Update selection end position
			if terminalX >= 0 && terminalY >= 0 &&
				terminalX < focusedWindow.Width-2 && terminalY < focusedWindow.Height-2 {
				focusedWindow.SelectionEnd.X = terminalX
				focusedWindow.SelectionEnd.Y = terminalY
				return o, nil
			}
		}
	}

	if !o.Dragging && !o.Resizing {
		// Always consume motion events to prevent leaking to terminals
		return o, nil
	}

	focusedWindow := o.GetFocusedWindow()
	if focusedWindow == nil {
		o.Dragging = false
		o.Resizing = false
		o.InteractionMode = false
		return o, nil
	}

	if o.Dragging && o.InteractionMode {
		// Allow windows to go outside screen bounds but not into dock area
		newX := mouse.X - o.DragOffsetX
		newY := mouse.Y - o.DragOffsetY

		// Prevent dragging into dock area
		maxY := o.GetUsableHeight() - focusedWindow.Height
		if newY > maxY {
			newY = maxY
		}

		focusedWindow.X = newX
		focusedWindow.Y = newY
		focusedWindow.MarkPositionDirty()
		return o, nil
	}

	if o.Resizing && o.InteractionMode {
		// Prevent resizing in tiling mode
		if o.AutoTiling {
			return o, nil
		}

		xOffset := mouse.X - o.ResizeStartX
		yOffset := mouse.Y - o.ResizeStartY

		newX := focusedWindow.X
		newY := focusedWindow.Y
		newWidth := focusedWindow.Width
		newHeight := focusedWindow.Height

		switch o.ResizeCorner {
		case app.TopLeft:
			newX = o.PreResizeState.X + xOffset
			newY = o.PreResizeState.Y + yOffset
			newWidth = o.PreResizeState.Width - xOffset
			newHeight = o.PreResizeState.Height - yOffset
		case app.TopRight:
			newY = o.PreResizeState.Y + yOffset
			newWidth = o.PreResizeState.Width + xOffset
			newHeight = o.PreResizeState.Height - yOffset
		case app.BottomLeft:
			newX = o.PreResizeState.X + xOffset
			newWidth = o.PreResizeState.Width - xOffset
			newHeight = o.PreResizeState.Height + yOffset
		case app.BottomRight:
			newWidth = o.PreResizeState.Width + xOffset
			newHeight = o.PreResizeState.Height + yOffset
		}

		if newWidth < config.DefaultWindowWidth {
			newWidth = config.DefaultWindowWidth
			if o.ResizeCorner == app.TopLeft || o.ResizeCorner == app.BottomLeft {
				newX = o.PreResizeState.X + o.PreResizeState.Width - config.DefaultWindowWidth
			}
		}
		if newHeight < config.DefaultWindowHeight {
			newHeight = config.DefaultWindowHeight
			if o.ResizeCorner == app.TopLeft || o.ResizeCorner == app.TopRight {
				newY = o.PreResizeState.Y + o.PreResizeState.Height - config.DefaultWindowHeight
			}
		}

		// Prevent resizing into dock area
		maxY := o.GetUsableHeight()
		if newY+newHeight > maxY {
			if o.ResizeCorner == app.BottomLeft || o.ResizeCorner == app.BottomRight {
				newHeight = maxY - newY
			}
		}
		if newY+newHeight > maxY {
			newY = maxY - newHeight
		}

		// Apply the resize
		focusedWindow.X = newX
		focusedWindow.Y = newY
		focusedWindow.Width = max(newWidth, config.DefaultWindowWidth)
		focusedWindow.Height = max(newHeight, config.DefaultWindowHeight)

		focusedWindow.Resize(focusedWindow.Width, focusedWindow.Height)
		focusedWindow.MarkPositionDirty()

		return o, nil
	}

	return o, nil
}

// handleMouseRelease handles mouse release events
func handleMouseRelease(msg tea.MouseReleaseMsg, o *app.OS) (*app.OS, tea.Cmd) {
	// Mouse release forwarding removed to prevent terminal corruption

	// Always consume release events to prevent leaking to terminals

	// Handle text selection completion
	if o.SelectionMode {
		focusedWindow := o.GetFocusedWindow()
		if focusedWindow != nil && focusedWindow.IsSelecting {
			// Extract selected text from terminal
			selectedText := extractSelectedText(focusedWindow, o)
			if selectedText != "" {
				focusedWindow.SelectedText = selectedText
				o.ShowNotification(fmt.Sprintf("Selected %d chars - Press 'c' to copy", len(selectedText)), "success", config.NotificationDuration)
			}
			focusedWindow.IsSelecting = false
			return o, nil
		}
	}

	// Handle window drop in tiling mode
	if o.Dragging && o.AutoTiling && o.DraggedWindowIndex >= 0 && o.DraggedWindowIndex < len(o.Windows) {
		mouse := msg.Mouse()

		// Find which window is under the cursor (excluding the dragged window)
		targetWindowIndex := -1
		for i := range o.Windows {
			if i == o.DraggedWindowIndex || o.Windows[i].Minimized || o.Windows[i].Minimizing {
				continue
			}
			// Only consider windows in current workspace
			if o.Windows[i].Workspace != o.CurrentWorkspace {
				continue
			}

			w := o.Windows[i]
			if mouse.X >= w.X && mouse.X < w.X+w.Width &&
				mouse.Y >= w.Y && mouse.Y < w.Y+w.Height {
				targetWindowIndex = i
				break
			}
		}

		if targetWindowIndex >= 0 && targetWindowIndex != o.DraggedWindowIndex {
			// Swap windows - dragged window goes to target's position, target goes to dragged window's original position
			o.SwapWindowsWithOriginal(o.DraggedWindowIndex, targetWindowIndex, o.TiledX, o.TiledY, o.TiledWidth, o.TiledHeight)
		} else {
			// No swap, just retile to restore proper positions
			o.TileAllWindows()
		}
		o.DraggedWindowIndex = -1
	}

	// Clean up interaction state on mouse release
	if o.Dragging || o.Resizing {
		o.Dragging = false
		o.Resizing = false
		o.InteractionMode = false

		for i := range o.Windows {
			o.Windows[i].IsBeingManipulated = false
		}
	} else {
		// Even if we weren't dragging/resizing, clear interaction mode from click
		o.InteractionMode = false
	}

	// Mouse edge snapping disabled - use keyboard shortcuts for snapping

	return o, nil
}

// handleMouseWheel handles mouse wheel events
func handleMouseWheel(msg tea.MouseWheelMsg, o *app.OS) (*app.OS, tea.Cmd) {
	// Handle scrolling in help and log viewers
	if o.ShowHelp {
		switch msg.Button {
		case tea.MouseWheelUp:
			if o.HelpScrollOffset > 0 {
				o.HelpScrollOffset--
			}
		case tea.MouseWheelDown:
			o.HelpScrollOffset++
		}
		return o, nil
	}

	if o.ShowLogs {
		switch msg.Button {
		case tea.MouseWheelUp:
			if o.LogScrollOffset > 0 {
				o.LogScrollOffset--
			}
		case tea.MouseWheelDown:
			o.LogScrollOffset++
		}
		return o, nil
	}

	// Mouse wheel forwarding to terminals removed to prevent corruption
	// Terminal applications handle their own scrolling when they need it

	return o, nil
}

// Hit testing helpers

// findClickedWindow finds the topmost window at the given coordinates
func findClickedWindow(x, y int, o *app.OS) int {
	// Find the topmost window (highest Z) that contains the click point
	topWindow := -1
	topZ := -1

	for i, window := range o.Windows {
		// Skip windows not in current workspace
		if window.Workspace != o.CurrentWorkspace {
			continue
		}
		// Skip minimized windows
		if window.Minimized {
			continue
		}
		// Check if click is within window bounds
		if x >= window.X && x < window.X+window.Width &&
			y >= window.Y && y < window.Y+window.Height {
			// This window contains the click - check if it's the topmost so far
			if window.Z > topZ {
				topZ = window.Z
				topWindow = i
			}
		}
	}

	return topWindow
}

// findDockItemClicked finds which dock item was clicked
func findDockItemClicked(x, y int, o *app.OS) int {
	// Count minimized windows in current workspace
	minimizedWindows := make([]int, 0)
	for i, window := range o.Windows {
		if window.Workspace == o.CurrentWorkspace && window.Minimized {
			minimizedWindows = append(minimizedWindows, i)
			if len(minimizedWindows) >= 9 {
				break // Only first 9 items are shown
			}
		}
	}

	if len(minimizedWindows) == 0 {
		return -1
	}

	// Calculate actual dock item widths (matching render.go logic)
	dockItemsWidth := 0
	itemNumber := 1
	itemWidths := make([]int, 0, len(minimizedWindows))

	for _, windowIndex := range minimizedWindows {
		window := o.Windows[windowIndex]

		// Get window name (only custom names)
		windowName := window.CustomName

		// Format label based on whether we have a custom name
		var labelText string
		if windowName != "" {
			// Truncate if too long (max 12 chars for dock item)
			if len(windowName) > 12 {
				windowName = windowName[:9] + "..."
			}
			labelText = fmt.Sprintf(" %d:%s ", itemNumber, windowName)
		} else {
			// Just show the number if no custom name
			labelText = fmt.Sprintf(" %d ", itemNumber)
		}

		// Calculate width: 2 for circles + label width
		itemWidth := 2 + len(labelText)
		itemWidths = append(itemWidths, itemWidth)

		// Add spacing between items
		if itemNumber > 1 {
			dockItemsWidth++ // Space between items
		}
		dockItemsWidth += itemWidth

		itemNumber++
	}

	// Calculate center position considering system info on sides
	leftInfoWidth := 30  // Mode + workspace indicators (matching render.go)
	rightInfoWidth := 20 // CPU graph (matching render.go)
	availableSpace := o.Width - leftInfoWidth - rightInfoWidth - dockItemsWidth
	leftSpacer := max(availableSpace/2, 0)

	startX := leftInfoWidth + leftSpacer

	// Check which item was clicked
	currentX := startX
	for i, windowIndex := range minimizedWindows {
		itemWidth := itemWidths[i]

		// Check if click is within this dock item (single line dock at bottom)
		if x >= currentX && x < currentX+itemWidth && y == o.Height-1 {
			return windowIndex
		}

		currentX += itemWidth
		if i < len(minimizedWindows)-1 {
			currentX++ // Space between items
		}
	}

	return -1
}

// max returns the maximum of two integers
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
