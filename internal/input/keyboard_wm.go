package input

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// HandleWindowManagementModeKey handles keyboard input in window management mode
func HandleWindowManagementModeKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	focusedWindow := o.GetFocusedWindow()

	// A wheel scroll here leaves the pane in an implicit copy mode too, and a
	// window-manager binding must not be swallowed by it. Any key ends the
	// scrolled view and is then handled as the window-manager command it is.
	if focusedWindow != nil && focusedWindow.InImplicitCopyMode() {
		focusedWindow.ExitCopyMode()
	}

	// Handle copy mode (vim-style scrollback/selection) - takes priority
	if focusedWindow.InCopyMode() {
		return HandleCopyModeKey(msg, o, focusedWindow)
	}

	// Handle scrollback browser overlay
	if o.ShowScrollbackBrowser {
		return HandleScrollbackBrowserKey(msg, o)
	}

	// Handle theme picker overlay (opens on top of settings)
	if o.ShowThemePicker {
		return handleThemePickerInput(msg, o)
	}

	// Handle the keybind manager (opens over settings, like the theme picker)
	if o.ShowKeybindManager {
		return handleKeybindManagerInput(msg, o)
	}

	// Handle settings overlay
	if o.ShowSettings {
		return handleSettingsInput(msg, o)
	}

	// Handle layout picker overlay
	if o.ShowLayoutPicker {
		return handleLayoutPickerInput(msg, o)
	}

	// Handle command palette overlay
	if o.ShowCommandPalette {
		return handleCommandPaletteInput(msg, o)
	}

	// Handle launcher overlay
	if o.ShowLauncher {
		return handleLauncherInput(msg, o)
	}

	// Handle session switcher overlay
	if o.ShowSessionSwitcher {
		return handleSessionSwitcherInput(msg, o)
	}

	// Handle workspace switcher overlay
	if o.ShowWorkspaceSwitcher {
		return handleWorkspaceSwitcherInput(msg, o)
	}

	// Handle aggregate view overlay
	if o.ShowAggregateView {
		return handleAggregateViewInput(msg, o)
	}

	key := msg.String()

	// Handle help menu interactions before general keybind dispatch
	if o.ShowHelp {
		// Handle escape - exit search first if active, then close help
		if key == "esc" || key == "q" || key == "?" {
			if o.HelpSearchMode {
				// Exit search mode first
				o.HelpSearchMode = false
				o.HelpSearchQuery = ""
				o.HelpScrollOffset = 0
				return o, nil
			}
			// Close help menu
			o.ShowHelp = false
			o.HelpScrollOffset = 0
			o.HelpCategory = -1
			o.HelpSearchQuery = ""
			o.HelpSearchMode = false
			return o, nil
		}

		// Handle up/down arrows for scrolling
		// Scroll by 2 rows at a time (1 entry + 1 gap row)
		if key == "up" {
			if o.HelpScrollOffset > 0 {
				o.HelpScrollOffset -= 2
				if o.HelpScrollOffset < 0 {
					o.HelpScrollOffset = 0
				}
			}
			return o, nil
		}
		if key == "down" {
			o.HelpScrollOffset += 2
			return o, nil
		}

		// Handle left/right arrows for category navigation (reset scroll)
		if key == "left" {
			o.HelpScrollOffset = 0
			return handleLeftKey(msg, o)
		}
		if key == "right" {
			o.HelpScrollOffset = 0
			return handleRightKey(msg, o)
		}

		// Toggle search mode with "/"
		if key == "/" {
			o.HelpSearchMode = !o.HelpSearchMode
			o.HelpScrollOffset = 0 // Reset scroll when toggling search
			if !o.HelpSearchMode {
				o.HelpSearchQuery = "" // Clear query when exiting search
			}
			return o, nil
		}

		// Handle typing in search mode
		if o.HelpSearchMode {
			// Handle backspace
			if key == "backspace" {
				if len(o.HelpSearchQuery) > 0 {
					o.HelpSearchQuery = o.HelpSearchQuery[:len(o.HelpSearchQuery)-1]
					o.HelpScrollOffset = 0 // Reset scroll when query changes
				}
				return o, nil
			}

			// Handle regular character input (single printable characters)
			if len(key) == 1 && key[0] >= 32 && key[0] <= 126 {
				o.HelpSearchQuery += key
				o.HelpScrollOffset = 0 // Reset scroll when query changes
				return o, nil
			}
		}

		// Help is showing but the key wasn't handled - ignore it, exactly as
		// terminal mode does. Falling through sent it on to the window-manager
		// dispatch below, where the overlay hides what it does: n created and
		// focused a window behind the help panel, x closed one, t toggled
		// tiling, and every other single-letter binding fired unseen. Help is
		// the only overlay in this function that was not modal.
		return o, nil
	}

	// Handle log viewer (takes priority in window management mode)
	if o.ShowLogs {
		return handleLogViewerKey(msg, o)
	}

	// Handle cache stats viewer (takes priority in window management mode)
	if o.ShowCacheStats {
		// Close cache stats with q, esc, or c
		if key == "q" || key == "esc" || key == "c" {
			o.ShowCacheStats = false
			return o, nil
		}

		// Reset cache stats with r
		if key == "r" {
			app.GetGlobalStyleCache().ResetStats()
			o.ShowNotification("Cache statistics reset", "info", 2*time.Second)
			return o, nil
		}

		// Ignore other keys when cache stats is active
		return o, nil
	}

	// Try config-based dispatch first (if registry is available)
	if o.KeybindRegistry != nil {
		action := lookupAction(msg, o.KeybindRegistry.GetAction)
		if action != "" {
			dispatcher := GetDispatcher()
			if dispatcher.HasAction(action) {
				return dispatcher.Dispatch(action, msg, o)
			}
		}
	}

	// The direct terminal-mode binds (window cycling) are honoured here too, so
	// the same chord cycles windows in both modes.
	if handleTerminalModeBinds(msg, o) {
		return o, nil
	}

	// The global scope (palette, launcher) acts here and in terminal mode alike.
	if m, cmd, ok := handleGlobalBinds(msg, o); ok {
		return m, cmd
	}

	// Ctrl+C is the last resort, and it is a fallback rather than an override:
	// the registry dispatch above has already had the key, so a user who binds
	// ctrl+c to something of their own gets it. What is not configurable is
	// ctrl+c doing nothing, which is the point of keeping it.
	switch key {
	case "ctrl+c":
		// Same routing as the quit keybinding, so in a daemon session it opens
		// the quit menu (detach is the default) rather than silently killing
		// anything.
		return requestQuit(o)

	default:
		// All other keybindings are handled by the config system above
		// Workspace switching (opt+1-9, opt+shift+1-9) is now fully configurable
		// The KeyNormalizer handles macOS unicode character expansion (¡, ™, £, etc.)
		// If a key isn't bound in the config, it does nothing (which is correct behavior)
		return o, nil
	}
}
