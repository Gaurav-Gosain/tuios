package input

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// HandleWindowManagementModeKey handles keyboard input in window management mode
func HandleWindowManagementModeKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	focusedWindow := o.GetFocusedWindow()

	// Handle copy mode (vim-style scrollback/selection) - takes priority
	if focusedWindow != nil && focusedWindow.CopyMode != nil && focusedWindow.CopyMode.Active {
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

	// Handle session switcher overlay
	if o.ShowSessionSwitcher {
		return handleSessionSwitcherInput(msg, o)
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

	// Settings: comma opens the settings page directly in window mode. Checked
	// before the config dispatch because the default keybinds map "," to a
	// tiling resize action, which would otherwise swallow it.
	if key == "," {
		o.OpenSettings()
		return o, nil
	}

	// Try config-based dispatch first (if registry is available)
	if o.KeybindRegistry != nil {
		action := o.KeybindRegistry.GetAction(key)
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

	// Command palette: ctrl+p. Matched on the decoded key event, not the raw
	// string, so it fires under every Kitty keyboard encoding (see isCtrlP).
	if isCtrlP(msg) {
		o.ShowCommandPalette = true
		o.CommandPaletteQuery = ""
		o.CommandPaletteSelected = 0
		o.CommandPaletteScroll = 0
		return o, nil
	}

	// Emergency/safety keybindings that bypass the config system
	// Only Ctrl+C is kept as emergency quit
	switch key {
	case "ctrl+c":
		// Emergency quit - show confirmation dialog (only if there are terminals)
		if shouldShowQuitDialog(o) {
			o.ShowQuitConfirm = true
			o.QuitConfirmSelection = 0 // Default to Yes
		} else {
			// No terminals - just quit
			o.Cleanup()
			return o, tea.Quit
		}
		return o, nil

	default:
		// All other keybindings are handled by the config system above
		// Workspace switching (opt+1-9, opt+shift+1-9) is now fully configurable
		// The KeyNormalizer handles macOS unicode character expansion (¡, ™, £, etc.)
		// If a key isn't bound in the config, it does nothing (which is correct behavior)
		return o, nil
	}
}
