package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	uv "github.com/charmbracelet/ultraviolet"
)

// handleMouseRelease handles mouse release events
func handleMouseRelease(msg tea.MouseReleaseMsg, o *app.OS) (*app.OS, tea.Cmd) {
	// A button coming up ends any gesture it started, whoever else claims the
	// event. Every branch below can return early, and each one that fired while
	// a resize was live left it running. Deferred so it covers all of them, and
	// idempotent so the cleanup at the bottom stays the normal way a gesture
	// ends. See OS.EndStrayGesture. The mode the gesture borrowed goes back the
	// same way, for the same reason.
	defer func() {
		o.EndStrayGesture()
		o.EndPointerGesture()
	}()

	// Armed by the cleanup below and returned from whichever branch gets there.
	var settleCmd tea.Cmd

	// End every overlay gesture before anything else. Both a grabbed panel and
	// the accent picker's grab on its grid or hue strip end on this button, but
	// only a panel drag consumes the release. Ending them together matters
	// because the picker's grab used to be cleared only when a panel happened to
	// be under drag as well, so a press in the picker left the colour following
	// the pointer for the rest of the dialog's life.
	panelDrag := o.OverlayDragActive()
	o.OverlayMouseRelease()
	if panelDrag {
		return o, nil
	}

	// Reset pointer shape on release
	app.ResetPointerShape()

	// A sidebar session gesture resolves on release: commit a reorder drag, or
	// deliver the plain click (switch / toggle) the press deferred.
	if o.SidebarDragActive() {
		mouse := msg.Mouse()
		o.SidebarRelease(mouse.X, mouse.Y)
		return o, nil
	}
	if o.SidebarEdgeActive() {
		mouse := msg.Mouse()
		o.SidebarEdgeRelease(mouse.X, mouse.Y)
		return o, nil
	}

	// A workspace pill gesture resolves here too: commit the arrangement the
	// pointer built, or deliver the switch the press deferred.
	if o.DockWorkspaceDragActive() {
		mouse := msg.Mouse()
		if _, cmd := o.DockWorkspaceDragRelease(mouse.X, mouse.Y); cmd != nil {
			return o, cmd
		}
		return o, nil
	}

	// A ctrl+left press that never passed the drag threshold was a stray grab, not
	// a move; clear it and stop. Multi-select lives on ctrl+shift+click, handled on
	// press. A committed ctrl-drag falls through to the normal window-drop below.
	if o.CtrlDragPending {
		o.CtrlDragPending = false
		return o, nil
	}
	o.CtrlDragging = false
	// A ctrl-drag that started in terminal mode gives typing back when the pane
	// lands: moving a window is not a request to stop typing. Deferred because
	// the drop leaves through several returns, all of which retile first.
	if o.CtrlDragWasTerminal {
		o.CtrlDragWasTerminal = false
		defer func() { o.Mode = app.TerminalMode }()
	}

	// A plain right press on a pane arms a resize. Below the drag threshold it
	// was a click: cancel the resize (restoring the few cells of jitter it may
	// have applied) and open the pane menu at the press position. A pane whose
	// app requested mouse tracking keeps the old contract, no menu without
	// ctrl/shift, because the right button belongs to that app. At or past the
	// threshold the resize completes below.
	if o.RightClickPending {
		o.RightClickPending = false
		mouse := msg.Mouse()
		if abs(mouse.X-o.RightPressX)+abs(mouse.Y-o.RightPressY) < rightClickDragThreshold {
			pressX, pressY := o.RightPressX, o.RightPressY
			appOwnsMouse := false
			if idx := findClickedWindow(pressX, pressY, o); idx != -1 {
				win := o.Windows[idx]
				appOwnsMouse = win.Terminal != nil && win.Terminal.HasMouseMode()
			}
			cancelRightClickResize(o)
			if !appOwnsMouse {
				o.OpenContextMenu(pressX, pressY)
			}
			return o, nil
		}
	}

	// A left press on a pane's content in window-management mode armed
	// click-to-type. Decide now, while the press coordinates are still intact;
	// the drag-completion branches below zero them. The mode switch itself
	// happens at the end so a sub-threshold tiling drag still snaps back first.
	enterTerminalOnRelease := false
	if o.ClickToTypePending {
		o.ClickToTypePending = false
		mouse := msg.Mouse()
		if mouse.Button == tea.MouseLeft && o.Mode == app.WindowManagementMode &&
			abs(mouse.X-o.DragStartX)+abs(mouse.Y-o.DragStartY) < clickToTypeDragThreshold {
			enterTerminalOnRelease = true
		}
	}
	// Forward mouse release to terminal if in terminal mode and window has mouse tracking
	if o.Mode == app.TerminalMode {
		focusedWindow := o.GetFocusedWindow()
		if focusedWindow != nil && focusedWindow.Terminal != nil && focusedWindow.Terminal.HasMouseMode() {
			mouse := msg.Mouse()
			// Convert to terminal-relative coordinates (0-based)
			termX, termY, inContent := focusedWindow.ScreenToTerminal(mouse.X, mouse.Y)
			// Check if release is within terminal content area
			if inContent {
				adjustedMouse := uv.MouseReleaseEvent{
					X:      termX,
					Y:      termY,
					Button: uv.MouseButton(mouse.Button),
					Mod:    uv.KeyMod(mouse.Mod),
				}
				sendMouseToWindow(focusedWindow, adjustedMouse)
				return o, nil
			}
		}
	}

	// Clear scrollbar drag
	if o.ScrollbarDragging {
		o.ScrollbarDragging = false
		o.ScrollbarDragWindowIndex = -1
		o.ScrollbarGrabOffset = 0
		o.Dragging = false
		o.InteractionMode = false
		o.DraggedWindowIndex = -1
		return o, nil
	}

	// Handle copy mode mouse release
	if o.Dragging && o.DraggedWindowIndex >= 0 && o.DraggedWindowIndex < len(o.Windows) {
		draggedWindow := o.Windows[o.DraggedWindowIndex]
		if draggedWindow.InCopyMode() {
			// Selection is complete: copy it if the user wants that, then clean
			// up drag state and stop auto-scroll.
			cmd := finishMouseSelection(o, draggedWindow)
			draggedWindow.InvalidateCache()
			o.Dragging = false
			o.DraggedWindowIndex = -1
			o.InteractionMode = false
			o.AutoScrollActive = false
			o.AutoScrollDir = 0
			return o, cmd
		}
	}

	// Handle window drop in tiling mode (drag-to-swap only, NOT resize)
	if o.Dragging && o.AutoTiling && !o.Resizing && o.DraggedWindowIndex >= 0 && o.DraggedWindowIndex < len(o.Windows) {
		mouse := msg.Mouse()

		// Calculate drag distance to determine if this was actually a drag or just a click
		dragDistance := abs(mouse.X-o.DragStartX) + abs(mouse.Y-o.DragStartY)
		const dragThreshold = 5 // pixels - must move at least this much to be considered a drag

		draggedWindow := o.Windows[o.DraggedWindowIndex]

		// Floating windows: no snap-back
		if draggedWindow.IsFloating {
			o.DraggedWindowIndex = -1
		} else if o.UseScrollingLayout {
			// Scrolling mode: windows don't move during drag.
			// For actual drags, check if cursor ended on a different window for swap.
			if dragDistance >= dragThreshold {
				sl := o.GetOrCreateScrollingLayout()
				draggedIntID := o.GetWindowIntID(draggedWindow.ID)
				for i := range o.Windows {
					if i == o.DraggedWindowIndex || o.Windows[i].Minimized || o.Windows[i].IsFloating || o.Windows[i].Workspace != o.CurrentWorkspace {
						continue
					}
					w := o.Windows[i]
					if mouse.X >= w.X && mouse.X < w.X+w.Width && mouse.Y >= w.Y && mouse.Y < w.Y+w.Height {
						targetIntID := o.GetWindowIntID(w.ID)
						dragCol, targetCol := -1, -1
						for ci, col := range sl.Columns {
							for _, wid := range col.WindowIDs {
								if wid == draggedIntID {
									dragCol = ci
								}
								if wid == targetIntID {
									targetCol = ci
								}
							}
						}
						if dragCol >= 0 && targetCol >= 0 && dragCol != targetCol {
							sl.Columns[dragCol], sl.Columns[targetCol] = sl.Columns[targetCol], sl.Columns[dragCol]
							sl.FocusedCol = targetCol
							o.ScrollingSetPositions()
						}
						break
					}
				}
			}
			o.DraggedWindowIndex = -1
		} else if dragDistance >= dragThreshold {
			// This was an actual drag, check for swap
			// Find which window is under the cursor (excluding the dragged window)
			targetWindowIndex := -1
			for i := range o.Windows {
				if i == o.DraggedWindowIndex || o.Windows[i].Minimized || o.Windows[i].Minimizing || o.Windows[i].IsFloating {
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
				// No swap - snap dragged window back to its original tiled position
				// Immediately set window back to tiled position to prevent layout corruption
				draggedWindow.X = o.TiledX
				draggedWindow.Y = o.TiledY
				draggedWindow.Width = o.TiledWidth
				draggedWindow.Height = o.TiledHeight
				draggedWindow.Resize(o.TiledWidth, o.TiledHeight)
				draggedWindow.MarkPositionDirty()
				draggedWindow.InvalidateCache()
			}
		} else {
			// Drag distance below threshold - snap back to prevent layout corruption from micro-drags
			// Even small mouse movements can displace the window during motion events
			draggedWindow.X = o.TiledX
			draggedWindow.Y = o.TiledY
			draggedWindow.Width = o.TiledWidth
			draggedWindow.Height = o.TiledHeight
			draggedWindow.Resize(o.TiledWidth, o.TiledHeight)
			draggedWindow.MarkPositionDirty()
			draggedWindow.InvalidateCache()
		}
		o.DraggedWindowIndex = -1
	}

	// Handle window edge snapping in floating mode (non-tiling)
	if o.Dragging && !o.AutoTiling && o.DraggedWindowIndex >= 0 && o.DraggedWindowIndex < len(o.Windows) {
		mouse := msg.Mouse()
		dragDistance := abs(mouse.X-o.DragStartX) + abs(mouse.Y-o.DragStartY)
		const dragThreshold = 5

		if dragDistance >= dragThreshold {
			// Detect edge zones for snapping
			// Edge zone is within edgeSize pixels of screen edge
			const edgeSize = 5
			topMargin := o.GetTopMargin()
			usableHeight := o.GetUsableHeight()
			bottomEdge := topMargin + usableHeight

			atLeft := mouse.X <= edgeSize
			atRight := mouse.X >= o.Width-edgeSize
			atTop := mouse.Y <= topMargin+edgeSize
			atBottom := mouse.Y >= bottomEdge-edgeSize

			snapTo := app.NoSnap

			if atTop && !atLeft && !atRight {
				// Top center - fullscreen
				snapTo = app.SnapFullScreen
			} else if atLeft && !atTop && !atBottom {
				// Left middle - snap left half
				snapTo = app.SnapLeft
			} else if atRight && !atTop && !atBottom {
				// Right middle - snap right half
				snapTo = app.SnapRight
			} else if atTop && atLeft {
				// Top-left corner - quarter
				snapTo = app.SnapTopLeft
			} else if atTop && atRight {
				// Top-right corner - quarter
				snapTo = app.SnapTopRight
			} else if atBottom && atLeft {
				// Bottom-left corner - quarter
				snapTo = app.SnapBottomLeft
			} else if atBottom && atRight {
				// Bottom-right corner - quarter
				snapTo = app.SnapBottomRight
			}

			if snapTo != app.NoSnap {
				o.Snap(o.DraggedWindowIndex, snapTo)
			}
		}
		o.DraggedWindowIndex = -1
	}

	// Clean up interaction state on mouse release
	if o.Dragging || o.Resizing {
		wasResizing := o.Resizing
		// Save the dragged/resized window index before anything clears it
		resizedWindowIndex := o.DraggedWindowIndex
		o.Dragging = false
		o.Resizing = false
		// A finished pane-border drag joins the same flush/sync cleanup below.
		o.BorderResizing = false
		o.BorderResizeEdge = app.BorderEdgeNone

		// Apply all pending PTY resizes that were deferred during drag/resize
		if wasResizing && len(o.PendingResizes) > 0 {
			for i := range o.Windows {
				if dimensions, exists := o.PendingResizes[o.Windows[i].ID]; exists {
					o.Windows[i].Resize(dimensions[0], dimensions[1])
				}
			}
			o.PendingResizes = make(map[string][2]int)
			o.FlushPTYBuffersAfterResize()
		}

		// In scrolling mode, capture resized width into the column BEFORE retiling
		if wasResizing && o.AutoTiling && o.UseScrollingLayout {
			if resizedWindowIndex >= 0 && resizedWindowIndex < len(o.Windows) {
				win := o.Windows[resizedWindowIndex]
				sl := o.GetOrCreateScrollingLayout()
				intID := o.GetWindowIntID(win.ID)
				for ci := range sl.Columns {
					for _, wid := range sl.Columns[ci].WindowIDs {
						if wid == intID {
							sl.Columns[ci].FixedWidth = win.Width
							sl.Columns[ci].Proportion = 0
						}
					}
				}
			}
		}

		// Mark layout as custom if resizing in tiling mode (BSP only)
		if wasResizing && o.AutoTiling && !o.UseScrollingLayout {
			o.MarkLayoutCustom()
			o.SyncBSPTreeFromGeometry()
		}

		for i := range o.Windows {
			o.Windows[i].IsBeingManipulated = false
			o.Windows[i].ContentDirty = true
			o.Windows[i].CachedLayer = nil
		}

		// Re-tile / re-layout after drag or resize.
		// For scrolling mode: only on actual resize (avoid viewport reset on click).
		// For BSP shared borders: always re-tile to restore the Tiled flag that
		// was temporarily cleared during drag setup (line 327).
		if wasResizing && o.AutoTiling && o.UseScrollingLayout {
			o.ScrollingSetPositions()
		} else if o.AutoTiling && config.SharedBorders && !o.UseScrollingLayout {
			o.TileAllWindows()
		}

		// Announce the finished resize. Firing here rather than during the drag
		// means one event per gesture carrying the size the window settled at,
		// instead of one per mouse-motion event carrying intermediate sizes.
		if wasResizing && resizedWindowIndex >= 0 && resizedWindowIndex < len(o.Windows) {
			o.FireResized(o.Windows[resizedWindowIndex])
		}

		// Comprehensive state cleanup to prevent stale values from affecting subsequent operations
		o.DragOffsetX = 0
		o.DragOffsetY = 0
		o.ResizeStartX = 0
		o.ResizeStartY = 0
		o.DragStartX = 0
		o.DragStartY = 0
		o.DraggedWindowIndex = -1

		// Clear interaction mode with a delay to allow shell prompts to fully redraw.
		// This gives shells like bash/zsh/starship time to:
		// 1. Receive SIGWINCH signal
		// 2. Query new terminal dimensions
		// 3. Recalculate and redraw the prompt for the new width
		// 4. Write the new prompt to the PTY
		// Without this delay, content polling resumes before the shell finishes,
		// resulting in incomplete or stale prompt displays. The wait comes back
		// through Update (InteractionSettledMsg) so the field is only ever
		// written on the loop that owns it.
		if wasResizing {
			settleCmd = app.InteractionSettleCmd()
		} else {
			o.InteractionMode = false
		}

		// Sync state to daemon after drag/resize completes
		// This ensures window positions persist across reconnects
		o.SyncStateToDaemon()
	} else {
		// Even if we weren't dragging/resizing, clear interaction mode from click
		o.InteractionMode = false
	}

	// Mouse edge snapping disabled - use keyboard shortcuts for snapping

	// Click-to-type: the press was on a pane's content and never became a drag,
	// so finish what a newcomer expects a click to do. The dispatcher runs the
	// same handler the Enter keybinding runs, notification included.
	if enterTerminalOnRelease {
		next, cmd := GetDispatcher().Dispatch("enter_terminal_mode", tea.KeyPressMsg{}, o)
		return next, tea.Batch(settleCmd, cmd)
	}

	return o, settleCmd
}

// rightClickDragThreshold and clickToTypeDragThreshold separate a click from a
// drag, in summed cell movement, matching the drag threshold the left-button
// window drag already uses.
const (
	rightClickDragThreshold  = 5
	clickToTypeDragThreshold = 5
	ctrlDragThreshold        = 5
)

// cancelRightClickResize unwinds the resize a plain right press set up, so the
// release can open the context menu instead. The press may have applied a few
// cells of visual resize (motion events resize immediately); geometry is
// restored from the state saved at press time, and in tiling mode the layout
// is retiled only when something actually moved.
func cancelRightClickResize(o *app.OS) {
	idx := o.DraggedWindowIndex
	o.Resizing = false
	o.Dragging = false
	o.InteractionMode = false
	o.PendingResizes = make(map[string][2]int)
	if idx >= 0 && idx < len(o.Windows) {
		win := o.Windows[idx]
		win.IsBeingManipulated = false
		moved := win.X != o.PreResizeState.X || win.Y != o.PreResizeState.Y ||
			win.Width != o.PreResizeState.Width || win.Height != o.PreResizeState.Height
		if moved {
			win.X, win.Y = o.PreResizeState.X, o.PreResizeState.Y
			win.ResizeVisual(o.PreResizeState.Width, o.PreResizeState.Height)
			win.MarkPositionDirty()
			win.InvalidateCache()
			if o.AutoTiling && !o.UseScrollingLayout {
				// Neighbors may have been nudged by the shared-border resize;
				// retiling snaps every pane back to the tree's layout.
				o.TileAllWindows()
			}
		}
	}
	o.DraggedWindowIndex = -1
	o.ResizeStartX, o.ResizeStartY = 0, 0
}
