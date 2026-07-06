package input

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/hooks"
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

	// Alt+N/Alt+P window cycling works in both terminal and WM mode
	if handleWindowCycle(msg, o) {
		return o, nil
	}

	// Command palette: ctrl+p
	if key == "ctrl+p" {
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

// HandleWorkspacePrefixCommand handles workspace prefix commands (Ctrl+B, w, ...)
func HandleWorkspacePrefixCommand(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.WorkspacePrefixActive = false
	o.PrefixActive = false
	return handleTerminalWorkspacePrefix(msg, o)
}

// HandleMinimizePrefixCommand handles minimize prefix commands (Ctrl+B, m, ...)
func HandleMinimizePrefixCommand(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.MinimizePrefixActive = false
	o.PrefixActive = false

	// Get list of minimized windows in current workspace
	var minimizedWindows []int
	for i, win := range o.Windows {
		if win.Minimized && win.Workspace == o.CurrentWorkspace {
			minimizedWindows = append(minimizedWindows, i)
		}
	}

	switch msg.String() {
	case "m":
		// Minimize focused window
		if o.FocusedWindow >= 0 && o.FocusedWindow < len(o.Windows) {
			o.MinimizeWindow(o.FocusedWindow)
		}
		return o, nil
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		num := int(msg.String()[0] - '0')
		if num > 0 && num <= len(minimizedWindows) {
			windowIndex := minimizedWindows[num-1]
			o.RestoreWindow(windowIndex)
			// Retile if in tiling mode
			if o.AutoTiling {
				o.TileAllWindows()
			}
		}
		return o, nil
	case "shift+m", "M":
		// Restore all minimized windows
		for _, idx := range minimizedWindows {
			o.RestoreWindow(idx)
		}
		// Retile if in tiling mode
		if o.AutoTiling {
			o.TileAllWindows()
		}
		return o, nil
	case "esc":
		// Cancel minimize prefix mode
		return o, nil
	default:
		// Unknown minimize command, ignore
		return o, nil
	}
}

// HandleTilingPrefixCommand handles tiling/window prefix commands (Ctrl+B, t, ...) in window management mode
func HandleTilingPrefixCommand(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.TilingPrefixActive = false
	o.PrefixActive = false

	switch msg.String() {
	case "n":
		// New window
		o.AddWindow("")
		return o, nil
	case "x":
		// Close window
		if len(o.Windows) > 0 && o.FocusedWindow >= 0 {
			w := o.Windows[o.FocusedWindow]
			o.FireHook(hooks.AfterCloseWindow, w.ID, w.Title())
			o.DeleteWindow(o.FocusedWindow)
		}
		return o, nil
	case "r":
		// Reset cache stats if showing cache stats overlay
		if o.ShowCacheStats {
			app.GetGlobalStyleCache().ResetStats()
			o.ShowNotification("Cache statistics reset", "info", 2*time.Second)
			return o, nil
		}
		// Otherwise, rename window (unless titles are hidden)
		if config.WindowTitlePosition != "hidden" && len(o.Windows) > 0 && o.FocusedWindow >= 0 {
			focusedWindow := o.GetFocusedWindow()
			if focusedWindow != nil {
				o.RenamingWindow = true
				if fw := o.GetFocusedWindow(); fw != nil {
					fw.InvalidateCache()
				}
				o.RenameBuffer = focusedWindow.CustomName
			}
		}
		return o, nil
	case "tab":
		// Next window
		if len(o.Windows) > 0 {
			o.CycleToNextVisibleWindow()
		}
		return o, nil
	case "shift+tab":
		// Previous window
		if len(o.Windows) > 0 {
			o.CycleToPreviousVisibleWindow()
		}
		return o, nil
	case "t":
		// Toggle tiling mode
		o.ToggleAutoTiling()
		if o.AutoTiling {
			o.ShowNotification("Tiling Mode Enabled [T]", "success", config.NotificationDuration)
		} else {
			o.ShowNotification("Tiling Mode Disabled", "info", config.NotificationDuration)
		}
		return o, nil
	case "esc":
		// Cancel tiling prefix mode
		return o, nil
	default:
		// Unknown tiling command, ignore
		return o, nil
	}
}

// HandleDebugPrefixCommand handles debug prefix commands (Ctrl+B, D, ...) in window management mode
func HandleDebugPrefixCommand(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.DebugPrefixActive = false
	o.PrefixActive = false

	switch msg.String() {
	case "l":
		// Toggle log viewer
		o.ShowLogs = !o.ShowLogs
		if o.ShowLogs {
			o.ShowNotification("Log Viewer: ON", "info", config.NotificationDuration)
		} else {
			o.ShowNotification("Log Viewer: OFF", "info", config.NotificationDuration)
		}
		return o, nil
	case "c":
		// Toggle cache statistics
		o.ShowCacheStats = !o.ShowCacheStats
		if o.ShowCacheStats {
			o.ShowNotification("Cache Stats: ON", "info", config.NotificationDuration)
		} else {
			o.ShowNotification("Cache Stats: OFF", "info", config.NotificationDuration)
		}
		return o, nil
	case "k":
		// Toggle showkeys overlay
		o.ShowKeys = !o.ShowKeys
		if o.ShowKeys {
			o.ShowNotification("Showkeys: ON", "info", config.NotificationDuration)
		} else {
			o.ShowNotification("Showkeys: OFF", "info", config.NotificationDuration)
		}
		return o, nil
	case "a":
		// Toggle animations
		config.AnimationsEnabled = !config.AnimationsEnabled
		if config.AnimationsEnabled {
			o.ShowNotification("Animations: ON", "info", config.NotificationDuration)
		} else {
			o.ShowNotification("Animations: OFF", "info", config.NotificationDuration)
		}
		return o, nil
	case "esc":
		// Cancel debug prefix mode
		return o, nil
	default:
		// Unknown debug command, ignore
		return o, nil
	}
}

// HandleTapePrefixCommand handles tape prefix commands (Ctrl+B, T, ...) in window management mode
func HandleTapePrefixCommand(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.TapePrefixActive = false
	o.PrefixActive = false

	switch msg.String() {
	case "m":
		// Open tape manager
		o.ToggleTapeManager()
		return o, nil
	case "r":
		// Start recording - show naming prompt
		if o.TapeRecorder != nil && o.TapeRecorder.IsRecording() {
			o.ShowNotification("Already recording", "warning", config.NotificationDuration)
		} else {
			o.TapeManagerStartRecording()
			o.ShowTapeManager = true // Show the UI for naming
		}
		return o, nil
	case "s":
		// Stop recording
		if o.TapeRecorder != nil && o.TapeRecorder.IsRecording() {
			o.TapeManagerStopRecording()
		} else {
			o.ShowNotification("Not recording", "warning", config.NotificationDuration)
		}
		return o, nil
	case "esc":
		// Cancel tape prefix mode
		return o, nil
	default:
		// Unknown tape command, ignore
		return o, nil
	}
}
