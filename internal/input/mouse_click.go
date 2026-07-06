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

	// Handle quit confirmation dialog clicks
	if o.ShowQuitConfirm {
		// Dialog is centered on screen
		dialogW, dialogH := 26, 7 // approximate dialog dimensions
		dialogX := (o.GetRenderWidth() - dialogW) / 2
		dialogY := (o.GetRenderHeight() - dialogH) / 2

		// Check if click is inside the dialog
		if X >= dialogX && X < dialogX+dialogW && Y >= dialogY && Y < dialogY+dialogH {
			// Button row is near the bottom of the dialog
			buttonY := dialogY + dialogH - 3
			if Y >= buttonY && Y < buttonY+2 {
				midX := dialogX + dialogW/2
				if X < midX {
					// Clicked "Yes" (left side)
					if o.IsDaemonSession && o.DaemonClient != nil {
						_ = o.DaemonClient.KillSession()
					}
					o.Cleanup()
					return o, tea.Quit
				} else {
					// Clicked "No" (right side)
					o.ShowQuitConfirm = false
					return o, nil
				}
			}
		} else {
			// Clicked outside dialog - dismiss it
			o.ShowQuitConfirm = false
		}
		return o, nil
	}

	// Check if click is in the dock area (always reserved)
	if ((config.DockbarPosition == "bottom") && (Y >= o.Height-config.DockHeight)) || ((config.DockbarPosition == "top") && (Y <= config.DockHeight)) {
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
	if clickedWindowIndex == -1 {
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

	// Handle copy mode mouse clicks AFTER button checks
	if clickedWindow.CopyMode != nil && clickedWindow.CopyMode.Active {
		// In copy mode, handle mouse clicks for cursor movement and selection
		if mouse.Button == tea.MouseLeft {
			// Check if clicking in terminal content area (not on title bar or buttons)
			_, _, inContent := clickedWindow.ScreenToTerminal(X, Y)
			if inContent {
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
