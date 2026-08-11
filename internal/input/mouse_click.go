package input

import (
	"fmt"
	"os"
	"time"

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

	// Ctrl or shift + right-click opens the context menu on a pane. A plain
	// right-click/drag on a pane is reserved for resize (it was too easy to open
	// the menu by accident mid-resize), so the menu needs a modifier there.
	// Empty desktop and the dock have no right-drag, so they still open the menu
	// on a plain right-click (handled below).
	if msg.Button == tea.MouseRight && msg.Mod&(tea.ModShift|tea.ModCtrl) != 0 {
		o.OpenContextMenu(X, Y)
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

	// Ctrl+Click: toggle multifocus on the clicked window
	if clickedWindowIndex != -1 && msg.Button == tea.MouseLeft && msg.Mod&tea.ModCtrl != 0 {
		o.ToggleMultifocus(clickedWindowIndex)
		return o, nil
	}

	// Scrollbar click: left click on right border of a window with scrollback
	if clickedWindowIndex != -1 && msg.Button == tea.MouseLeft {
		win := o.Windows[clickedWindowIndex]
		rightBorderX := win.X + win.Width - 1
		win.RLockIO()
		hasScrollback := win.Terminal != nil && win.Terminal.ScrollbackLen() > 0
		win.RUnlockIO()
		if X == rightBorderX && hasScrollback {
			o.FocusWindow(clickedWindowIndex)
			scrollToPosition(win, Y)
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
		win := o.Windows[clickedWindowIndex]
		if win.SelectedText != "" || win.IsSelecting {
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

	leftMost := clickedWindow.X + clickedWindow.Width

	// DEBUG: Log click attempts
	if os.Getenv("TUIOS_DEBUG_INTERNAL") == "1" {
		if f, err := os.OpenFile("/tmp/tuios-mouse-debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			_, _ = fmt.Fprintf(f, "[CLICK] X=%d Y=%d, Window X=%d Y=%d W=%d H=%d, leftMost=%d\n",
				X, Y, clickedWindow.X, clickedWindow.Y, clickedWindow.Width, clickedWindow.Height, leftMost)
			_ = f.Close()
		}
	}

	// Check button clicks FIRST before mode switching or focus changes
	// Only check if buttons are not hidden
	if !config.HideWindowButtons {
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
	o.FocusWindow(clickedWindowIndex)
	if o.Mode == app.TerminalMode {
		o.Mode = app.WindowManagementMode
	}

	// A left press on the content area in window-management mode arms
	// click-to-type: released without a drag it enters terminal mode
	// (handleMouseRelease), so clicking a pane is enough to start typing.
	// Title bar and border presses never arm it, so they stay pure drag
	// handles, and the press itself still sets up the drag below.
	if mouse.Button == tea.MouseLeft && o.Mode == app.WindowManagementMode && !o.SelectionMode {
		if _, _, inContent := clickedWindow.ScreenToTerminal(X, Y); inContent {
			o.ClickToTypePending = true
			o.DragStartX = mouse.X
			o.DragStartY = mouse.Y
		}
	}

	// A plain right press arms the click-vs-drag decision: the resize state set
	// up below makes a drag resize, and the release opens the context menu
	// instead when the pointer never moved past the threshold
	// (handleMouseRelease). Armed before the zoom check so a zoomed pane,
	// which cannot be resized, still gets its menu on the click. Modified
	// right presses stay pure resizes.
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
		// Already in interaction mode, now set resize-specific flags
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
		// Check if we're in selection mode
		if o.SelectionMode {
			// Calculate terminal coordinates relative to window content
			terminalX, terminalY, inContent := clickedWindow.ScreenToTerminal(X, Y)

			// Start text selection
			if inContent {
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

		// Set grabbing pointer during drag
		app.SetPointerShape(app.PointerGrabbing)
		// Already in interaction mode, now set drag-specific flags
		o.Dragging = true
		o.DragStartX = mouse.X
		o.DragStartY = mouse.Y
		o.Windows[clickedWindowIndex].IsBeingManipulated = true
		// Temporarily untile for border rendering during drag
		if o.Windows[clickedWindowIndex].Tiled {
			o.Windows[clickedWindowIndex].Tiled = false
			o.Windows[clickedWindowIndex].Resize(o.Windows[clickedWindowIndex].Width, o.Windows[clickedWindowIndex].Height)
		}
		o.DraggedWindowIndex = clickedWindowIndex

		// In tiling mode (non-scrolling), complete pending animations to avoid
		// state conflicts when starting a drag. Scrolling mode doesn't drag
		// windows, so let its slide animations play.
		if o.AutoTiling && !o.UseScrollingLayout {
			o.CompleteAllAnimations()

			// Store current position (after completing all animations) for tiling mode swaps
			o.TiledX = clickedWindow.X
			o.TiledY = clickedWindow.Y
			o.TiledWidth = clickedWindow.Width
			o.TiledHeight = clickedWindow.Height
		}
	}
	return o, nil
}

// selectWord selects the word at the given position
func selectWord(window *terminal.Window, x, y int, o *app.OS) {
	if window.Terminal == nil {
		return
	}

	screen := window.Terminal
	maxX := window.ContentWidth()

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
	maxX := window.ContentWidth()

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
