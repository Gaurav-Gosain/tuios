package input

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	uv "github.com/charmbracelet/ultraviolet"
)

// handleMouseMotion handles mouse motion events
func handleMouseMotion(msg tea.MouseMotionMsg, o *app.OS) (*app.OS, tea.Cmd) {
	mouse := msg.Mouse()

	o.X = mouse.X
	o.Y = mouse.Y
	o.LastMouseX = mouse.X
	o.LastMouseY = mouse.Y

	// Drag an overlay panel that was grabbed by its title bar / right-click.
	if o.OverlayMouseMotion(mouse.X, mouse.Y) {
		return o, nil
	}

	// Update pointer shape based on what we're hovering over (OSC 22)
	o.UpdatePointerForPosition(mouse.X, mouse.Y)

	// Forward mouse motion to terminal if in terminal mode and window supports motion events.
	// Only modes 1002 (button-event) and 1003 (any-event) support motion forwarding.
	// Mode 1000/1001 (normal tracking) only supports click/release  - forwarding motion
	// events to these apps causes phantom keypresses (issue #78).
	if o.Mode == app.TerminalMode {
		focusedWindow := o.GetFocusedWindow()
		if focusedWindow != nil && focusedWindow.Terminal != nil {
			shouldForward := focusedWindow.Terminal.SupportsMotionEvents()

			if shouldForward {
				// Convert to terminal-relative coordinates (0-based)
				termX, termY, inContent := focusedWindow.ScreenToTerminal(mouse.X, mouse.Y)
				// Check if motion is within terminal content area
				if inContent {
					// Create adjusted mouse event with terminal-relative coordinates
					adjustedMouse := uv.MouseMotionEvent{
						X:      termX,
						Y:      termY,
						Button: uv.MouseButton(mouse.Button),
						Mod:    uv.KeyMod(mouse.Mod),
					}
					// Send to the terminal (uses PTY for daemon windows)
					sendMouseToWindow(focusedWindow, adjustedMouse)
					return o, nil
				}
			}
		}
	}

	// Handle scrollbar drag
	if o.ScrollbarDragging && o.ScrollbarDragWindowIndex >= 0 && o.ScrollbarDragWindowIndex < len(o.Windows) {
		win := o.Windows[o.ScrollbarDragWindowIndex]
		scrollToPosition(win, mouse.Y)
		return o, nil
	}

	// Handle copy mode mouse motion
	if o.Dragging && o.DraggedWindowIndex >= 0 && o.DraggedWindowIndex < len(o.Windows) {
		draggedWindow := o.Windows[o.DraggedWindowIndex]
		if draggedWindow.CopyMode != nil && draggedWindow.CopyMode.Active {
			scrollDir := HandleCopyModeMouseMotion(draggedWindow.CopyMode, draggedWindow, mouse.X, mouse.Y)
			o.AutoScrollDir = scrollDir
			if scrollDir != 0 && !o.AutoScrollActive {
				o.AutoScrollActive = true
				return o, tea.Tick(50*time.Millisecond, func(t time.Time) tea.Msg {
					return app.AutoScrollTickMsg{}
				})
			}
			if scrollDir == 0 {
				o.AutoScrollActive = false
			}
			return o, nil
		}
	}

	// Handle text selection motion with auto-scroll
	{
		focusedWindow := o.GetFocusedWindow()
		if focusedWindow != nil && focusedWindow.IsSelecting {
			terminalX, terminalY, inContent := focusedWindow.ScreenToTerminal(mouse.X, mouse.Y)

			if inContent {
				focusedWindow.SelectionEnd.X = terminalX
				focusedWindow.SelectionEnd.Y = terminalY
			} else {
				// Auto-scroll when dragging above or below the content area
				borderOff := focusedWindow.BorderOffset()
				contentTop := focusedWindow.Y + borderOff
				contentBottom := focusedWindow.Y + borderOff + focusedWindow.ContentHeight()

				if mouse.Y < contentTop {
					// Dragging above  - enter copy mode and scroll up
					if focusedWindow.CopyMode == nil || !focusedWindow.CopyMode.Active {
						focusedWindow.EnterCopyMode()
					}
					if focusedWindow.CopyMode != nil {
						for range 3 {
							MoveUp(focusedWindow.CopyMode, focusedWindow)
						}
					}
					focusedWindow.SelectionEnd.Y = 0
					focusedWindow.SelectionEnd.X = max(terminalX, 0)
				} else if mouse.Y >= contentBottom {
					// Dragging below  - scroll down (or exit copy mode if at bottom)
					if focusedWindow.CopyMode != nil && focusedWindow.CopyMode.Active {
						for range 3 {
							MoveDown(focusedWindow.CopyMode, focusedWindow)
						}
					}
					focusedWindow.SelectionEnd.Y = focusedWindow.ContentHeight() - 1
					focusedWindow.SelectionEnd.X = max(terminalX, 0)
				}
			}
			focusedWindow.InvalidateCache()
			return o, nil
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
		// In scrolling mode, don't move windows during drag  - layout controls positions.
		// Swap detection happens on release.
		if o.UseScrollingLayout {
			return o, nil
		}
		// Calculate new position - allow windows to go partially off-screen for edge snapping
		newX := mouse.X - o.DragOffsetX
		newY := mouse.Y - o.DragOffsetY

		// Minimal bounds to prevent rendering issues and windows disappearing behind dock
		// Keep at least some of the window visible (title bar area)
		minVisibleX := 20 // Keep at least 20px visible on the right
		minVisibleY := 3  // Keep at least title bar visible at bottom

		// Prevent window from going too far left (causes ANSI rendering issues)
		if newX < -(focusedWindow.Width - minVisibleX) {
			newX = -(focusedWindow.Width - minVisibleX)
		}

		// Prevent window from going too far right
		if newX > o.Width-minVisibleX {
			newX = o.Width - minVisibleX
		}

		// Prevent window from going too far up
		topMargin := o.GetTopMargin()
		if newY < topMargin-(focusedWindow.Height-minVisibleY) {
			newY = topMargin - (focusedWindow.Height - minVisibleY)
		}

		// Prevent window from going behind dock
		maxY := topMargin + o.GetUsableHeight() - minVisibleY
		if newY > maxY {
			newY = maxY
		}

		focusedWindow.X = newX
		focusedWindow.Y = newY
		focusedWindow.MarkPositionDirty()
		return o, nil
	}

	if o.Resizing && o.InteractionMode {
		xOffset := mouse.X - o.ResizeStartX
		yOffset := mouse.Y - o.ResizeStartY

		newX := focusedWindow.X
		newY := focusedWindow.Y
		newWidth := focusedWindow.Width
		newHeight := focusedWindow.Height

		// In scrolling mode, only allow width resize (columns fill full height)
		if o.UseScrollingLayout {
			yOffset = 0
		}

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

		// Apply minimum size constraints
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

		// Apply viewport bounds checking to prevent windows from going off-screen
		// This is consistent with drag bounds checking and prevents layout issues

		// Left edge: prevent negative X
		if newX < 0 {
			// If resizing from left, adjust width to compensate
			if o.ResizeCorner == app.TopLeft || o.ResizeCorner == app.BottomLeft {
				newWidth += newX // Add the negative offset back to width
			}
			newX = 0
		}

		// Top edge: prevent window from moving into dock area or above screen
		topMargin := o.GetTopMargin()
		if newY < topMargin {
			// If resizing from top, adjust height to compensate
			if o.ResizeCorner == app.TopLeft || o.ResizeCorner == app.TopRight {
				newHeight += newY - topMargin // Add the offset back to height
			}
			newY = topMargin
		}

		// Right edge: prevent window from exceeding viewport width
		if newX+newWidth > o.Width {
			if o.ResizeCorner == app.TopRight || o.ResizeCorner == app.BottomRight {
				// Resizing from right edge - constrain width
				newWidth = o.Width - newX
			} else {
				// Resizing from left edge - constrain X position
				newX = o.Width - newWidth
			}
		}

		// Bottom edge: prevent window from exceeding usable height (dock area)
		// maxY is the absolute bottom boundary accounting for dock position
		maxY := topMargin + o.GetUsableHeight()
		if newY+newHeight > maxY {
			if o.ResizeCorner == app.BottomLeft || o.ResizeCorner == app.BottomRight {
				// Resizing from bottom edge - constrain height
				newHeight = maxY - newY
			} else {
				// Resizing from top edge - constrain Y position
				newY = maxY - newHeight
			}
		}

		// Final safety check: ensure dimensions stay within bounds after all adjustments
		newWidth = max(newWidth, config.DefaultWindowWidth)
		newHeight = max(newHeight, config.DefaultWindowHeight)
		newWidth = min(newWidth, o.Width-newX)
		newHeight = min(newHeight, maxY-newY)

		// In tiling mode (except scrolling), block resizing edges at screen boundaries
		if o.AutoTiling && !o.UseScrollingLayout {
			const edgeTolerance = 2 // Small tolerance for detecting screen edges

			// Check which edges are at screen boundaries
			atLeftEdge := focusedWindow.X <= edgeTolerance
			atRightEdge := (focusedWindow.X + focusedWindow.Width) >= (o.Width - edgeTolerance)
			atTopEdge := focusedWindow.Y <= edgeTolerance
			atBottomEdge := (focusedWindow.Y + focusedWindow.Height) >= (maxY - edgeTolerance)

			// Block resizing edges that are at screen boundaries
			switch o.ResizeCorner {
			case app.TopLeft:
				if atLeftEdge {
					newX = focusedWindow.X
					newWidth = focusedWindow.Width
				}
				if atTopEdge {
					newY = focusedWindow.Y
					newHeight = focusedWindow.Height
				}
			case app.TopRight:
				if atRightEdge {
					newWidth = focusedWindow.Width
				}
				if atTopEdge {
					newY = focusedWindow.Y
					newHeight = focusedWindow.Height
				}
			case app.BottomLeft:
				if atLeftEdge {
					newX = focusedWindow.X
					newWidth = focusedWindow.Width
				}
				if atBottomEdge {
					newHeight = focusedWindow.Height
				}
			case app.BottomRight:
				if atRightEdge {
					newWidth = focusedWindow.Width
				}
				if atBottomEdge {
					newHeight = focusedWindow.Height
				}
			}

			// In tiling mode, update visual state but defer PTY resize until drag completes
			// Store pending resizes for all affected windows
			o.AdjustTilingNeighborsVisual(focusedWindow, newX, newY, newWidth, newHeight)
			// Sync BSP ratios continuously so separator overlay follows the resize
			if config.SharedBorders {
				o.SyncBSPTreeFromGeometry()
			}
		} else if o.UseScrollingLayout {
			// Scrolling mode: compute width from horizontal drag delta.
			switch o.ResizeCorner {
			case app.TopLeft, app.BottomLeft:
				newWidth = o.PreResizeState.Width - xOffset
			case app.TopRight, app.BottomRight:
				newWidth = o.PreResizeState.Width + xOffset
			}
			maxWidth := o.Width * 9 / 10
			newWidth = max(min(newWidth, maxWidth), config.DefaultWindowWidth)

			// Update column width and reposition all windows visually.
			sl := o.GetOrCreateScrollingLayout()
			intID := o.GetWindowIntID(focusedWindow.ID)
			oldWidth := 0
			for ci := range sl.Columns {
				for _, wid := range sl.Columns[ci].WindowIDs {
					if wid == intID {
						oldWidth = sl.ResolveColumnWidth(ci, o.GetRenderWidth())
						sl.Columns[ci].FixedWidth = newWidth
						sl.Columns[ci].Proportion = 0
					}
				}
			}
			// For left-edge resize, shift viewport so the right edge stays fixed
			if (o.ResizeCorner == app.TopLeft || o.ResizeCorner == app.BottomLeft) && oldWidth > 0 {
				sl.ViewportX += newWidth - oldWidth
			}
			sl.ClampViewport(o.GetRenderWidth())
			layouts := sl.ComputePositions(o.GetRenderWidth(), o.GetUsableHeight(), o.GetTopMargin())
			for winID, rect := range layouts {
				win := o.GetWindowByIntID(winID)
				if win == nil {
					continue
				}
				win.X = rect.X
				win.Y = rect.Y
				win.Width = rect.W
				// Don't call ResizeVisual or Resize  - just set visual width.
				// Terminal emulator keeps old dimensions until release.
				win.MarkPositionDirty()
				win.InvalidateCache()
			}
			// Defer PTY resize to mouse release
			o.PendingResizes[focusedWindow.ID] = [2]int{newWidth, focusedWindow.Height}
		} else {
			// In floating mode, apply visual resize only (defer PTY resize until drag completes)
			focusedWindow.X = newX
			focusedWindow.Y = newY
			focusedWindow.ResizeVisual(newWidth, newHeight) // Visual resize only
			focusedWindow.MarkPositionDirty()
			// Store pending resize so PTY gets resized on mouse release
			o.PendingResizes[focusedWindow.ID] = [2]int{newWidth, newHeight}
		}

		return o, nil
	}

	return o, nil
}
