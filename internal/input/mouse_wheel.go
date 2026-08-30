package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	uv "github.com/charmbracelet/ultraviolet"
)

// handleMouseWheel handles mouse wheel events
func handleMouseWheel(msg tea.MouseWheelMsg, o *app.OS) (*app.OS, tea.Cmd) {
	// Scroll the floating overlay panel under the cursor (help, settings,
	// palette, theme picker, session/layout lists).
	if o.OverlayActive() {
		wm := msg.Mouse()
		if o.OverlayMouseWheel(wm.X, wm.Y, msg.Button == tea.MouseWheelUp) {
			// A wheel over the launcher scrolls rows into view whose icons have
			// not been decoded yet. Every other panel answers nil here.
			return o, o.LauncherIconWork()
		}
	}

	// Wheel over the sidebar band scrolls the sidebar list, never the pane the
	// sidebar sits in front of.
	if o.SidebarActive() {
		wm := msg.Mouse()
		if o.SidebarWheel(wm.X, wm.Y, msg.Button == tea.MouseWheelUp) {
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
			if o.Settings.NiriReverseScroll {
				dir = -1
			}
			switch msg.Button {
			case tea.MouseWheelUp:
				o.ScrollingScrollViewport(-1 * dir)
			case tea.MouseWheelDown:
				o.ScrollingScrollViewport(1 * dir)
			// A horizontal wheel moves the viewport under the same modifier as
			// a vertical one, rather than on its own. A trackpad puts a little
			// sideways drift into almost every vertical scroll and the terminal
			// forwards that as a left/right wheel button, so answering it
			// unmodified walked the whole layout sideways whenever someone
			// scrolled back through a pane.
			case tea.MouseWheelLeft:
				o.ScrollingScrollViewport(-1)
			case tea.MouseWheelRight:
				o.ScrollingScrollViewport(1)
			}
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
				// One physical notch scrolls one step in the guest, which feels
				// sluggish in apps that scroll a small amount per wheel event
				// (browsers especially). Send config.ScrollLines events per notch
				// so a notch covers the same distance as scrollback scrolling,
				// tunable through the existing scroll-speed setting.
				reps := max(o.Settings.ScrollLines, 1)
				for range reps {
					sendMouseToWindow(focusedWindow, adjustedMouse)
				}
				return o, nil
			}
		}
	}

	// Handle scrollback in terminal mode
	if o.Mode == app.TerminalMode {
		focusedWindow := o.GetFocusedWindow()
		if focusedWindow != nil {
			switch msg.Button {
			case tea.MouseWheelUp:
				if focusedWindow.InCopyMode() {
					// Already in copy mode  - scroll up
					scrollCopyModeUp(focusedWindow, &o.Settings)
				} else if o.Mode == app.TerminalMode && focusedWindow.Terminal != nil && !focusedWindow.Terminal.HasMouseMode() && !focusedWindow.IsAltScreen() && focusedWindow.ScrollbackLen() > 0 {
					// No mouse tracking, not alt screen, and there is history to
					// show: turn the wheel and the view scrolls. Copy mode is the
					// only thing that can render scrollback, so it is switched on
					// implicitly, without a notification and without the dock
					// changing mode. Panes with no scrollback are left alone
					// rather than dropped into an empty scrolled state.
					focusedWindow.EnterCopyModeImplicit()
					scrollCopyModeUp(focusedWindow, &o.Settings)
				}
				return o, nil
			case tea.MouseWheelDown:
				if focusedWindow.InCopyMode() {
					// In copy mode, scroll down
					scrollCopyModeDown(focusedWindow, &o.Settings)
					leaveCopyModeAtBottom(focusedWindow)
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
				if focusedWindow.InCopyMode() {
					scrollCopyModeUp(focusedWindow, &o.Settings)
				} else if focusedWindow.ScrollbackLen() > 0 {
					// Same silent entry as terminal mode: the wheel scrolls, it
					// does not put the pane into a mode and teach keys for it.
					focusedWindow.EnterCopyModeImplicit()
					scrollCopyModeUp(focusedWindow, &o.Settings)
				}
			case tea.MouseWheelDown:
				if focusedWindow.InCopyMode() {
					scrollCopyModeDown(focusedWindow, &o.Settings)
					leaveCopyModeAtBottom(focusedWindow)
				}
			}
		}
	}

	return o, nil
}
