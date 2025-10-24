// Package input implements mouse event handling for TUIOS.
package input

import (
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	tea "github.com/charmbracelet/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
)

// handleMouseClick handles mouse click events
func handleMouseClick(msg tea.MouseClickMsg, o *app.OS) (*app.OS, tea.Cmd) {
	mouse := msg.Mouse()
	X := mouse.X
	Y := mouse.Y

	// Forward mouse events to terminal if in terminal mode and alt screen
	if o.Mode == app.TerminalMode {
		// Find which window was clicked
		clickedWindowIndex := findClickedWindow(X, Y, o)
		if clickedWindowIndex != -1 {
			clickedWindow := o.Windows[clickedWindowIndex]
			if clickedWindow.IsAltScreen && clickedWindow.Terminal != nil {
				// Convert to terminal-relative coordinates (0-based)
				termX := X - clickedWindow.X - 1
				termY := Y - clickedWindow.Y
				// Check if click is within terminal content area
				if termX >= 0 && termY >= 0 && termX < clickedWindow.Width-2 && termY < clickedWindow.Height-2 {
					// Focus the window first so subsequent events work
					o.FocusWindow(clickedWindowIndex)

					// Create adjusted mouse event with terminal-relative coordinates
					adjustedMouse := uv.MouseClickEvent{
						X:      termX,
						Y:      termY,
						Button: uv.MouseButton(mouse.Button),
						Mod:    uv.KeyMod(mouse.Mod),
					}
					// Send to the terminal emulator
					clickedWindow.Terminal.SendMouse(adjustedMouse)
					return o, nil
				}
			}
		}
	}

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

	clickedWindow := o.Windows[clickedWindowIndex]

	leftMost := clickedWindow.X + clickedWindow.Width

	// DEBUG: Log click attempts
	if os.Getenv("TUIOS_DEBUG_INTERNAL") == "1" {
		if f, err := os.OpenFile("/tmp/tuios-mouse-debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
			fmt.Fprintf(f, "[CLICK] X=%d Y=%d, Window X=%d Y=%d W=%d H=%d, leftMost=%d\n",
				X, Y, clickedWindow.X, clickedWindow.Y, clickedWindow.Width, clickedWindow.Height, leftMost)
			f.Close()
		}
	}

	// Check button clicks FIRST before mode switching or focus changes
	// Title bar is at window.Y (buttons are on the first line of the window)
	titleBarY := clickedWindow.Y

	// Button hitbox: slightly wider range based on empirical testing
	// Close button is rightmost, minimize is to its left

	// cross (close button) - rightmost area
	if mouse.Button == tea.MouseLeft && X >= leftMost-4 && X <= leftMost-1 && Y == titleBarY {
		o.DeleteWindow(clickedWindowIndex)
		o.InteractionMode = false
		return o, nil
	}

	if o.AutoTiling {
		// Tiling mode: minimize button
		if mouse.Button == tea.MouseLeft && X >= leftMost-7 && X <= leftMost-5 && Y == titleBarY {
			o.MinimizeWindow(clickedWindowIndex)
			o.InteractionMode = false
			return o, nil
		}
	} else {
		// Non-tiling: maximize button in middle
		if mouse.Button == tea.MouseLeft && X >= leftMost-7 && X <= leftMost-5 && Y == titleBarY {
			o.Snap(clickedWindowIndex, app.SnapFullScreen)
			o.InteractionMode = false
			return o, nil
		}

		// Non-tiling: minimize button leftmost
		if mouse.Button == tea.MouseLeft && X >= leftMost-10 && X <= leftMost-8 && Y == titleBarY {
			o.MinimizeWindow(clickedWindowIndex)
			o.InteractionMode = false
			return o, nil
		}
	}

	// Handle copy mode mouse clicks AFTER button checks
	if clickedWindow.CopyMode != nil && clickedWindow.CopyMode.Active {
		// In copy mode, handle mouse clicks for cursor movement and selection
		if mouse.Button == tea.MouseLeft {
			// Check if clicking in terminal content area (not on title bar or buttons)
			terminalX := X - clickedWindow.X - 1
			terminalY := Y - clickedWindow.Y      // Fixed: Y coordinate relative to window
			if terminalX >= 0 && terminalY >= 0 && terminalX < clickedWindow.Width-2 && terminalY < clickedWindow.Height-2 {
				// Start drag for visual selection
				HandleCopyModeMouseDrag(clickedWindow.CopyMode, clickedWindow, X, Y)
				o.Dragging = true
				o.DraggedWindowIndex = clickedWindowIndex
				o.InteractionMode = true
				return o, nil
			}
		}
		// If click is outside content area, fall through to normal window interaction
	}

	// Focus the clicked window and bring to front Z-index
	// This happens AFTER button and copy mode checks
	o.FocusWindow(clickedWindowIndex)
	if o.Mode == app.TerminalMode {
		o.Mode = app.WindowManagementMode
	}

	// Set interaction mode to prevent expensive rendering during drag/resize
	o.InteractionMode = true

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
				// Track consecutive clicks for double/triple-click selection
				now := time.Now()
				timeSinceLastClick := now.Sub(clickedWindow.LastClickTime)
				samePosition := clickedWindow.LastClickX == terminalX && clickedWindow.LastClickY == terminalY

				// Reset click count if too much time has passed or different position
				if timeSinceLastClick > 500*time.Millisecond || !samePosition {
					clickedWindow.ClickCount = 1
				} else {
					clickedWindow.ClickCount++
				}

				clickedWindow.LastClickTime = now
				clickedWindow.LastClickX = terminalX
				clickedWindow.LastClickY = terminalY

				// Handle different selection modes based on click count
				switch clickedWindow.ClickCount {
				case 1:
					// Single click - character selection
					clickedWindow.IsSelecting = true
					clickedWindow.SelectionStart.X = terminalX
					clickedWindow.SelectionStart.Y = terminalY
					clickedWindow.SelectionEnd = clickedWindow.SelectionStart
					clickedWindow.SelectionMode = 0 // Character mode
				case 2:
					// Double click - word selection
					selectWord(clickedWindow, terminalX, terminalY, o)
					clickedWindow.SelectionMode = 1 // Word mode
				case 3:
					// Triple click - line selection
					selectLine(clickedWindow, terminalY)
					clickedWindow.SelectionMode = 2 // Line mode
					// Reset click count after triple click
					clickedWindow.ClickCount = 0
				}

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

	// Forward mouse motion to terminal if in terminal mode and alt screen
	if o.Mode == app.TerminalMode {
		focusedWindow := o.GetFocusedWindow()
		if focusedWindow != nil && focusedWindow.IsAltScreen && focusedWindow.Terminal != nil {
			// Convert to terminal-relative coordinates (0-based)
			termX := mouse.X - focusedWindow.X - 1
			termY := mouse.Y - focusedWindow.Y
			// Check if motion is within terminal content area
			if termX >= 0 && termY >= 0 && termX < focusedWindow.Width-2 && termY < focusedWindow.Height-2 {
				// Create adjusted mouse event with terminal-relative coordinates
				adjustedMouse := uv.MouseMotionEvent{
					X:      termX,
					Y:      termY,
					Button: uv.MouseButton(mouse.Button),
					Mod:    uv.KeyMod(mouse.Mod),
				}
				// Send to the terminal emulator
				focusedWindow.Terminal.SendMouse(adjustedMouse)
				return o, nil
			}
		}
	}

	// Handle copy mode mouse motion
	if o.Dragging && o.DraggedWindowIndex >= 0 && o.DraggedWindowIndex < len(o.Windows) {
		draggedWindow := o.Windows[o.DraggedWindowIndex]
		if draggedWindow.CopyMode != nil && draggedWindow.CopyMode.Active {
			// Update selection in copy mode
			HandleCopyModeMouseMotion(draggedWindow.CopyMode, draggedWindow, mouse.X, mouse.Y)
			return o, nil
		}
	}

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
	// Forward mouse release to terminal if in terminal mode and alt screen
	if o.Mode == app.TerminalMode {
		focusedWindow := o.GetFocusedWindow()
		if focusedWindow != nil && focusedWindow.IsAltScreen && focusedWindow.Terminal != nil {
			mouse := msg.Mouse()
			// Convert to terminal-relative coordinates (0-based)
			termX := mouse.X - focusedWindow.X - 1
			termY := mouse.Y - focusedWindow.Y
			// Check if release is within terminal content area
			if termX >= 0 && termY >= 0 && termX < focusedWindow.Width-2 && termY < focusedWindow.Height-2 {
				// Create adjusted mouse event with terminal-relative coordinates
				adjustedMouse := uv.MouseReleaseEvent{
					X:      termX,
					Y:      termY,
					Button: uv.MouseButton(mouse.Button),
					Mod:    uv.KeyMod(mouse.Mod),
				}
				// Send to the terminal emulator
				focusedWindow.Terminal.SendMouse(adjustedMouse)
				return o, nil
			}
		}
	}

	// Handle copy mode mouse release
	if o.Dragging && o.DraggedWindowIndex >= 0 && o.DraggedWindowIndex < len(o.Windows) {
		draggedWindow := o.Windows[o.DraggedWindowIndex]
		if draggedWindow.CopyMode != nil && draggedWindow.CopyMode.Active {
			// Selection is complete, just clean up drag state
			o.Dragging = false
			o.DraggedWindowIndex = -1
			o.InteractionMode = false
			return o, nil
		}
	}

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

		// Calculate drag distance to determine if this was actually a drag or just a click
		dragDistance := abs(mouse.X-o.DragStartX) + abs(mouse.Y-o.DragStartY)
		const dragThreshold = 5 // pixels - must move at least this much to be considered a drag

		if dragDistance >= dragThreshold {
			// This was an actual drag, check for swap
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
		}
		// If dragDistance < dragThreshold, it was just a click - do nothing
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

	// Forward mouse wheel to terminal if in terminal mode and alt screen
	// This allows applications like vim, less, htop to handle their own scrolling
	if o.Mode == app.TerminalMode {
		focusedWindow := o.GetFocusedWindow()
		if focusedWindow != nil && focusedWindow.IsAltScreen && focusedWindow.Terminal != nil {
			mouse := msg.Mouse()
			// Convert to terminal-relative coordinates (0-based)
			termX := mouse.X - focusedWindow.X - 1
			termY := mouse.Y - focusedWindow.Y
			// Check if wheel is within terminal content area
			if termX >= 0 && termY >= 0 && termX < focusedWindow.Width-2 && termY < focusedWindow.Height-2 {
				// Create adjusted mouse event with terminal-relative coordinates
				adjustedMouse := uv.MouseWheelEvent{
					X:      termX,
					Y:      termY,
					Button: uv.MouseButton(mouse.Button),
					Mod:    uv.KeyMod(mouse.Mod),
				}
				// Send to the terminal emulator
				focusedWindow.Terminal.SendMouse(adjustedMouse)
				return o, nil
			}
		}
	}

	// Handle scrollback in terminal mode or selection mode
	if o.Mode == app.TerminalMode || o.SelectionMode {
		focusedWindow := o.GetFocusedWindow()
		if focusedWindow != nil {
			switch msg.Button {
			case tea.MouseWheelUp:
				if o.SelectionMode {
					// In selection mode, scroll without entering scrollback mode
					if focusedWindow.Terminal != nil {
						scrollbackLen := focusedWindow.ScrollbackLen()
						if scrollbackLen > 0 && focusedWindow.ScrollbackOffset < scrollbackLen {
							focusedWindow.ScrollbackOffset += 3
							if focusedWindow.ScrollbackOffset > scrollbackLen {
								focusedWindow.ScrollbackOffset = scrollbackLen
							}
							focusedWindow.InvalidateCache()
						}
					}
				} else {
					// In terminal mode, enter copy mode on wheel up
					if focusedWindow.CopyMode == nil || !focusedWindow.CopyMode.Active {
						focusedWindow.EnterCopyMode()
						o.ShowNotification("COPY MODE (hjkl/q)", "info", config.NotificationDuration)
					}
					// Scroll up in copy mode
					if focusedWindow.CopyMode != nil && focusedWindow.CopyMode.Active {
						for i := 0; i < 3; i++ {
							MoveUp(focusedWindow.CopyMode, focusedWindow)
						}
						focusedWindow.InvalidateCache()
					}
				}
				return o, nil
			case tea.MouseWheelDown:
				if o.SelectionMode {
					// In selection mode, scroll without entering scrollback mode
					if focusedWindow.ScrollbackOffset > 0 {
						focusedWindow.ScrollbackOffset -= 3
						if focusedWindow.ScrollbackOffset < 0 {
							focusedWindow.ScrollbackOffset = 0
						}
						focusedWindow.InvalidateCache()
					}
				} else if focusedWindow.CopyMode != nil && focusedWindow.CopyMode.Active {
					// In copy mode, scroll down
					for i := 0; i < 3; i++ {
						MoveDown(focusedWindow.CopyMode, focusedWindow)
					}
					// Exit copy mode if at bottom
					if focusedWindow.CopyMode.ScrollOffset == 0 && focusedWindow.CopyMode.CursorY >= focusedWindow.Height-3 {
						focusedWindow.ExitCopyMode()
						o.ShowNotification("Copy Mode Exited", "info", config.NotificationDuration)
					}
					focusedWindow.InvalidateCache()
				}
				return o, nil
			}
		}
	}

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

	// Sort by minimize order to match renderDock
	sort.Slice(minimizedWindows, func(i, j int) bool {
		return o.Windows[minimizedWindows[i]].MinimizeOrder < o.Windows[minimizedWindows[j]].MinimizeOrder
	})

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
	leftInfoWidth := 30 // Mode + workspace indicators (matching render.go)

	// Calculate rightInfoWidth based on whether we're in copy mode (matching render.go exactly)
	focusedWindow := o.GetFocusedWindow()
	inCopyMode := focusedWindow != nil && focusedWindow.CopyMode != nil && focusedWindow.CopyMode.Active

	var rightInfoWidth int
	if inCopyMode {
		// In copy mode, help text can be very long - estimate based on copy mode state
		// These widths match the actual help text lengths in render.go
		switch focusedWindow.CopyMode.State {
		case terminal.CopyModeNormal:
			rightInfoWidth = 110 // Length of normal mode help text + padding
		case terminal.CopyModeSearch:
			rightInfoWidth = 60 // Length of search mode help text + padding
		case terminal.CopyModeVisualChar:
			rightInfoWidth = 90 // Length of visual char mode help text + padding
		case terminal.CopyModeVisualLine:
			rightInfoWidth = 35 // Length of visual line mode help text + padding
		default:
			rightInfoWidth = 32
		}
	} else {
		rightInfoWidth = 32 // CPU graph (~19 chars) + space + RAM (~11 chars) = ~31 chars (matching render.go line 1819)
	}

	availableSpace := o.Width - leftInfoWidth - rightInfoWidth - dockItemsWidth
	leftSpacer := max(availableSpace/2, 0)

	startX := leftInfoWidth + leftSpacer

	// DEBUG: Log dock click attempts
	if os.Getenv("TUIOS_DEBUG_INTERNAL") == "1" {
		if f, err := os.OpenFile("/tmp/tuios-dock-debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
			fmt.Fprintf(f, "[DOCK CLICK] X=%d Y=%d, Height=%d, startX=%d, dockItemsWidth=%d, numItems=%d\n",
				x, y, o.Height, startX, dockItemsWidth, len(minimizedWindows))
			f.Close()
		}
	}

	// Check which item was clicked
	currentX := startX
	for i, windowIndex := range minimizedWindows {
		// Add space BEFORE item (matching renderDock logic where space comes before item)
		if i > 0 {
			currentX++ // Space between items comes BEFORE this item
		}

		itemWidth := itemWidths[i]

		// Dock items are rendered as: leftCircle (1 cell) + label + rightCircle (1 cell)
		// The entire item including circles should be clickable
		clickableStart := currentX
		clickableEnd := currentX + itemWidth

		// DEBUG: Log each item bounds
		if os.Getenv("TUIOS_DEBUG_INTERNAL") == "1" {
			if f, err := os.OpenFile("/tmp/tuios-dock-debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
				fmt.Fprintf(f, "[DOCK ITEM %d] windowIndex=%d, Full range [%d,%d), Clickable [%d,%d), Y=%d (checking Y==%d)\n",
					i, windowIndex, currentX, currentX+itemWidth, clickableStart, clickableEnd, o.Height-1, y)
				f.Close()
			}
		}

		// Check if click is within this dock item (dock bar is at o.Height-1)
		if x >= clickableStart && x < clickableEnd && y == o.Height-1 {
			// DEBUG: Log successful match
			if os.Getenv("TUIOS_DEBUG_INTERNAL") == "1" {
				if f, err := os.OpenFile("/tmp/tuios-dock-debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
					fmt.Fprintf(f, "[DOCK MATCH] Item %d (windowIndex=%d) matched! Click X=%d in range [%d,%d)\n",
						i, windowIndex, x, clickableStart, clickableEnd)
					f.Close()
				}
			}
			return windowIndex
		}

		currentX += itemWidth
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

// abs returns the absolute value of an integer
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// selectWord selects the word at the given position
func selectWord(window *terminal.Window, x, y int, o *app.OS) {
	if window.Terminal == nil {
		return
	}

	screen := window.Terminal
	maxX := window.Width - 2

	// Find the start of the word (move left until we hit a non-word character)
	startX := x
	for startX > 0 {
		cell := screen.CellAt(startX-1, y)
		if cell == nil || cell.Content == "" || !isWordChar(rune(cell.Content[0])) {
			break
		}
		startX--
	}

	// Find the end of the word (move right until we hit a non-word character)
	endX := x
	for endX < maxX-1 {
		cell := screen.CellAt(endX+1, y)
		if cell == nil || cell.Content == "" || !isWordChar(rune(cell.Content[0])) {
			break
		}
		endX++
	}

	// Set the selection
	window.IsSelecting = true
	window.SelectionStart.X = startX
	window.SelectionStart.Y = y
	window.SelectionEnd.X = endX
	window.SelectionEnd.Y = y

	// Extract the selected text
	window.SelectedText = extractSelectedText(window, o)
	window.InvalidateCache()
}

// selectLine selects the entire line at the given Y position
func selectLine(window *terminal.Window, y int) {
	maxX := window.Width - 2

	// Select the entire line
	window.IsSelecting = true
	window.SelectionStart.X = 0
	window.SelectionStart.Y = y
	window.SelectionEnd.X = maxX - 1
	window.SelectionEnd.Y = y

	window.InvalidateCache()
}

// isWordChar returns true if the rune is part of a word (alphanumeric or underscore)
func isWordChar(r rune) bool {
	return (r >= 'a' && r <= 'z') ||
		(r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') ||
		r == '_' || r == '-' || r == '.'
}
