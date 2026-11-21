package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	uv "github.com/charmbracelet/ultraviolet"
)

func handleMouseRelease(msg tea.MouseReleaseMsg, o *app.OS) (*app.OS, tea.Cmd) {
	defer func() {
		o.EndStrayGesture()
		o.EndCtrlDrag()
		o.EndPointerGesture()
		o.ReleaseGestureAnnouncements()
		o.ReleaseClickReveal(msg.X, msg.Y)
	}()

	if o.QuickLookBarRelease() {
		return o, nil
	}

	if o.DraftRelease() {
		o.OverlayMouseRelease()
		return o, nil
	}

	if o.PiPPressed {
		o.PiPPressed = false
		return o, nil
	}

	var settleCmd tea.Cmd

	if taken, cmd := quickLookCopyRelease(o); o.QuickLookLetGo() || taken {
		return o, cmd
	}
	if mouse := msg.Mouse(); o.ShowInbox {
		if taken, cmd := cockpitMouseRelease(o); taken {
			o.OverlayMouseRelease()
			return o, cmd
		}
		if cmd, ok := o.CockpitRelease(mouse.X, mouse.Y); ok {
			o.OverlayMouseRelease()
			return o, cmd
		}
	}
	panelDrag := o.OverlayDragActive()
	o.OverlayMouseRelease()
	if panelDrag {
		return o, nil
	}

	o.ResetPointerShape()

	if mouse := msg.Mouse(); o.DnDRelease(mouse.X, mouse.Y) {
		return o, nil
	}

	if o.FileDragActive() {
		mouse := msg.Mouse()
		o.FileDragRelease(mouse.X, mouse.Y, mouse.Mod.Contains(tea.ModAlt))
		return o, o.TakeSidebarCmd()
	}

	if mouse := msg.Mouse(); mouse.Button != tea.MouseRight {
		if w := o.SidebarSessionPaneAt(mouse.X, mouse.Y); w != nil {
			if paneTakesMouse(o, w) {
				if tx, ty, inside := w.ScreenToTerminal(mouse.X, mouse.Y); inside {
					sendMouseToWindow(o, w, uv.MouseReleaseEvent{X: tx, Y: ty, Button: uv.MouseButton(mouse.Button), Mod: uv.KeyMod(mouse.Mod)})
				}
			}
			return o, nil
		}
	}

	if o.SidebarDragActive() {
		mouse := msg.Mouse()
		o.SidebarRelease(mouse.X, mouse.Y)
		return o, o.TakeSidebarCmd()
	}
	if o.SidebarSplitActive() {
		mouse := msg.Mouse()
		o.SidebarSplitRelease(mouse.X, mouse.Y)
		return o, nil
	}
	if o.SidebarEdgeActive() {
		mouse := msg.Mouse()
		o.SidebarEdgeRelease(mouse.X, mouse.Y)
		return o, o.TakeSidebarCmd()
	}

	if o.DockWorkspaceDragActive() {
		mouse := msg.Mouse()
		if _, cmd := o.DockWorkspaceDragRelease(mouse.X, mouse.Y); cmd != nil {
			return o, cmd
		}
		return o, nil
	}

	if o.CtrlDragPending {
		o.CtrlDragPending = false
		if url := o.CtrlClickLink; url != "" {
			o.CtrlClickLink = ""
			if o.CtrlDragIndex >= 0 && o.CtrlDragIndex < len(o.Windows) {
				o.FocusWindowFromClick(o.CtrlDragIndex, o.DragStartX, o.DragStartY)
			}
			return o, o.OpenLink(url)
		}
		return o, nil
	}
	o.CtrlDragging = false

	if o.RightClickPending {
		o.RightClickPending = false
		mouse := msg.Mouse()
		if abs(mouse.X-o.RightPressX)+abs(mouse.Y-o.RightPressY) < o.DragThreshold() {
			cancelRightClickResize(o)
			o.OpenContextMenu(o.RightPressX, o.RightPressY)
			return o, nil
		}
	}

	enterTerminalOnRelease := false
	if o.ClickToTypePending {
		o.ClickToTypePending = false
		mouse := msg.Mouse()
		if mouse.Button == tea.MouseLeft && o.Mode == app.WindowManagementMode &&
			abs(mouse.X-o.DragStartX)+abs(mouse.Y-o.DragStartY) < o.DragThreshold() {
			enterTerminalOnRelease = true
		}
	}
	if terminalMouseBindingConsumesRelease(msg, o) {
		return o, nil
	}
	if o.Mode == app.TerminalMode {
		focusedWindow := o.GetFocusedWindow()
		if paneTakesMouse(o, focusedWindow) {
			mouse := msg.Mouse()
			termX, termY, inContent := focusedWindow.ScreenToTerminal(mouse.X, mouse.Y)
			if inContent {
				adjustedMouse := uv.MouseReleaseEvent{
					X:      termX,
					Y:      termY,
					Button: uv.MouseButton(mouse.Button),
					Mod:    uv.KeyMod(mouse.Mod),
				}
				sendMouseToWindow(o, focusedWindow, adjustedMouse)
				return o, nil
			}
		}
	}

	if o.ScrollbarDragging {
		o.ScrollbarDragging = false
		o.ScrollbarDragWindowIndex = -1
		o.ScrollbarGrabOffset = 0
		o.Dragging = false
		o.InteractionMode = false
		o.DraggedWindowIndex = -1
		return o, nil
	}

	if o.Dragging && o.DraggedWindowIndex >= 0 && o.DraggedWindowIndex < len(o.Windows) {
		draggedWindow := o.Windows[o.DraggedWindowIndex]
		if draggedWindow.InCopyMode() && !draggedWindow.IsBeingManipulated {
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

	if o.Dragging && o.AutoTiling && !o.Resizing && o.DraggedWindowIndex >= 0 && o.DraggedWindowIndex < len(o.Windows) {
		mouse := msg.Mouse()

		dragDistance := tiledDragDistance(o, mouse.X, mouse.Y)

		draggedWindow := o.Windows[o.DraggedWindowIndex]

		if draggedWindow.IsFloating {
			o.DraggedWindowIndex = -1
		} else if o.UseScrollingLayout {
			swapped := false
			if dragDistance >= tiledDragThreshold {
				if target := o.DropTargetAt(mouse.X, mouse.Y); target >= 0 {
					sl := o.GetOrCreateScrollingLayout()
					draggedIntID := o.GetWindowIntID(draggedWindow.ID)
					targetIntID := o.GetWindowIntID(o.Windows[target].ID)
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
						draggedWindow.IsBeingManipulated = false
						o.ScrollingSetPositions()
						swapped = true
					}
				}
			}
			if !swapped {
				snapBack(o, draggedWindow)
			}
			o.DraggedWindowIndex = -1
		} else if dragDistance >= tiledDragThreshold {
			if drop, ok := o.TiledDropAt(mouse.X, mouse.Y); ok && drop.Dir != layout.PreselectionNone {
				if !o.MoveWindowIntoSplit(draggedWindow, drop.Target, drop.Dir) {
					snapBack(o, draggedWindow)
				}
			} else if target := o.DropTargetAt(mouse.X, mouse.Y); target >= 0 {
				o.SwapWindowsWithOriginal(o.DraggedWindowIndex, target, o.TiledX, o.TiledY, o.TiledWidth, o.TiledHeight)
			} else {
				snapBack(o, draggedWindow)
			}
		} else {
			snapBack(o, draggedWindow)
		}
		o.DraggedWindowIndex = -1
	}

	if o.Dragging && !o.AutoTiling && o.DraggedWindowIndex >= 0 && o.DraggedWindowIndex < len(o.Windows) {
		mouse := msg.Mouse()
		dragDistance := abs(mouse.X-o.DragStartX) + abs(mouse.Y-o.DragStartY)
		if dragDistance >= o.DragThreshold() {
			snapTo := o.SnapZoneAt(mouse.X, mouse.Y)

			if snapTo != app.NoSnap {
				o.Snap(o.DraggedWindowIndex, snapTo)
			}
		}
		o.DraggedWindowIndex = -1
	}

	if o.Dragging || o.Resizing {
		wasResizing := o.Resizing
		resizedWindowIndex := o.DraggedWindowIndex
		o.Dragging = false
		o.Resizing = false
		o.BorderResizing = false
		o.BorderResizeEdge = app.BorderEdgeNone

		if wasResizing && len(o.PendingResizes) > 0 {
			for i := range o.Windows {
				if dimensions, exists := o.PendingResizes[o.Windows[i].ID]; exists {
					o.Windows[i].Resize(dimensions[0], dimensions[1])
				}
			}
			o.PendingResizes = make(map[string][2]int)
			o.FlushPTYBuffersAfterResize()
		}

		tiledResize := wasResizing && resizedWindowIndex >= 0 && resizedWindowIndex < len(o.Windows) &&
			o.ResizesWithTiling(o.Windows[resizedWindowIndex])

		if tiledResize && o.UseScrollingLayout {
			if resizedWindowIndex >= 0 && resizedWindowIndex < len(o.Windows) &&
				o.Windows[resizedWindowIndex].Width != o.PreResizeState.Width {
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

		if tiledResize && !o.UseScrollingLayout {
			o.SyncMasterStackFromGeometry()
			o.MarkLayoutCustom()
			o.SyncBSPTreeFromGeometry()
		}

		for i := range o.Windows {
			o.Windows[i].IsBeingManipulated = false
			o.Windows[i].ContentDirty = true
			o.Windows[i].CachedLayer = nil
		}

		if tiledResize && o.UseScrollingLayout {
			o.ScrollingSetPositions()
		} else if o.PanesBorderless() {
			o.TileAllWindows()
		}

		if wasResizing && resizedWindowIndex >= 0 && resizedWindowIndex < len(o.Windows) {
			o.FireResized(o.Windows[resizedWindowIndex])
		}

		o.DragOffsetX = 0
		o.DragOffsetY = 0
		o.ResizeStartX = 0
		o.ResizeStartY = 0
		o.DragStartX = 0
		o.DragStartY = 0
		o.DraggedWindowIndex = -1
		o.ClearDropZone()

		if wasResizing {
			settleCmd = app.InteractionSettleCmd()
		} else {
			o.InteractionMode = false
		}

		o.SyncStateToDaemon()
	} else {
		o.InteractionMode = false
	}

	if enterTerminalOnRelease {
		next, cmd := GetDispatcher().Dispatch("enter_terminal_mode", tea.KeyPressMsg{}, o)
		return next, tea.Batch(settleCmd, cmd)
	}

	return o, settleCmd
}

const tiledDragThreshold = 5

func tiledDragDistance(o *app.OS, x, y int) int {
	return abs(x-o.DragStartX) + abs(y-o.DragStartY)
}

func snapBack(o *app.OS, win *terminal.Window) {
	o.ReturnToDragSlot(win)
}

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
				o.TileAllWindows()
			}
		}
	}
	o.DraggedWindowIndex = -1
	o.ResizeStartX, o.ResizeStartY = 0, 0
}
