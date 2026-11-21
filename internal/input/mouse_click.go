package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	uv "github.com/charmbracelet/ultraviolet"
)

func handleMouseClick(msg tea.MouseClickMsg, o *app.OS) (*app.OS, tea.Cmd) {
	mouse := msg.Mouse()
	X := mouse.X
	Y := mouse.Y

	o.CancelCopyFlash()

	if o.CancelPointerGesture() {
		return o, nil
	}

	if o.ContextMenuActive() {
		if action, consumed := o.ContextMenuClick(X, Y); consumed {
			return runContextMenuAction(action, o)
		}
	}

	if msg.Button == tea.MouseLeft {
		if i := o.WelcomeChoiceAt(X, Y); i >= 0 {
			return o, o.ChooseWelcome(i)
		}
	}

	if o.PathPickerClick(X, Y) {
		return o, nil
	}

	if msg.Button == tea.MouseRight && msg.Mod&(tea.ModShift|tea.ModCtrl) != 0 && !o.QuickLookContains(X, Y) {
		o.OpenContextMenu(X, Y)
		return o, nil
	}

	if o.RenameMouseClick(X, Y) {
		return o, nil
	}

	if o.QuickLookMouseClick(X, Y) {
		quickLookCopyPress(msg, o)
		return o, nil
	}

	if o.ShowInbox {
		if taken, cmd := cockpitMousePress(msg, o); taken {
			return o, cmd
		}
	}

	if o.OverlayActive() {
		o.NoteClickShift(msg.Mod&tea.ModShift != 0)
		if handled, cmd := o.OverlayMouseClick(X, Y, msg.Button == tea.MouseRight); handled {
			return o, cmd
		}
	}

	if edge, w := o.SidebarSessionTitlePaneAt(X, Y); w != nil {
		if msg.Button == tea.MouseRight {
			o.OpenSettingsAt("Sidebar")
			return o, nil
		}
		if msg.Button == tea.MouseLeft {
			if action, ok := o.WindowButtonIn(w.ID, X, Y); ok {
				switch action {
				case app.WindowButtonClose:
					o.FocusSidebarSession(edge)
					o.SidebarSessionIntent("CloseWindow", w.ID)
				case app.WindowButtonMinimize:
					o.FocusSidebarSession(edge)
					o.SidebarSessionIntent("MinimizeWindow", w.ID)
				case app.WindowButtonZoom:
					o.FocusSidebarSession(edge)
					o.FocusSidebarSessionPane(w)
					o.ToggleSidebarSessionZoom(edge, w)
				}
				return o, nil
			}
			o.FocusSidebarSession(edge)
			o.FocusSidebarSessionPane(w)
			return o, nil
		}
	}

	if msg.Button != tea.MouseRight {
		if edge, ok := o.SidebarSessionAt(X, Y); ok {
			o.FocusSidebarSession(edge)
			if w := o.SidebarSessionPaneAt(X, Y); w != nil {
				o.FocusSidebarSessionPane(w)
				if paneTakesMouse(o, w) {
					if tx, ty, inside := w.ScreenToTerminal(X, Y); inside {
						sendMouseToWindow(o, w, uv.MouseClickEvent{X: tx, Y: ty, Button: uv.MouseButton(mouse.Button), Mod: uv.KeyMod(mouse.Mod)})
					}
				}
			}
			return o, nil
		}
	}

	o.NoteSidebarPressCopy(msg.Mod&(tea.ModCtrl|tea.ModShift) != 0)
	if o.SidebarActive() && o.SidebarClick(X, Y, msg.Button == tea.MouseRight) {
		return o, o.TakeSidebarCmd()
	}

	if o.SidebarFocused && o.FarOpenOfferAt(X, Y) && o.NotificationClick(X, Y) {
		return o, o.TakeSidebarCmd()
	}

	if o.SidebarFocused {
		o.ExitSidebarFocus()
	} else {
		o.RefoldAgentsAfterClickAway()
	}

	if o.InDockBand(Y) {
		if msg.Button == tea.MouseRight {
			o.OpenContextMenu(X, Y)
			return o, nil
		}
		switch o.DockSessionActionAt(X, Y) {
		case app.DockSessionLeave:
			if m, cmd, detached := detachSession(o); detached {
				return m, cmd
			}
			return o, nil
		case app.DockSessionClose:
			return o, o.OpenSessionClose()
		case app.DockSessionNew:
			o.OpenNewSessionPicker()
			return o, nil
		case app.DockSessionAgents:
			o.OpenAgentsFromDock()
			return o, nil
		}
		if o.NotificationClick(X, Y) {
			return o, o.TakeSidebarCmd()
		}
		if msg.Button == tea.MouseLeft && o.SpotlightChipAt(X, Y) {
			save, _ := o.TurnOffSpotlight()
			return o, save
		}
		if i := o.DockButtonAt(X, Y); i >= 0 {
			if msg.Button == tea.MouseLeft {
				if action := o.PressDockButton(i, X, Y); action != "" {
					_, cmd := RunActionByName(action, o)
					return o, cmd
				}
			}
			return o, nil
		}
		if name := o.DockCustomComponentAt(X, Y); name != "" {
			if name == config.DockComponentTransfers {
				if msg.Button == tea.MouseLeft {
					return o, o.OpenTransfers()
				}
				return o, nil
			}
			if o.RunDockComponentClick(name, int(msg.Button)) {
				return o, nil
			}
		}
		if o.ScrollDockWorkspacesAt(X, Y) {
			return o, nil
		}
		if o.BeginDockWorkspaceDrag(X, Y) {
			return o, nil
		}
		if ws := o.DockWorkspaceAt(X, Y); ws > 0 {
			o.SwitchToWorkspace(ws)
			return o, nil
		}
		if o.DockOverflowAt(X, Y) {
			o.OpenAggregateView()
			return o, nil
		}
		if o.HasMinimizedWindows() {
			dockIndex := o.DockItemAt(X, Y)
			if dockIndex != -1 {
				o.RestoreWindow(dockIndex)
				if o.AutoTiling {
					o.TileAllWindows()
				}
			}
		}
		return o, nil
	}

	clickedWindowIndex := findClickedWindow(X, Y, o)

	if next, cmd, done := scratchPress(msg, o, clickedWindowIndex, X, Y); done {
		return next, cmd
	}

	if pipOwns(X, Y, o) {
		o.PiPPressed = true
		if msg.Button == tea.MouseLeft && o.JumpToPiP() {
			o.SyncStateToDaemon()
		}
		return o, nil
	}

	if next, cmd, handled := handleTerminalMouseBinding(msg, o, clickedWindowIndex); handled {
		return next, cmd
	}

	if clickedWindowIndex != -1 && msg.Button == tea.MouseLeft &&
		msg.Mod&(tea.ModCtrl|tea.ModShift) == tea.ModCtrl|tea.ModShift {
		o.ToggleMultifocus(clickedWindowIndex)
		return o, nil
	}
	if clickedWindowIndex != -1 && msg.Button == tea.MouseLeft &&
		msg.Mod&tea.ModCtrl != 0 && msg.Mod&tea.ModShift == 0 {
		if _, _, inContent := o.Windows[clickedWindowIndex].ScreenToTerminal(X, Y); inContent {
			o.CtrlDragPending = true
			o.CtrlDragIndex = clickedWindowIndex
			o.DragStartX, o.DragStartY = X, Y
			o.CtrlClickLink = ""
			if linkClickAllows(o, tea.ModCtrl) {
				if link, ok := o.LinkAt(X, Y); ok {
					o.CtrlClickLink = link.URL
				}
			}
			return o, nil
		}
		o.FocusWindowFromClick(clickedWindowIndex, X, Y)
		return o, nil
	}

	if clickedWindowIndex != -1 && msg.Button == tea.MouseLeft &&
		msg.Mod&tea.ModAlt != 0 && msg.Mod&(tea.ModCtrl|tea.ModShift) == 0 &&
		o.Settings.AltDrag && !o.Windows[clickedWindowIndex].Zoomed {
		o.BeginPointerGesture()
		beginWindowDrag(o, clickedWindowIndex, X, Y)
		return o, nil
	}

	if clickedWindowIndex != -1 && msg.Button == tea.MouseLeft {
		win := o.Windows[clickedWindowIndex]
		if rect, drawn := o.ScrollbarHit(win); drawn && rect.Contains(X, Y) {
			o.FocusWindowFromClick(clickedWindowIndex, X, Y)
			o.ScrollbarGrabOffset = scrollbarGrab(win, rect, Y, &o.Settings)
			o.ScrollbarDragging = true
			o.ScrollbarDragWindowIndex = clickedWindowIndex
			o.InteractionMode = true
			o.Dragging = true
			o.DraggedWindowIndex = clickedWindowIndex
			return o, nil
		}
	}

	if msg.Button == tea.MouseLeft && msg.Mod == 0 && armBorderResize(X, Y, o) {
		return o, nil
	}

	if clickedWindowIndex != -1 && o.Mode == app.TerminalMode &&
		msg.Button == tea.MouseRight && msg.Mod == 0 {
		if o.Windows[clickedWindowIndex].HasSelection() {
			o.OpenSelectionMenu(X, Y, clickedWindowIndex)
			return o, nil
		}
	}

	if msg.Button == tea.MouseLeft && msg.Mod == tea.ModShift && linkClickAllows(o, tea.ModShift) {
		if link, ok := o.LinkAt(X, Y); ok {
			if clickedWindowIndex != -1 {
				o.FocusWindowFromClick(clickedWindowIndex, X, Y)
			}
			return o, o.OpenLink(link.URL)
		}
	}

	if clickedWindowIndex != -1 && o.Mode == app.TerminalMode {
		clickedWindow := o.Windows[clickedWindowIndex]
		if o.DrawsTML(clickedWindow) && o.FocusedWindow != clickedWindowIndex {
			o.FocusWindowFromClick(clickedWindowIndex, X, Y)
			return o, nil
		}
		if paneTakesMouse(o, clickedWindow) {
			termX, termY, inContent := clickedWindow.ScreenToTerminal(X, Y)
			if inContent {
				o.FocusWindowFromClick(clickedWindowIndex, X, Y)

				adjustedMouse := uv.MouseClickEvent{
					X:      termX,
					Y:      termY,
					Button: uv.MouseButton(mouse.Button),
					Mod:    uv.KeyMod(mouse.Mod),
				}
				sendMouseToWindow(o, clickedWindow, adjustedMouse)
				if mouse.Button == tea.MouseLeft {
					o.DnDNotePress(clickedWindow, X, Y, termX, termY)
				}
				return o, nil
			}
		}
	}
	if clickedWindowIndex != -1 && o.Mode == app.TerminalMode &&
		msg.Button == tea.MouseRight && msg.Mod == 0 {
		if o.TouchClient || o.Settings.RightClickOpensMenu {
			o.OpenContextMenu(X, Y)
		}
		return o, nil
	}

	if clickedWindowIndex == -1 {
		if msg.Button == tea.MouseRight && msg.Mod == 0 && o.Mode == app.WindowManagementMode {
			o.OpenContextMenu(X, Y)
		}
		return o, nil
	}

	clickedWindow := o.Windows[clickedWindowIndex]

	if mouse.Button == tea.MouseLeft {
		if action, ok := o.WindowButtonIn(clickedWindow.ID, X, Y); ok {
			switch action {
			case app.WindowButtonClose:
				if o.CloseNeedsConfirm(clickedWindowIndex) {
					o.OpenSessionCloseForWindow(clickedWindowIndex)
				} else {
					o.CloseWindowByHand(clickedWindowIndex)
				}
			case app.WindowButtonMinimize:
				o.MinimizeWindow(clickedWindowIndex)
			case app.WindowButtonFloat:
				o.FocusWindow(clickedWindowIndex)
				o.ToggleFloating()
			case app.WindowButtonZoom:
				if o.AutoTiling && !clickedWindow.IsFloating {
					o.FocusWindow(clickedWindowIndex)
					o.ToggleZoom()
				} else {
					o.Snap(clickedWindowIndex, app.SnapFullScreen)
				}
			case app.WindowButtonNone:
				return o, nil
			}
			o.InteractionMode = false
			return o, nil
		}
	}

	if mouse.Button == tea.MouseLeft && (clickedWindow.InCopyMode() || o.Mode == app.TerminalMode) {
		terminalX, terminalY, inContent := clickedWindow.ScreenToTerminal(X, Y)
		if inContent {
			o.FocusWindowFromClick(clickedWindowIndex, X, Y)
			if !clickedWindow.InCopyMode() {
				clickedWindow.EnterCopyModeImplicit()
			}
			o.CancelPendingCopy()
			o.SelectionDragged = false
			beginMouseSelection(clickedWindow.CopyMode, clickedWindow, X, Y,
				registerClick(clickedWindow, terminalX, terminalY, o.DoubleClickWindow()), &o.Settings)
			o.Dragging = true
			o.DraggedWindowIndex = clickedWindowIndex
			o.InteractionMode = true
			return o, nil
		}
	}

	if mouse.Button == tea.MouseRight {
		o.BeginPointerGesture()
	}
	o.FocusWindowFromClick(clickedWindowIndex, X, Y)
	if o.Mode == app.TerminalMode {
		o.SetBorrowedMode(app.WindowManagementMode)
	}

	if mouse.Button == tea.MouseLeft && o.Mode == app.WindowManagementMode &&
		o.Settings.ClickToType != config.ClickToTypeOff {
		if termX, termY, inContent := clickedWindow.ScreenToTerminal(X, Y); inContent {
			arm := true
			if o.Settings.ClickToType == config.ClickToTypeDouble {
				arm = registerClick(clickedWindow, termX, termY, o.DoubleClickWindow()) >= 2
				if arm {
					clickedWindow.ClickCount = 0
				}
			}
			if arm {
				o.ClickToTypePending = true
				o.DragStartX = mouse.X
				o.DragStartY = mouse.Y
			}
		}
	}

	if mouse.Button == tea.MouseRight && msg.Mod == 0 {
		o.RightClickPending = true
		o.RightPressX, o.RightPressY = X, Y
	}

	if clickedWindow.Zoomed {
		return o, nil
	}

	o.InteractionMode = true

	o.DragOffsetX = X - clickedWindow.X
	o.DragOffsetY = Y - clickedWindow.Y

	switch mouse.Button {
	case tea.MouseRight:
		o.Resizing = true
		o.DraggedWindowIndex = clickedWindowIndex
		o.Windows[clickedWindowIndex].IsBeingManipulated = true
		o.ResizeStartX = mouse.X
		o.ResizeStartY = mouse.Y
		o.PreResizeState = terminal.Window{
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

		switch o.ResizeCorner {
		case app.TopLeft, app.BottomRight:
			o.SetPointerShape(app.PointerNWSEResize)
		case app.TopRight, app.BottomLeft:
			o.SetPointerShape(app.PointerNESWResize)
		}

	case tea.MouseLeft:
		beginWindowDrag(o, clickedWindowIndex, mouse.X, mouse.Y)
	}
	return o, nil
}

func beginWindowDrag(o *app.OS, idx, x, y int) {
	win := o.Windows[idx]
	o.FocusWindowFromClick(idx, x, y)
	if o.Mode == app.TerminalMode {
		o.SetBorrowedMode(app.WindowManagementMode)
	}
	o.SetPointerShape(app.PointerGrabbing)
	o.InteractionMode = true
	o.Dragging = true
	o.DraggedWindowIndex = idx
	o.DragStartX, o.DragStartY = x, y
	o.DragOffsetX = x - win.X
	o.DragOffsetY = y - win.Y
	win.IsBeingManipulated = true
	o.ClearDropZone()
	if o.AutoTiling {
		o.CompleteAllAnimations()
		o.TiledX = win.X
		o.TiledY = win.Y
		o.TiledWidth = win.Width
		o.TiledHeight = win.Height
	}
}

func untilePaneForDrag(win *terminal.Window) {
	if !win.Tiled {
		return
	}
	win.Tiled = false
	win.Resize(win.Width, win.Height)
}

func finalizeCtrlDrag(o *app.OS, x, y int) (*app.OS, tea.Cmd) {
	o.CtrlDragging = false
	return handleMouseRelease(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft}, o)
}

func linkClickAllows(o *app.OS, mod tea.KeyMod) bool {
	switch o.Settings.LinkClick {
	case config.LinkClickOff:
		return false
	case config.LinkClickCtrl:
		return mod == tea.ModCtrl
	case config.LinkClickShift:
		return mod == tea.ModShift
	}
	return mod == tea.ModCtrl || mod == tea.ModShift
}
