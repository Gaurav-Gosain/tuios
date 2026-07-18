package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	uv "github.com/charmbracelet/ultraviolet"
)

// handleMouseWheel handles mouse wheel events
func handleMouseWheel(msg tea.MouseWheelMsg, o *app.OS) (*app.OS, tea.Cmd) {
	// Scroll the floating overlay panel under the cursor (help, settings,
	// palette, theme picker, session/layout lists).
	if o.OverlayActive() {
		wm := msg.Mouse()
		if o.OverlayMouseWheel(wm.X, wm.Y, msg.Button == tea.MouseWheelUp) {
			return o, nil
		}
	}

	if o.ShowLogs {
		_, maxScroll := logScrollBounds(o.Height, len(o.LogMessages))

		switch msg.Button {
		case tea.MouseWheelUp:
			if o.LogScrollOffset > 0 {
				o.LogScrollOffset--
			}
		case tea.MouseWheelDown:
			if o.LogScrollOffset < maxScroll {
				o.LogScrollOffset++
			}
		}
		return o, nil
	}

	// Alt+scroll or Shift+scroll in scrolling tiling mode: scroll the viewport left/right
	if o.AutoTiling && o.UseScrollingLayout {
		mouse := msg.Mouse()
		if mouse.Mod&(tea.ModAlt|tea.ModShift) != 0 {
			dir := 1
			if config.NiriReverseScroll {
				dir = -1
			}
			switch msg.Button {
			case tea.MouseWheelUp:
				o.ScrollingScrollViewport(-1 * dir)
			case tea.MouseWheelDown:
				o.ScrollingScrollViewport(1 * dir)
			}
			return o, nil
		}
		// Also intercept horizontal scroll events (MouseWheelLeft/Right) if available
		switch msg.Button {
		case tea.MouseWheelLeft:
			o.ScrollingScrollViewport(-1)
			return o, nil
		case tea.MouseWheelRight:
			o.ScrollingScrollViewport(1)
			return o, nil
		}
	}

	// Forward mouse wheel to terminal if in terminal mode and window has mouse tracking
	// This allows applications like vim, less, htop to handle their own scrolling
	if o.Mode == app.TerminalMode {
		focusedWindow := o.GetFocusedWindow()
		if focusedWindow != nil && focusedWindow.Terminal != nil && focusedWindow.Terminal.HasMouseMode() {
			mouse := msg.Mouse()
			// Convert to terminal-relative coordinates (0-based)
			termX, termY, inContent := focusedWindow.ScreenToTerminal(mouse.X, mouse.Y)
			// Check if wheel is within terminal content area
			if inContent {
				adjustedMouse := uv.MouseWheelEvent{
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

	// Handle scrollback in terminal mode or selection mode
	if o.Mode == app.TerminalMode || o.SelectionMode {
		focusedWindow := o.GetFocusedWindow()
		if focusedWindow != nil {
			switch msg.Button {
			case tea.MouseWheelUp:
				if o.SelectionMode {
					// In selection mode, scroll without entering scrollback mode
					if focusedWindow.Terminal != nil {
						scrollbackLen := focusedWindow.ScrollbackLen()
						if scrollbackLen > 0 && focusedWindow.ScrollbackOffset < scrollbackLen {
							focusedWindow.ScrollbackOffset += 3
							if focusedWindow.ScrollbackOffset > scrollbackLen {
								focusedWindow.ScrollbackOffset = scrollbackLen
							}
							focusedWindow.InvalidateCache()
						}
					}
				} else if o.Mode == app.TerminalMode && focusedWindow.Terminal != nil && !focusedWindow.Terminal.HasMouseMode() && !focusedWindow.IsAltScreen() {
					// No mouse tracking and not alt screen  - enter copy mode and scroll.
					// Copy mode supports selection, search, and vim navigation.
					if focusedWindow.CopyMode == nil || !focusedWindow.CopyMode.Active {
						focusedWindow.EnterCopyMode()
						o.ShowNotification("COPY MODE (hjkl/q)", "info", config.NotificationDuration)
					}
					if focusedWindow.CopyMode != nil && focusedWindow.CopyMode.Active {
						for range 3 {
							MoveUp(focusedWindow.CopyMode, focusedWindow)
						}
						focusedWindow.InvalidateCache()
					}
				} else if focusedWindow.CopyMode != nil && focusedWindow.CopyMode.Active {
					// Already in copy mode  - scroll up
					for range 3 {
						MoveUp(focusedWindow.CopyMode, focusedWindow)
					}
					focusedWindow.InvalidateCache()
				}
				return o, nil
			case tea.MouseWheelDown:
				if o.SelectionMode {
					// In selection mode, scroll without entering scrollback mode
					if focusedWindow.ScrollbackOffset > 0 {
						focusedWindow.ScrollbackOffset -= 3
						if focusedWindow.ScrollbackOffset < 0 {
							focusedWindow.ScrollbackOffset = 0
						}
						focusedWindow.InvalidateCache()
					}
				} else if focusedWindow.CopyMode != nil && focusedWindow.CopyMode.Active {
					// In copy mode, scroll down
					for range 3 {
						MoveDown(focusedWindow.CopyMode, focusedWindow)
					}
					// Exit copy mode if at bottom
					if focusedWindow.CopyMode.ScrollOffset == 0 && focusedWindow.CopyMode.CursorY >= focusedWindow.Height-3 {
						focusedWindow.ExitCopyMode()
						o.ShowNotification("Copy Mode Exited", "info", config.NotificationDuration)
					}
					focusedWindow.InvalidateCache()
				}
				return o, nil
			}
		}
	}

	// Handle scrollback in window management mode too
	if o.Mode == app.WindowManagementMode {
		focusedWindow := o.GetFocusedWindow()
		if focusedWindow != nil && focusedWindow.Terminal != nil && !focusedWindow.IsAltScreen() {
			switch msg.Button {
			case tea.MouseWheelUp:
				scrollbackLen := focusedWindow.ScrollbackLen()
				if scrollbackLen > 0 {
					if focusedWindow.CopyMode == nil || !focusedWindow.CopyMode.Active {
						focusedWindow.EnterCopyMode()
						o.ShowNotification("COPY MODE (hjkl/q)", "info", config.NotificationDuration)
					}
					if focusedWindow.CopyMode != nil && focusedWindow.CopyMode.Active {
						for range 3 {
							MoveUp(focusedWindow.CopyMode, focusedWindow)
						}
						focusedWindow.InvalidateCache()
					}
				}
			case tea.MouseWheelDown:
				if focusedWindow.CopyMode != nil && focusedWindow.CopyMode.Active {
					for range 3 {
						MoveDown(focusedWindow.CopyMode, focusedWindow)
					}
					if focusedWindow.CopyMode.ScrollOffset == 0 && focusedWindow.CopyMode.CursorY >= focusedWindow.ContentHeight()-1 {
						focusedWindow.ExitCopyMode()
					}
					focusedWindow.InvalidateCache()
				}
			}
		}
	}

	return o, nil
}
