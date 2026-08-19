package input

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
	uv "github.com/charmbracelet/ultraviolet"
)

// guestWantsMotion reports whether a guest's own mouse mode would have had the
// host report this motion to it. The host is held in all-motion tracking so
// tuios can draw its own hover and follow focus, so this is the only thing
// standing between a guest and motion it never asked for:
//
//   - 1003 (any-event): every motion.
//   - 1002 (button-event): only motion with a button held.
//   - 1000/1001 (normal): none. Motion delivered to these apps comes back as
//     phantom keypresses (issue #78).
func guestWantsMotion(term *vt.Emulator, button tea.MouseButton) bool {
	if term.HasAllMotionMode() {
		return true
	}
	return term.HasCellMotionMode() && button != tea.MouseNone
}

// handleMouseMotion handles mouse motion events
func handleMouseMotion(msg tea.MouseMotionMsg, o *app.OS) (*app.OS, tea.Cmd) {
	mouse := msg.Mouse()

	o.X = mouse.X
	o.Y = mouse.Y
	o.LastMouseX = mouse.X
	o.LastMouseY = mouse.Y

	// The host is held in all-motion tracking so hover and focus-follows-mouse
	// see the pointer, which means motion no longer implies a button is down.
	// Everything below that means "drag" is routed off a flag a press set, so a
	// release that went missing would keep the gesture alive against bare hover:
	// the panel would follow the cursor, the rail would resize, the picker would
	// repaint. Update already retires the window layer's drag and resize this
	// way; this covers the gestures the chrome holds in flags of its own.
	//
	// Only the gesture routing is retired here. The hover paths below run either
	// way, which is the whole point of asking for button-free motion.
	if mouse.Button == tea.MouseNone {
		o.EndPointerGrabs()
	}

	// An open context menu tracks the pointer, so the row that would run on a
	// click is the row the cursor is on. It also stops here rather than falling
	// through: the pane underneath is behind a modal menu and has no business
	// seeing motion over it.
	if o.ContextMenuActive() {
		o.ContextMenuHover(mouse.X, mouse.Y)
		return o, nil
	}

	// A pressed or dragged sidebar session row owns the pointer until release:
	// the motion either commits the press to a reorder drag or advances the
	// drag, and nothing underneath may see it either way.
	if o.SidebarDragActive() {
		o.SidebarDragMotion(mouse.X, mouse.Y)
		return o, nil
	}
	if o.SidebarEdgeActive() {
		o.SidebarEdgeMotion(mouse.X, mouse.Y)
		return o, nil
	}

	// A pressed or dragged workspace pill owns the pointer until release, for
	// the same reason a rail row does: the motion is what turns the press into a
	// reorder, and a pill dragged across the dock must not also be hovering the
	// controls it passes over.
	if o.DockWorkspaceDragActive() {
		o.DockWorkspaceDragMotion(mouse.X, mouse.Y)
		return o, nil
	}

	// Overlay panels: keep dragging a grabbed panel, or highlight the row under
	// the cursor so hover tracks the pointer in every overlay. Either way the
	// motion is consumed; a pane behind an overlay panel never sees it. The
	// hover paths (overlays and the sidebar band) yield while a window gesture
	// is in progress, so a drag or resize that crosses them never stalls.
	if o.OverlayDragActive() {
		o.OverlayMouseMotion(mouse.X, mouse.Y)
		return o, nil
	}
	if !o.Dragging && !o.Resizing && !o.ScrollbarDragging {
		if o.OverlayMouseMotion(mouse.X, mouse.Y) {
			return o, nil
		}
		// The sidebar band tracks hover the same way the overlays do, and
		// consumes motion over it so the pane it sits in front of never sees it.
		if o.SidebarActive() && o.SidebarMotion(mouse.X, mouse.Y) {
			return o, nil
		}
	}

	// The dock's session controls track the pointer so the recessed one is only
	// loud where a click would land, and so the glyph under it says what it does.
	// The motion is not consumed: nothing else in the dock band reacts to it, and
	// clearing the highlight and the label on the way out is the same call.
	//
	// This is also what times the label. There is no tick standing by to notice
	// the pointer has rested; arriving motion is the clock, and the maintenance
	// tick is held only across the delay window of a live gesture.
	o.DockSessionHoverAt(mouse.X, mouse.Y)

	// The workspace strip is timed off the same arriving motion, for the pills
	// that had to cut a name short. It runs after the session controls so a
	// pointer crossing from one to the other hands the label over in one pass:
	// each call clears only its own surface's hover.
	o.DockWorkspaceHoverAt(mouse.X, mouse.Y)

	// Ctrl-drag: an armed grab commits to a move once the pointer passes the
	// drag threshold, then rides the same path as a title-bar drag (the block
	// below moves the now-focused window). Ctrl let go before the grab commits
	// is the ctrl+click the press deferred.
	if o.CtrlDragPending {
		if mouse.Mod&tea.ModCtrl == 0 {
			o.CtrlDragPending = false
			if o.CtrlDragIndex >= 0 && o.CtrlDragIndex < len(o.Windows) {
				o.ToggleMultifocus(o.CtrlDragIndex)
			}
			return o, nil
		}
		if abs(mouse.X-o.DragStartX)+abs(mouse.Y-o.DragStartY) >= ctrlDragThreshold {
			o.CtrlDragPending = false
			o.CtrlDragging = true
			// beginWindowDrag leaves window management on, which is right for a
			// title-bar grab but not here: a ctrl-drag from a pane the user is
			// typing in should hand typing back when the pane lands.
			o.CtrlDragWasTerminal = o.Mode == app.TerminalMode
			beginWindowDrag(o, o.CtrlDragIndex, o.DragStartX, o.DragStartY)
		}
	}
	// A committed ctrl-drag drops the instant ctrl is no longer held, matching
	// "on leaving ctrl it lets go".
	if o.CtrlDragging && mouse.Mod&tea.ModCtrl == 0 {
		return finalizeCtrlDrag(o, mouse.X, mouse.Y)
	}

	// Update pointer shape based on what we're hovering over (OSC 22)
	o.UpdatePointerForPosition(mouse.X, mouse.Y)

	// Focus follows the mouse when the user opted in: the pane under the cursor
	// takes focus without a click and without entering terminal mode (clicking
	// is what starts typing). Gestures in progress keep their window, chrome
	// (overlays, menus, the sidebar band, the dock) never steals pane focus,
	// and nothing happens unless the pane under the cursor actually differs
	// from the focused one.
	// It follows the mouse in terminal mode too: a setting called
	// focus-follows-mouse that stops working wherever the user actually spends
	// their time just reads as broken. Rail keyboard focus still suppresses it,
	// since the rail owns the keyboard and a click is the way back to a pane.
	if config.FocusFollowsMouse && !o.SidebarFocused &&
		!o.Dragging && !o.Resizing && !o.ScrollbarDragging &&
		!o.AnyOverlayOpen() && !o.ContextMenuActive() &&
		!o.SidebarBandContains(mouse.X, mouse.Y) && !o.InDockBand(mouse.Y) {
		if idx := findClickedWindow(mouse.X, mouse.Y, o); idx >= 0 && idx != o.FocusedWindow {
			o.FocusWindow(idx)
		}
	}

	if o.Mode == app.TerminalMode {
		focusedWindow := o.GetFocusedWindow()
		if focusedWindow != nil && focusedWindow.Terminal != nil {
			if guestWantsMotion(focusedWindow.Terminal, mouse.Button) {
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
		scrollToThumbRow(win, mouse.Y-o.ScrollbarGrabOffset)
		return o, nil
	}

	// Handle copy mode mouse motion
	if o.Dragging && o.DraggedWindowIndex >= 0 && o.DraggedWindowIndex < len(o.Windows) {
		draggedWindow := o.Windows[o.DraggedWindowIndex]
		if draggedWindow.CopyMode != nil && draggedWindow.CopyMode.Active {
			// The pointer moved with the button down, so this gesture is a
			// drag. Its release is unambiguous and copies at once, rather than
			// waiting out a multi-click window that no longer applies. Cell
			// motion is only reported when the cell actually changes, so this
			// cannot be set by a stationary press.
			o.SelectionDragged = true
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

		// The visible sliver must stay inside the content region, so a drag can
		// never park a pane wholly under a reserved sidebar band.
		leftMargin := o.GetLeftMargin()
		contentRight := leftMargin + o.GetContentWidth()

		// Prevent window from going too far left (causes ANSI rendering issues)
		if newX < leftMargin-(focusedWindow.Width-minVisibleX) {
			newX = leftMargin - (focusedWindow.Width - minVisibleX)
		}

		// Prevent window from going too far right
		if newX > contentRight-minVisibleX {
			newX = contentRight - minVisibleX
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
		// A pane-border drag moves a single edge to the pointer, not a corner.
		if o.BorderResizing {
			applyBorderResize(o, mouse.X, mouse.Y)
			return o, nil
		}

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
		// or under a reserved sidebar band. This is consistent with drag bounds
		// checking and prevents layout issues.
		leftMargin := o.GetLeftMargin()
		contentRight := leftMargin + o.GetContentWidth()

		// Left edge: prevent X before the content region
		if newX < leftMargin {
			// If resizing from left, adjust width to compensate
			if o.ResizeCorner == app.TopLeft || o.ResizeCorner == app.BottomLeft {
				newWidth += newX - leftMargin // Give the overshoot back to width
			}
			newX = leftMargin
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

		// Right edge: prevent window from exceeding the content region
		if newX+newWidth > contentRight {
			if o.ResizeCorner == app.TopRight || o.ResizeCorner == app.BottomRight {
				// Resizing from right edge - constrain width
				newWidth = contentRight - newX
			} else {
				// Resizing from left edge - constrain X position
				newX = contentRight - newWidth
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
		newWidth = min(newWidth, contentRight-newX)
		newHeight = min(newHeight, maxY-newY)

		// In tiling mode (except scrolling), block resizing edges at the
		// content-region boundaries
		if o.AutoTiling && !o.UseScrollingLayout {
			const edgeTolerance = 2 // Small tolerance for detecting screen edges

			// Check which edges are at content-region boundaries
			atLeftEdge := focusedWindow.X <= leftMargin+edgeTolerance
			atRightEdge := (focusedWindow.X + focusedWindow.Width) >= (contentRight - edgeTolerance)
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
			treeInSync := o.AdjustTilingNeighborsVisual(focusedWindow, newX, newY, newWidth, newHeight)
			// The separator overlay is drawn from the tree's ratios, so they have
			// to catch up with the new geometry before the next frame. Mark it
			// rather than syncing here: syncing walks every node in the tree and
			// reapplies the layout, and motion events outnumber frames. A resize
			// the BSP tree drove needs none of this - the ratios are already what
			// the geometry was built from.
			if config.SharedBorders && !treeInSync {
				o.MarkBSPSyncPending()
			}
		} else if o.UseScrollingLayout {
			// Scrolling mode: compute width from horizontal drag delta. All
			// strip math runs against the content width beside the sidebar band,
			// matching scrollingSetPositions.
			viewW := o.ScrollingViewWidth()
			switch o.ResizeCorner {
			case app.TopLeft, app.BottomLeft:
				newWidth = o.PreResizeState.Width - xOffset
			case app.TopRight, app.BottomRight:
				newWidth = o.PreResizeState.Width + xOffset
			}
			maxWidth := viewW * 9 / 10
			newWidth = max(min(newWidth, maxWidth), config.DefaultWindowWidth)

			// Update column width and reposition all windows visually.
			sl := o.GetOrCreateScrollingLayout()
			intID := o.GetWindowIntID(focusedWindow.ID)
			oldWidth := 0
			for ci := range sl.Columns {
				for _, wid := range sl.Columns[ci].WindowIDs {
					if wid == intID {
						oldWidth = sl.ResolveColumnWidth(ci, viewW)
						sl.Columns[ci].FixedWidth = newWidth
						sl.Columns[ci].Proportion = 0
					}
				}
			}
			// For left-edge resize, shift viewport so the right edge stays fixed
			if (o.ResizeCorner == app.TopLeft || o.ResizeCorner == app.BottomLeft) && oldWidth > 0 {
				sl.ViewportX += newWidth - oldWidth
			}
			sl.ClampViewport(viewW)
			layouts := sl.ComputePositions(viewW, o.GetUsableHeight(), o.GetTopMargin())
			stripLeft := o.GetLeftMargin()
			for winID, rect := range layouts {
				win := o.GetWindowByIntID(winID)
				if win == nil {
					continue
				}
				win.X = stripLeft + rect.X
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
