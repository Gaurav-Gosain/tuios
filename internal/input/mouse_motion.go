package input

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	uv "github.com/charmbracelet/ultraviolet"
	"tuios.dev/vt"
)

func guestWantsMotion(term vt.Terminal, button tea.MouseButton) bool {
	if term.HasAllMotionMode() {
		return true
	}
	return term.HasCellMotionMode() && button != tea.MouseNone
}

func handleMouseMotion(msg tea.MouseMotionMsg, o *app.OS) (*app.OS, tea.Cmd) {
	mouse := msg.Mouse()

	o.LastMouseX, o.LastMouseY = o.ScreenPoint(mouse.X, mouse.Y)

	if mouse.Button == tea.MouseNone {
		o.EndPointerGrabs()
		o.QuickLookBarRelease()
		o.QuickLookLetGo()
	}
	if o.QuickLookBarDrag(o.LastMouseX, o.LastMouseY) {
		return o, nil
	}

	if o.DnDMotion(mouse.X, mouse.Y, mouse.Button) {
		return o, nil
	}

	if o.ContextMenuActive() {
		o.ContextMenuHover(mouse.X, mouse.Y)
		return o, nil
	}
	if o.PathPickerOpen() {
		o.PathPickerHover(mouse.X, mouse.Y)
		return o, nil
	}

	if o.SidebarDragActive() {
		o.SidebarDragMotion(mouse.X, mouse.Y)
		return o, nil
	}
	if o.FileDragActive() {
		o.FileDragMotion(mouse.X, mouse.Y, mouse.Mod.Contains(tea.ModAlt))
		return o, nil
	}
	if o.SidebarEdgeActive() {
		o.SidebarEdgeMotion(mouse.X, mouse.Y)
		return o, nil
	}
	if o.SidebarSplitActive() {
		o.SidebarSplitMotion(mouse.X, mouse.Y)
		return o, nil
	}

	if o.DockWorkspaceDragActive() {
		o.DockWorkspaceDragMotion(mouse.X, mouse.Y)
		return o, nil
	}

	if o.OverlayDragActive() {
		o.OverlayMouseMotion(mouse.X, mouse.Y)
		return o, nil
	}
	if o.DraftDrag(mouse.X, mouse.Y) {
		return o, nil
	}
	if o.QuickLookTakesMotion(mouse.X, mouse.Y) {
		return o, nil
	}
	if !o.Dragging && !o.Resizing && !o.ScrollbarDragging {
		if o.OverlayMouseMotion(mouse.X, mouse.Y) {
			o.DropLinkHover()
			return o, nil
		}
		if w := o.SidebarSessionPaneAt(mouse.X, mouse.Y); w != nil {
			o.DropLinkHover()
			if paneWantsMotion(o, w, mouse.Button) {
				if tx, ty, inside := w.ScreenToTerminal(mouse.X, mouse.Y); inside {
					sendMouseToWindow(o, w, uv.MouseMotionEvent{X: tx, Y: ty, Button: uv.MouseButton(mouse.Button), Mod: uv.KeyMod(mouse.Mod)})
				}
			}
			return o, nil
		}
		if o.SidebarActive() && o.SidebarMotion(mouse.X, mouse.Y) {
			o.DropLinkHover()
			o.ResetPointerShape()
			return o, nil
		}
	}

	o.DockSessionHoverAt(mouse.X, mouse.Y)

	o.DockButtonHoverAt(mouse.X, mouse.Y)

	o.DockWorkspaceHoverAt(mouse.X, mouse.Y)

	o.DockNotifHoverAt(mouse.X, mouse.Y)

	o.WindowButtonHoverAt(mouse.X, mouse.Y)

	if o.CtrlDragPending {
		if mouse.Mod&tea.ModCtrl == 0 {
			o.CtrlDragPending = false
			if url := o.CtrlClickLink; url != "" {
				o.CtrlClickLink = ""
				return o, o.OpenLink(url)
			}
			if o.CtrlDragIndex >= 0 && o.CtrlDragIndex < len(o.Windows) {
				o.ToggleMultifocus(o.CtrlDragIndex)
			}
			return o, nil
		}
		if abs(mouse.X-o.DragStartX)+abs(mouse.Y-o.DragStartY) >= o.DragThreshold() {
			o.CtrlDragPending = false
			o.CtrlClickLink = ""
			o.CtrlDragging = true
			o.BeginCtrlDrag()
			beginWindowDrag(o, o.CtrlDragIndex, o.DragStartX, o.DragStartY)
		}
	}
	if o.CtrlDragging && mouse.Mod&tea.ModCtrl == 0 {
		return finalizeCtrlDrag(o, mouse.X, mouse.Y)
	}

	o.UpdatePointerForPosition(mouse.X, mouse.Y)

	o.TrackLinkPointer(mouse.X, mouse.Y)

	if o.Settings.FocusFollowsMouse && !focusFollowsMousePaused(o.Settings.FocusFollowsMousePause, mouse.Mod) &&
		!o.SidebarFocused &&
		!o.Dragging && !o.Resizing && !o.ScrollbarDragging &&
		!o.AnyOverlayOpen() && !o.ContextMenuActive() &&
		!o.SidebarBandContains(mouse.X, mouse.Y) && !o.InDockBand(mouse.Y) {
		idx := findClickedWindow(mouse.X, mouse.Y, o)
		if idx >= 0 && pipOwns(mouse.X, mouse.Y, o) {
			idx = -1
		}
		if idx >= 0 && idx != o.FocusedWindow {
			o.FocusWindow(idx)
			o.RevealHoveredColumn()
		}
	}

	if o.Mode == app.TerminalMode && (mouse.Button != tea.MouseNone || !pipOwns(mouse.X, mouse.Y, o)) {
		focusedWindow := o.GetFocusedWindow()
		if focusedWindow != nil && focusedWindow.Terminal != nil {
			if paneWantsMotion(o, focusedWindow, mouse.Button) {
				termX, termY, inContent := focusedWindow.ScreenToTerminal(mouse.X, mouse.Y)
				if inContent {
					adjustedMouse := uv.MouseMotionEvent{
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
	}

	if o.ScrollbarDragging && o.ScrollbarDragWindowIndex >= 0 && o.ScrollbarDragWindowIndex < len(o.Windows) {
		win := o.Windows[o.ScrollbarDragWindowIndex]
		scrollToThumbRow(win, mouse.Y-o.ScrollbarGrabOffset, &o.Settings)
		return o, nil
	}

	if o.Dragging && o.DraggedWindowIndex >= 0 && o.DraggedWindowIndex < len(o.Windows) {
		draggedWindow := o.Windows[o.DraggedWindowIndex]
		if draggedWindow.CopyMode != nil && draggedWindow.CopyMode.Active {
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
		if o.DraggedWindowIndex >= 0 && o.DraggedWindowIndex < len(o.Windows) {
			focusedWindow = o.Windows[o.DraggedWindowIndex]
		}
		untilePaneForDrag(focusedWindow)
		newX := mouse.X - o.DragOffsetX
		newY := mouse.Y - o.DragOffsetY

		minVisibleX := 20
		minVisibleY := 3

		leftMargin := o.PaneLeft()
		contentRight := leftMargin + o.PaneWidth()

		if newX < leftMargin-(focusedWindow.Width-minVisibleX) {
			newX = leftMargin - (focusedWindow.Width - minVisibleX)
		}

		if newX > contentRight-minVisibleX {
			newX = contentRight - minVisibleX
		}

		topMargin := o.PaneTop()
		if newY < topMargin-(focusedWindow.Height-minVisibleY) {
			newY = topMargin - (focusedWindow.Height - minVisibleY)
		}

		maxY := topMargin + o.PaneHeight() - minVisibleY
		if newY > maxY {
			newY = maxY
		}

		focusedWindow.X = newX
		focusedWindow.Y = newY
		focusedWindow.MarkPositionDirty()
		if tiledDragDistance(o, mouse.X, mouse.Y) >= tiledDragThreshold {
			return o, o.UpdateDropZone(mouse.X, mouse.Y)
		}
		o.ClearDropZone()
		return o, nil
	}

	if o.Resizing && o.InteractionMode {
		if o.RightClickPending && abs(mouse.X-o.RightPressX)+abs(mouse.Y-o.RightPressY) >= o.DragThreshold() {
			o.RightClickPending = false
		}
		if o.BorderResizing {
			applyBorderResize(o, mouse.X, mouse.Y)
			return o, nil
		}

		if o.DraggedWindowIndex >= 0 && o.DraggedWindowIndex < len(o.Windows) {
			focusedWindow = o.Windows[o.DraggedWindowIndex]
		}

		xOffset := mouse.X - o.ResizeStartX
		yOffset := mouse.Y - o.ResizeStartY

		newX := focusedWindow.X
		newY := focusedWindow.Y
		newWidth := focusedWindow.Width
		newHeight := focusedWindow.Height

		tiled := o.ResizesWithTiling(focusedWindow)

		if tiled && o.UseScrollingLayout {
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

		leftMargin := o.PaneLeft()
		contentRight := leftMargin + o.PaneWidth()

		if newX < leftMargin {
			if o.ResizeCorner == app.TopLeft || o.ResizeCorner == app.BottomLeft {
				newWidth += newX - leftMargin
			}
			newX = leftMargin
		}

		topMargin := o.PaneTop()
		if newY < topMargin {
			if o.ResizeCorner == app.TopLeft || o.ResizeCorner == app.TopRight {
				newHeight += newY - topMargin
			}
			newY = topMargin
		}

		if newX+newWidth > contentRight {
			if o.ResizeCorner == app.TopRight || o.ResizeCorner == app.BottomRight {
				newWidth = contentRight - newX
			} else {
				newX = contentRight - newWidth
			}
		}

		maxY := topMargin + o.PaneHeight()
		if newY+newHeight > maxY {
			if o.ResizeCorner == app.BottomLeft || o.ResizeCorner == app.BottomRight {
				newHeight = maxY - newY
			} else {
				newY = maxY - newHeight
			}
		}

		newWidth = max(newWidth, config.DefaultWindowWidth)
		newHeight = max(newHeight, config.DefaultWindowHeight)
		newWidth = min(newWidth, contentRight-newX)
		newHeight = min(newHeight, maxY-newY)

		if tiled && !o.UseScrollingLayout {
			const edgeTolerance = 2

			atLeftEdge := focusedWindow.X <= leftMargin+edgeTolerance
			atRightEdge := (focusedWindow.X + focusedWindow.Width) >= (contentRight - edgeTolerance)
			atTopEdge := focusedWindow.Y <= edgeTolerance
			atBottomEdge := (focusedWindow.Y + focusedWindow.Height) >= (maxY - edgeTolerance)

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

			treeInSync := o.AdjustTilingNeighborsVisual(focusedWindow, newX, newY, newWidth, newHeight)
			if o.SharedBorders && !treeInSync {
				o.MarkBSPSyncPending()
			}
		} else if tiled && o.UseScrollingLayout {
			switch o.ResizeCorner {
			case app.TopLeft, app.BottomLeft:
				newWidth = o.PreResizeState.Width - xOffset
			case app.TopRight, app.BottomRight:
				newWidth = o.PreResizeState.Width + xOffset
			}
			keepRight := o.ResizeCorner == app.TopLeft || o.ResizeCorner == app.BottomLeft
			o.ScrollingResizeColumnVisual(focusedWindow, newWidth, keepRight)
		} else {
			focusedWindow.X = newX
			focusedWindow.Y = newY
			focusedWindow.ResizeVisual(newWidth, newHeight)
			focusedWindow.MarkPositionDirty()
			o.PendingResizes[focusedWindow.ID] = [2]int{newWidth, newHeight}
		}

		return o, nil
	}

	return o, nil
}

func focusFollowsMousePaused(key string, mod tea.KeyMod) bool {
	switch key {
	case config.FocusFollowsMousePauseCtrl:
		return mod&tea.ModCtrl != 0
	case config.FocusFollowsMousePauseAlt:
		return mod&tea.ModAlt != 0
	}
	return false
}
