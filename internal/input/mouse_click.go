package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	uv "github.com/charmbracelet/ultraviolet"
)

// handleMouseClick handles mouse click events
func handleMouseClick(msg tea.MouseClickMsg, o *app.OS) (*app.OS, tea.Cmd) {
	mouse := msg.Mouse()
	X := mouse.X
	Y := mouse.Y

	// An open context menu is modal to the mouse: it either runs the row that
	// was clicked or, for a click anywhere else, closes without running
	// anything. Either way the click stops here, so it cannot also focus a pane
	// or start a drag underneath the menu.
	if o.ContextMenuActive() {
		if action, consumed := o.ContextMenuClick(X, Y); consumed {
			return runContextMenuAction(action, o)
		}
	}

	// Ctrl or shift + right-click opens the context menu on a pane, on the press
	// itself. This is the only way in over a pane whose app requested mouse
	// tracking, where the right button belongs to that app; elsewhere a plain
	// right-click reaches the menu too, decided on release once it is clear the
	// gesture was not a resize drag (handleMouseRelease).
	if msg.Button == tea.MouseRight && msg.Mod&(tea.ModShift|tea.ModCtrl) != 0 {
		o.OpenContextMenu(X, Y)
		return o, nil
	}

	// A rename in flight is modal to the mouse: a click on the dialog does
	// nothing, a click anywhere else cancels. Ahead of the overlays because the
	// dialog is anchored to its target rather than stacked with them.
	if o.RenameMouseClick(X, Y) {
		return o, nil
	}

	// Floating overlay panels (help, settings, palette, theme picker) consume
	// clicks before they can reach the window layer: select a tab/row/control,
	// grab the title bar or right-drag to move, or click away to dismiss.
	if o.OverlayActive() {
		if handled, cmd := o.OverlayMouseClick(X, Y, msg.Button == tea.MouseRight); handled {
			return o, cmd
		}
	}

	// The sidebar is a reserved-region panel like the dock: a click anywhere in
	// its band is the sidebar's (focus a window, switch or expand a session, or,
	// on right-click, open the context menu), never the pane it sits in front of.
	if o.SidebarActive() && o.SidebarClick(X, Y, msg.Button == tea.MouseRight) {
		return o, nil
	}

	// A click outside the rail is intent to leave it: the pane the user clicked
	// wins over keyboard rail focus. Rail focus is cleared here so the click below
	// focuses the pane normally.
	if o.SidebarFocused {
		o.ExitSidebarFocus()
	}

	// Check if click is in the dock area (always reserved).
	//
	// The top test is exclusive for the same reason the bottom one is: a dock of
	// DockHeight rows at the top occupies rows 0 to DockHeight-1, so an inclusive
	// test claims one row too many, and that row is the first row of the topmost
	// window. With a top dock and any minimized window, an ordinary click on that
	// row was being swallowed as a dock click.
	if ((config.DockbarPosition == "bottom") && (Y >= o.Height-config.DockHeight)) || ((config.DockbarPosition == "top") && (Y < config.DockHeight)) {
		// A plain right-click on the dock opens its menu (the dock item's menu
		// when one is under the pointer). The dock has no drag gesture on the
		// right button, so the menu can open on the press itself.
		if msg.Button == tea.MouseRight {
			o.OpenContextMenu(X, Y)
			return o, nil
		}
		// The session controls are the bar's outermost block and are tested
		// first. Leaving happens on the click; closing only ever raises its
		// confirmation, whatever the session is holding.
		switch o.DockSessionActionAt(X, Y) {
		case app.DockSessionLeave:
			if m, cmd, detached := detachSession(o); detached {
				return m, cmd
			}
			return o, nil
		case app.DockSessionClose:
			o.OpenSessionClose()
			return o, nil
		}
		// The message block owns its own columns at the bar's right-hand end,
		// ahead of the dock items: its body jumps to the pane the message came
		// from, its right-hand end dismisses.
		if o.NotificationClick(X, Y) {
			return o, nil
		}
		// The strip's overflow arrows sit in the strip's own columns and are
		// tested before its pills, so a gutter is an arrow and never the pill
		// that was under it before the strip started scrolling.
		if o.ScrollDockWorkspacesAt(X, Y) {
			return o, nil
		}
		// The workspace strip owns its columns in the left region, ahead of the
		// dock items: the two never overlap, but the strip is tested first so a
		// tab stays a tab whatever the item layout does.
		//
		// A press on a pill only arms the gesture. The switch it might be is
		// delivered on a release that never left the pill, because switching
		// retiles the panes and moves the strip, so a press that switched would
		// pull the pill out from under a drag that had just started. The "+" is
		// not a workspace and cannot be dragged, so it takes its press directly.
		if o.BeginDockWorkspaceDrag(X, Y) {
			return o, nil
		}
		if ws := o.DockWorkspaceAt(X, Y); ws > 0 {
			o.SwitchToWorkspace(ws)
			return o, nil
		}
		// The marker standing for the entries that did not fit opens the panel
		// that lists them, so a pane the bar had no room for is still reachable
		// by mouse.
		if o.DockOverflowAt(X, Y) {
			o.OpenAggregateView()
			return o, nil
		}
		// Handle dock click only if there are minimized windows
		if o.HasMinimizedWindows() {
			dockIndex := o.DockItemAt(X, Y)
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

	// A committed ctrl-drag ends on the next mouse event without ctrl held, so a
	// click arriving mid-drag drops the window where it sits.
	if o.CtrlDragging && msg.Mod&tea.ModCtrl == 0 {
		return finalizeCtrlDrag(o, X, Y)
	}

	// Fast hit testing - find which window was clicked without expensive canvas generation
	clickedWindowIndex := findClickedWindow(X, Y, o)

	// Ctrl + left press on a window: multi-select on a click, or grab the pane
	// for moving on a drag. On the content it arms the click-vs-drag decision
	// (committed past the threshold in handleMouseMotion, then moved through the
	// same path as a title-bar drag); a sub-threshold release falls through to
	// the ctrl+click multi-select. On the border or title bar, where a drag
	// already means resize or a title-bar move, it stays the immediate toggle.
	// ctrl+shift+left-click toggles multi-select. Plain ctrl+left instead grabs the
	// window: on content it arms a move-drag (committed past the threshold in
	// handleMouseMotion), off the content it just focuses. Keeping multi-select on
	// its own chord stops a ctrl-click from selecting when the intent was to grab.
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
			return o, nil
		}
		o.FocusWindow(clickedWindowIndex)
		return o, nil
	}

	// Alt + left press grabs the pane for moving, the gesture nearly every
	// desktop window manager binds and so the one a newcomer's hands try first.
	// It sits beside ctrl+drag rather than replacing it: ctrl+drag is in the help
	// and in people's fingers, and a move gesture costs nothing to have twice.
	//
	// It grabs at once instead of arming a threshold the way ctrl+drag does,
	// because ctrl+left already means multi-select and alt+left means nothing
	// else, so there is no click-versus-drag question to answer. Grabbing at once
	// also sets Dragging on the press, which is what keeps the motion filter in
	// cmd/tuios from dropping the very motion that would move the pane.
	//
	// Placed above the guest forwarding below, exactly as ctrl+drag is, so the
	// gesture works over a pane whose app asked for mouse tracking. That does
	// take alt-drag away from such an app, which is what alt_drag = false hands
	// back.
	if clickedWindowIndex != -1 && msg.Button == tea.MouseLeft &&
		msg.Mod&tea.ModAlt != 0 && msg.Mod&(tea.ModCtrl|tea.ModShift) == 0 &&
		config.AltDrag && !o.Windows[clickedWindowIndex].Zoomed {
		o.BeginPointerGesture()
		beginWindowDrag(o, clickedWindowIndex, X, Y)
		return o, nil
	}

	// Scrollbar click: left press on cells the bar was drawn on. The bar floats
	// in the pane's last content column and only while the pane is scrolled
	// back, so the grab is gated on the rect the renderer recorded - otherwise a
	// click on the rightmost content cell of a pane with history would
	// jump-scroll instead of reaching the guest.
	if clickedWindowIndex != -1 && msg.Button == tea.MouseLeft {
		win := o.Windows[clickedWindowIndex]
		if rect, drawn := o.ScrollbarHit(win); drawn && rect.Contains(X, Y) {
			o.FocusWindow(clickedWindowIndex)
			o.ScrollbarGrabOffset = scrollbarGrab(win, rect, Y)
			o.ScrollbarDragging = true
			o.ScrollbarDragWindowIndex = clickedWindowIndex
			o.InteractionMode = true
			o.Dragging = true
			o.DraggedWindowIndex = clickedWindowIndex
			return o, nil
		}
	}

	// Left press on a pane border starts an additive resize: a tiled pane's
	// shared divider or a floating pane's own edge. It never grabs content, the
	// sidebar band, or the dock, and leaves the ctrl/shift+right menu and the
	// plain-right-press resize untouched.
	if msg.Button == tea.MouseLeft && msg.Mod == 0 && armBorderResize(X, Y, o) {
		return o, nil
	}

	// Terminal mode, plain right-click: a context menu only when there is an
	// active text selection to act on (copy, paste, clear). Without one the
	// click belongs to the pane, so it falls through to the forwarding below
	// and mouse-mode apps still get it.
	if clickedWindowIndex != -1 && o.Mode == app.TerminalMode &&
		msg.Button == tea.MouseRight && msg.Mod == 0 {
		if o.Windows[clickedWindowIndex].HasSelection() {
			o.OpenSelectionMenu(X, Y, clickedWindowIndex)
			return o, nil
		}
	}

	// Forward mouse events to terminal if in terminal mode and window has mouse tracking
	if clickedWindowIndex != -1 && o.Mode == app.TerminalMode {
		clickedWindow := o.Windows[clickedWindowIndex]
		// Forward mouse only when app explicitly requested mouse tracking (DECSET 1000-1003)
		if clickedWindow.Terminal != nil && clickedWindow.Terminal.HasMouseMode() {
			// Convert to terminal-relative coordinates (0-based)
			termX, termY, inContent := clickedWindow.ScreenToTerminal(X, Y)
			// Check if click is within terminal content area
			if inContent {
				// Focus the window first so subsequent events work
				o.FocusWindow(clickedWindowIndex)

				// Create adjusted mouse event with terminal-relative coordinates
				adjustedMouse := uv.MouseClickEvent{
					X:      termX,
					Y:      termY,
					Button: uv.MouseButton(mouse.Button),
					Mod:    uv.KeyMod(mouse.Mod),
				}
				// Send to the terminal (uses PTY for daemon windows)
				sendMouseToWindow(clickedWindow, adjustedMouse)
				return o, nil
			}
		}
	}
	// Terminal mode, plain right-click that was neither a selection menu nor
	// forwarded to a mouse-mode app: consumed, so it cannot fall through and
	// start a window resize underneath the user's shell.
	if clickedWindowIndex != -1 && o.Mode == app.TerminalMode &&
		msg.Button == tea.MouseRight && msg.Mod == 0 {
		// A finger has no ctrl and no shift, so a long press is the only right
		// click a phone can make, and dropping it here left the pane menu
		// unreachable from the mode a user spends their time in. The menu is
		// also the only finger-sized way to close, zoom or split a pane, since
		// the title bar's own buttons are one row tall. A pointer keeps the old
		// contract: it reaches the same menu with ctrl or shift held.
		if o.TouchClient {
			o.OpenContextMenu(X, Y)
		}
		return o, nil
	}

	if clickedWindowIndex == -1 {
		// A plain right-click on empty desktop opens the desktop menu; there is
		// no desktop drag on the right button, so it opens on the press itself.
		if msg.Button == tea.MouseRight && msg.Mod == 0 && o.Mode == app.WindowManagementMode {
			o.OpenContextMenu(X, Y)
		}
		// Consume the event even if no window is hit to prevent leaking
		return o, nil
	}

	clickedWindow := o.Windows[clickedWindowIndex]

	// Window controls come first, before mode switching or focus changes.
	//
	// The cells are whichever ones the renderer recorded as it drew the pill:
	// the styles differ in glyph, width and order, the pill goes to whichever
	// end appearance.window_button_position names, and the corner glyph it
	// stops short of is itself configurable. The offsets this used to measure
	// from the window rectangle had drifted a column off the drawn pill, so a
	// press on the corner closed the window and a press on the minimize glyph's
	// own leading pad did nothing.
	if mouse.Button == tea.MouseLeft {
		if action, ok := o.WindowButtonIn(clickedWindow.ID, X, Y); ok {
			switch action {
			case app.WindowButtonClose:
				o.DeleteWindow(clickedWindowIndex)
			case app.WindowButtonMinimize:
				o.MinimizeWindow(clickedWindowIndex)
			case app.WindowButtonZoom:
				o.Snap(clickedWindowIndex, app.SnapFullScreen)
			case app.WindowButtonNone:
				return o, nil
			}
			o.InteractionMode = false
			return o, nil
		}
	}

	// Text selection with the mouse, AFTER the button checks.
	//
	// A left press inside a pane's content selects text, in terminal mode as
	// well as in copy mode. It used to only select in copy mode: in terminal
	// mode the same press grabbed the window, dragged it, and dropped the user
	// into window management mode, so click-dragging over output moved the pane
	// instead of selecting the line. The title bar and the borders are still
	// the window's drag handle, and window management mode still drags from
	// anywhere, so nothing lost a way to move a window.
	//
	// Panes running an application with mouse tracking never reach here; their
	// press was forwarded to the application further up, exactly as the wheel is.
	if mouse.Button == tea.MouseLeft && (clickedWindow.InCopyMode() || o.Mode == app.TerminalMode) {
		terminalX, terminalY, inContent := clickedWindow.ScreenToTerminal(X, Y)
		if inContent {
			o.FocusWindow(clickedWindowIndex)
			if !clickedWindow.InCopyMode() {
				// Selection reads through copy mode's machinery, so a
				// selection in terminal mode has to turn it on. Implicitly:
				// nothing is announced and the dock does not change.
				clickedWindow.EnterCopyModeImplicit()
			}
			// Any press retires a clipboard write that has not landed yet:
			// this press may be the third of a triple-click, in which case
			// the word the double-click was about to copy is not what the
			// user is selecting.
			o.CancelPendingCopy()
			o.SelectionDragged = false
			beginMouseSelection(clickedWindow.CopyMode, clickedWindow, X, Y,
				registerClick(clickedWindow, terminalX, terminalY))
			o.Dragging = true
			o.DraggedWindowIndex = clickedWindowIndex
			o.InteractionMode = true
			return o, nil
		}
		// A press outside the content area falls through to normal window
		// interaction: that is the title bar, and it should still drag.
	}

	// Focus the clicked window and bring to front Z-index
	// This happens AFTER button and copy mode checks
	//
	// A right press starts a resize, which borrows window-management mode. The
	// borrow is taken here rather than beside the resize flags below, which run
	// after the flip: by then there is no terminal mode left for
	// BeginPointerGesture to notice, and a resize begun while typing gave back
	// window management instead of the shell.
	if mouse.Button == tea.MouseRight {
		o.BeginPointerGesture()
	}
	o.FocusWindow(clickedWindowIndex)
	if o.Mode == app.TerminalMode {
		o.Mode = app.WindowManagementMode
	}

	// A left press on the content area in window-management mode arms
	// click-to-type: released without a drag it enters terminal mode
	// (handleMouseRelease), so clicking a pane is enough to start typing.
	// Title bar and border presses never arm it, so they stay pure drag
	// handles, and the press itself still sets up the drag below.
	//
	// How many clicks that takes, or whether a click changes mode at all, is
	// appearance.click_to_type. Only the arming moves: the drag threshold, the
	// release and the mode switch itself are the same for every policy, so a
	// click that does not arm is an ordinary window-management click.
	//
	// "double" reads its second click from the counter word and line selection
	// already keep, so a double click means one thing everywhere. Arming spends
	// the count, leaving the first press in the terminal mode it opened to
	// start a fresh selection instead of landing as a third click on a line.
	// The clicks that changed mode select nothing themselves: entering a mode
	// is not a request to put a word on the clipboard.
	if mouse.Button == tea.MouseLeft && o.Mode == app.WindowManagementMode &&
		config.ClickToType != config.ClickToTypeOff {
		if termX, termY, inContent := clickedWindow.ScreenToTerminal(X, Y); inContent {
			arm := true
			if config.ClickToType == config.ClickToTypeDouble {
				arm = registerClick(clickedWindow, termX, termY) >= 2
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

	// A plain right press arms the click-vs-drag decision: the resize state set
	// up below makes a drag resize, and the release opens the context menu
	// instead when the pointer never moved past the threshold
	// (handleMouseRelease). Armed before the zoom check so a zoomed pane,
	// which cannot be resized, still gets its menu on the click.
	if mouse.Button == tea.MouseRight && msg.Mod == 0 {
		o.RightClickPending = true
		o.RightPressX, o.RightPressY = X, Y
	}

	// Zoomed windows are immune to drag/resize  - skip interaction state setup.
	// The click still focuses the window (already done above) but no drag/resize starts.
	if clickedWindow.Zoomed {
		return o, nil
	}

	// Set interaction mode to prevent expensive rendering during drag/resize
	o.InteractionMode = true

	// Calculate drag offset based on the clicked window
	o.DragOffsetX = X - clickedWindow.X
	o.DragOffsetY = Y - clickedWindow.Y

	switch mouse.Button {
	case tea.MouseRight:
		// Already in interaction mode and past the mode borrow above, now set
		// resize-specific flags.
		o.Resizing = true
		o.DraggedWindowIndex = clickedWindowIndex
		o.Windows[clickedWindowIndex].IsBeingManipulated = true
		o.ResizeStartX = mouse.X
		o.ResizeStartY = mouse.Y
		// Save state for resize calculations (avoid mutex copying)
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

		// Set precise resize cursor based on corner
		switch o.ResizeCorner {
		case app.TopLeft, app.BottomRight:
			app.SetPointerShape(app.PointerNWSEResize)
		case app.TopRight, app.BottomLeft:
			app.SetPointerShape(app.PointerNESWResize)
		}

	case tea.MouseLeft:
		beginWindowDrag(o, clickedWindowIndex, mouse.X, mouse.Y)
	}
	return o, nil
}

// beginWindowDrag puts a window into the move gesture the title-bar drag uses:
// it focuses and grabs the pane at (x, y), untiles it for free rendering, and
// in tiling mode records the slot to swap back into. The title-bar press and a
// committed ctrl-drag both route through here, so the two share one movement
// path (motion in handleMouseMotion, drop in handleMouseRelease). Motion moves
// the focused window, so the grab focuses it; the mode switch keeps a drag from
// forwarding motion to a mouse-mode app underneath.
func beginWindowDrag(o *app.OS, idx, x, y int) {
	win := o.Windows[idx]
	o.FocusWindow(idx)
	if o.Mode == app.TerminalMode {
		o.Mode = app.WindowManagementMode
	}
	app.SetPointerShape(app.PointerGrabbing)
	o.InteractionMode = true
	o.Dragging = true
	o.DraggedWindowIndex = idx
	o.DragStartX, o.DragStartY = x, y
	o.DragOffsetX = x - win.X
	o.DragOffsetY = y - win.Y
	win.IsBeingManipulated = true
	// Temporarily untile for border rendering during drag.
	if win.Tiled {
		win.Tiled = false
		win.Resize(win.Width, win.Height)
	}
	// In tiling mode (non-scrolling), complete pending animations to avoid state
	// conflicts, then record the slot for the swap-on-release. Scrolling mode
	// doesn't drag windows, so let its slide animations play.
	if o.AutoTiling && !o.UseScrollingLayout {
		o.CompleteAllAnimations()
		o.TiledX = win.X
		o.TiledY = win.Y
		o.TiledWidth = win.Width
		o.TiledHeight = win.Height
	}
}

// finalizeCtrlDrag drops a committed ctrl-drag at (x, y) by running the normal
// left-button release path, so the tiling swap and floating snap behave exactly
// as a title-bar drag's release does.
func finalizeCtrlDrag(o *app.OS, x, y int) (*app.OS, tea.Cmd) {
	o.CtrlDragging = false
	return handleMouseRelease(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft}, o)
}
