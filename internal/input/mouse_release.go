package input

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	uv "github.com/charmbracelet/ultraviolet"
)

// handleMouseRelease handles mouse release events
func handleMouseRelease(msg tea.MouseReleaseMsg, o *app.OS) (*app.OS, tea.Cmd) {
	// End an in-progress overlay drag before anything else.
	if o.OverlayDragActive() {
		o.OverlayMouseRelease()
		return o, nil
	}

	// Reset pointer shape on release
	app.ResetPointerShape()
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
		o.Dragging = false
		o.InteractionMode = false
		o.DraggedWindowIndex = -1
		return o, nil
	}

	// Handle copy mode mouse release
	if o.Dragging && o.DraggedWindowIndex >= 0 && o.DraggedWindowIndex < len(o.Windows) {
		draggedWindow := o.Windows[o.DraggedWindowIndex]
		if draggedWindow.CopyMode != nil && draggedWindow.CopyMode.Active {
			// Selection is complete, clean up drag state and stop auto-scroll
			o.Dragging = false
			o.DraggedWindowIndex = -1
			o.InteractionMode = false
			o.AutoScrollActive = false
			o.AutoScrollDir = 0
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
		// resulting in incomplete or stale prompt displays.
		if wasResizing {
			go func() {
				time.Sleep(150 * time.Millisecond)
				// Only clear if no new interaction has started in the meantime
				// This prevents a race condition where a user quickly switches from
				// resizing to dragging, and the delayed goroutine would incorrectly
				// clear InteractionMode during the active drag operation.
				if !o.Dragging && !o.Resizing {
					o.InteractionMode = false
				}
			}()
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

	return o, nil
}
