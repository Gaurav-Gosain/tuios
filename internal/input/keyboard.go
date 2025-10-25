// Package input implements keyboard event handling for TUIOS.
package input

import (
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	tea "github.com/charmbracelet/bubbletea/v2"
)

// HandleTerminalModeKey handles keyboard input in terminal mode
func HandleTerminalModeKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	focusedWindow := o.GetFocusedWindow()

	// Handle help menu first (takes priority over everything in terminal mode)
	if o.ShowHelp {
		key := msg.String()

		// Handle escape - exit search first if active, then close help
		if key == "esc" {
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
			return o, nil
		}

		// Handle ? to close help
		if key == "?" {
			o.ShowHelp = false
			o.HelpScrollOffset = 0
			o.HelpCategory = -1 // Reset to trigger auto-selection next time
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

		// Handle left/right arrows for category navigation
		if key == "left" {
			o.HelpScrollOffset = 0 // Reset scroll when changing categories
			return handleLeftKey(msg, o)
		}
		if key == "right" {
			o.HelpScrollOffset = 0 // Reset scroll when changing categories
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

		// Help is showing but key wasn't handled - ignore it
		return o, nil
	}

	// Handle log viewer (takes priority in terminal mode)
	if o.ShowLogs {
		key := msg.String()

		// Close log viewer with q, esc, or Ctrl+B D l
		if key == "q" || key == "esc" {
			o.ShowLogs = false
			o.LogScrollOffset = 0
			return o, nil
		}

		// Calculate how many logs can fit on screen (matching render logic)
		// Height - 8 for margins/borders, minimum 8
		maxDisplayHeight := max(o.Height-8, 8)
		totalLogs := len(o.LogMessages)

		// Fixed overhead: title (1) + blank after title (1) + blank before hint (1) + hint (1) = 4
		fixedLines := 4
		// If scrollable, add scroll indicator: blank (1) + indicator (1) = 2
		if totalLogs > maxDisplayHeight-fixedLines {
			fixedLines = 6
		}
		logsPerPage := maxDisplayHeight - fixedLines
		if logsPerPage < 1 {
			logsPerPage = 1
		}

		// Calculate max scroll position based on visible capacity
		// Can only scroll if there are more logs than fit on screen
		maxScroll := totalLogs - logsPerPage
		if maxScroll < 0 {
			maxScroll = 0
		}

		// Scroll up/down
		if key == "up" || key == "k" {
			if o.LogScrollOffset > 0 {
				o.LogScrollOffset--
			}
			return o, nil
		}
		if key == "down" || key == "j" {
			if o.LogScrollOffset < maxScroll {
				o.LogScrollOffset++
			}
			return o, nil
		}

		// Page up/down (scroll by half page)
		pageSize := logsPerPage / 2
		if pageSize < 1 {
			pageSize = 1
		}
		if key == "pgup" || key == "ctrl+u" {
			o.LogScrollOffset -= pageSize
			if o.LogScrollOffset < 0 {
				o.LogScrollOffset = 0
			}
			return o, nil
		}
		if key == "pgdown" || key == "ctrl+d" {
			o.LogScrollOffset += pageSize
			if o.LogScrollOffset > maxScroll {
				o.LogScrollOffset = maxScroll
			}
			return o, nil
		}

		// Go to top/bottom
		if key == "g" || key == "home" {
			o.LogScrollOffset = 0
			return o, nil
		}
		if key == "G" || key == "end" {
			o.LogScrollOffset = maxScroll
			return o, nil
		}

		// Ignore other keys when log viewer is active
		return o, nil
	}

	// Handle cache stats viewer (takes priority in terminal mode)
	if o.ShowCacheStats {
		key := msg.String()

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

	// Handle copy mode (vim-style scrollback/selection)
	if focusedWindow != nil && focusedWindow.CopyMode != nil && focusedWindow.CopyMode.Active {
		return HandleCopyModeKey(msg, o, focusedWindow)
	}

	// Check for prefix key in terminal mode
	if msg.String() == "ctrl+b" {
		// If prefix is already active, send Ctrl+B to terminal
		if o.PrefixActive {
			o.PrefixActive = false
			if focusedWindow != nil {
				// Send literal Ctrl+B
				focusedWindow.SendInput([]byte{0x02})
			}
			return o, nil
		}
		// Activate prefix mode
		o.PrefixActive = true
		o.LastPrefixTime = time.Now()
		return o, nil
	}

	// Handle workspace prefix commands (Ctrl+B, w, ...)
	if o.WorkspacePrefixActive {
		return handleTerminalWorkspacePrefix(msg, o)
	}

	// Handle minimize prefix commands (Ctrl+B, m, ...)
	if o.MinimizePrefixActive {
		return handleTerminalMinimizePrefix(msg, o)
	}

	// Handle tiling prefix commands (Ctrl+B, t, ...)
	if o.TilingPrefixActive {
		return handleTerminalTilingPrefix(msg, o)
	}

	// Handle debug prefix commands (Ctrl+B, D, ...)
	if o.DebugPrefixActive {
		return handleTerminalDebugPrefix(msg, o)
	}

	// Handle prefix commands in terminal mode
	if o.PrefixActive {
		return handleTerminalPrefixCommand(msg, o)
	}

	// Handle Alt+1-9 workspace switching in terminal mode
	// Don't send workspace switching keys to the PTY
	handled := handleWorkspaceSwitch(msg, o)
	if handled {
		return o, nil
	}

	// Handle paste shortcuts - intercept and request clipboard via OSC 52
	keyStr := msg.String()
	if keyStr == "ctrl+v" || keyStr == "ctrl+shift+v" || keyStr == "super+v" || keyStr == "super+shift+v" {
		if focusedWindow != nil {
			// Use tea.ReadClipboard to request clipboard via OSC 52
			// This will generate a tea.ClipboardMsg which we handle in handler.go
			return o, tea.ReadClipboard
		}
		return o, nil
	}

	// Normal terminal mode - pass through all keys
	if focusedWindow != nil {
		rawInput := getRawKeyBytes(msg)

		if len(rawInput) > 0 {
			if err := focusedWindow.SendInput(rawInput); err != nil {
				// Terminal unavailable, switch back to window mode
				o.Mode = app.WindowManagementMode
				focusedWindow.InvalidateCache()
			}
		}
	} else {
		// No focused window, switch back to window mode
		o.Mode = app.WindowManagementMode
	}
	return o, nil
}

// handleTerminalWorkspacePrefix handles workspace prefix commands in terminal mode
func handleTerminalWorkspacePrefix(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.WorkspacePrefixActive = false
	o.PrefixActive = false

	keyStr := msg.String()

	// Handle digit keys for workspace switching
	if len(keyStr) == 1 && keyStr[0] >= '1' && keyStr[0] <= '9' {
		num := int(keyStr[0] - '0')
		o.SwitchToWorkspace(num)
		return o, nil
	}

	// Handle Shift+digit for moving window to workspace
	if o.FocusedWindow >= 0 && o.FocusedWindow < len(o.Windows) {
		workspace := 0
		switch keyStr {
		case "shift+1", "!":
			workspace = 1
		case "shift+2", "@":
			workspace = 2
		case "shift+3", "#":
			workspace = 3
		case "shift+4", "$":
			workspace = 4
		case "shift+5", "%":
			workspace = 5
		case "shift+6", "^":
			workspace = 6
		case "shift+7", "&":
			workspace = 7
		case "shift+8", "*":
			workspace = 8
		case "shift+9", "(":
			workspace = 9
		}
		if workspace > 0 {
			o.MoveWindowToWorkspaceAndFollow(o.FocusedWindow, workspace)
		}
	}

	return o, nil
}

// handleTerminalMinimizePrefix handles minimize prefix commands in terminal mode
func handleTerminalMinimizePrefix(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
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

// handleTerminalTilingPrefix handles tiling/window prefix commands in terminal mode
func handleTerminalTilingPrefix(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
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
			o.DeleteWindow(o.FocusedWindow)
			// If we still have windows, stay in terminal mode
			if len(o.Windows) > 0 {
				if newFocused := o.GetFocusedWindow(); newFocused != nil {
					newFocused.InvalidateCache()
				}
			} else {
				// No windows left, exit terminal mode
				o.Mode = app.WindowManagementMode
			}
		}
		return o, nil
	case "r":
		// Rename window - exit terminal mode for this
		if len(o.Windows) > 0 && o.FocusedWindow >= 0 {
			focusedWindow := o.GetFocusedWindow()
			if focusedWindow != nil {
				o.Mode = app.WindowManagementMode
				o.RenamingWindow = true
				o.RenameBuffer = focusedWindow.CustomName
			}
		}
		return o, nil
	case "tab":
		// Next window
		o.CycleToNextVisibleWindow()
		// Refresh the new window in terminal mode
		if newFocused := o.GetFocusedWindow(); newFocused != nil {
			newFocused.InvalidateCache()
		}
		return o, nil
	case "shift+tab":
		// Previous window
		o.CycleToPreviousVisibleWindow()
		// Refresh the new window in terminal mode
		if newFocused := o.GetFocusedWindow(); newFocused != nil {
			newFocused.InvalidateCache()
		}
		return o, nil
	case "t":
		// Toggle tiling mode
		o.AutoTiling = !o.AutoTiling
		if o.AutoTiling {
			o.TileAllWindows()
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

// handleTerminalDebugPrefix handles debug prefix commands (Ctrl+B, D, ...)
func handleTerminalDebugPrefix(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
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
	case "esc":
		// Cancel debug prefix mode
		return o, nil
	default:
		// Unknown debug command, ignore
		return o, nil
	}
}

// handleTerminalPrefixCommand handles prefix commands in terminal mode
func handleTerminalPrefixCommand(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.PrefixActive = false
	switch msg.String() {
	case "w":
		// Activate workspace prefix mode
		o.WorkspacePrefixActive = true
		o.PrefixActive = true // Keep prefix active for the next key
		o.LastPrefixTime = time.Now()
		return o, nil
	case "m":
		// Activate minimize prefix mode
		o.MinimizePrefixActive = true
		o.PrefixActive = true // Keep prefix active for the next key
		o.LastPrefixTime = time.Now()
		return o, nil
	case "t":
		// Activate tiling/window prefix mode
		o.TilingPrefixActive = true
		o.PrefixActive = true // Keep prefix active for the next key
		o.LastPrefixTime = time.Now()
		return o, nil
	case "D":
		// Activate debug prefix mode (Ctrl+B, Shift+D)
		o.DebugPrefixActive = true
		o.PrefixActive = true // Keep prefix active for the next key
		o.LastPrefixTime = time.Now()
		return o, nil
	case "d", "esc":
		// Detach/exit terminal mode (like tmux detach)
		o.Mode = app.WindowManagementMode
		o.ShowNotification("Window Management Mode", "info", config.NotificationDuration)
		if focusedWindow := o.GetFocusedWindow(); focusedWindow != nil {
			focusedWindow.InvalidateCache()
		}
		return o, nil

	// Window navigation commands work in insert mode
	case "n", "tab":
		// Next window
		o.CycleToNextVisibleWindow()
		// Refresh the new window in terminal mode
		if newFocused := o.GetFocusedWindow(); newFocused != nil {
			newFocused.InvalidateCache()
		}
		return o, nil
	case "p", "shift+tab":
		// Previous window (like tmux with 'p' or like normal mode with 'shift+tab')
		o.CycleToPreviousVisibleWindow()
		// Refresh the new window in terminal mode
		if newFocused := o.GetFocusedWindow(); newFocused != nil {
			newFocused.InvalidateCache()
		}
		return o, nil
	case "0", "1", "2", "3", "4", "5", "6", "7", "8", "9":
		// Jump to window by number
		return handleTerminalWindowSelection(msg, o)

	// Window management
	case "c":
		// Create new window
		o.AddWindow("")
		return o, nil
	case "x":
		// Close current window
		if len(o.Windows) > 0 && o.FocusedWindow >= 0 {
			o.DeleteWindow(o.FocusedWindow)
			// If we still have windows, stay in terminal mode
			if len(o.Windows) > 0 {
				if newFocused := o.GetFocusedWindow(); newFocused != nil {
					newFocused.InvalidateCache()
				}
			} else {
				// No windows left, exit terminal mode
				o.Mode = app.WindowManagementMode
			}
		}
		return o, nil
	case ",", "r":
		// Rename window - exit terminal mode for this (like tmux with ',' or like normal mode with 'r')
		if len(o.Windows) > 0 && o.FocusedWindow >= 0 {
			focusedWindow := o.GetFocusedWindow()
			if focusedWindow != nil {
				o.Mode = app.WindowManagementMode
				o.RenamingWindow = true
				o.RenameBuffer = focusedWindow.CustomName
			}
		}
		return o, nil

	// Layout commands
	case "space":
		// Toggle tiling mode (like tmux)
		o.AutoTiling = !o.AutoTiling
		if o.AutoTiling {
			o.TileAllWindows()
		}
		return o, nil
	case "z":
		// Toggle fullscreen for current window
		if !o.AutoTiling && len(o.Windows) > 0 && o.FocusedWindow >= 0 {
			o.Snap(o.FocusedWindow, app.SnapFullScreen)
		}
		return o, nil
	case "[":
		// Enter copy mode (vim-style scrollback/selection)
		if focusedWindow := o.GetFocusedWindow(); focusedWindow != nil {
			focusedWindow.EnterCopyMode()
			o.ShowNotification("COPY MODE (hjkl/q)", "info", config.NotificationDuration*2)
		}
		return o, nil

	// Help
	case "?":
		// Toggle help
		o.ShowHelp = !o.ShowHelp
		return o, nil

	case "q":
		// Quit application
		o.Cleanup()
		o.PrefixActive = false
		return o, tea.Quit

	default:
		// Unknown prefix command, pass through the key
		focusedWindow := o.GetFocusedWindow()
		if focusedWindow != nil {
			rawInput := getRawKeyBytes(msg)
			if len(rawInput) > 0 {
				focusedWindow.SendInput(rawInput)
			}
		}
	}
	return o, nil
}

// handleTerminalWindowSelection handles window selection in terminal mode
func handleTerminalWindowSelection(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	num := int(msg.String()[0] - '0')
	if o.AutoTiling {
		// In tiling mode, select visible window in current workspace
		visibleIndex := 0
		for i, win := range o.Windows {
			if win.Workspace == o.CurrentWorkspace && !win.Minimized {
				visibleIndex++
				if visibleIndex == num || (num == 0 && visibleIndex == 10) {
					o.FocusWindow(i)
					break
				}
			}
		}
	} else {
		// Normal mode, select by absolute index in current workspace
		windowsInWorkspace := 0
		for i, win := range o.Windows {
			if win.Workspace == o.CurrentWorkspace {
				windowsInWorkspace++
				if windowsInWorkspace == num || (num == 0 && windowsInWorkspace == 10) {
					o.FocusWindow(i)
					break
				}
			}
		}
	}
	// Refresh the new window in terminal mode
	if newFocused := o.GetFocusedWindow(); newFocused != nil {
		newFocused.InvalidateCache()
	}
	return o, nil
}

// HandleWindowManagementModeKey handles keyboard input in window management mode
func HandleWindowManagementModeKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	focusedWindow := o.GetFocusedWindow()

	// Handle copy mode (vim-style scrollback/selection) - takes priority
	if focusedWindow != nil && focusedWindow.CopyMode != nil && focusedWindow.CopyMode.Active {
		return HandleCopyModeKey(msg, o, focusedWindow)
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
		// Close log viewer with q, esc, or Ctrl+B D l
		if key == "q" || key == "esc" {
			o.ShowLogs = false
			o.LogScrollOffset = 0
			return o, nil
		}

		// Calculate how many logs can fit on screen (matching render logic)
		// Height - 8 for margins/borders, minimum 8
		maxDisplayHeight := max(o.Height-8, 8)
		totalLogs := len(o.LogMessages)

		// Fixed overhead: title (1) + blank after title (1) + blank before hint (1) + hint (1) = 4
		fixedLines := 4
		// If scrollable, add scroll indicator: blank (1) + indicator (1) = 2
		if totalLogs > maxDisplayHeight-fixedLines {
			fixedLines = 6
		}
		logsPerPage := maxDisplayHeight - fixedLines
		if logsPerPage < 1 {
			logsPerPage = 1
		}

		// Calculate max scroll position based on visible capacity
		// Can only scroll if there are more logs than fit on screen
		maxScroll := totalLogs - logsPerPage
		if maxScroll < 0 {
			maxScroll = 0
		}

		// Scroll up/down
		if key == "up" || key == "k" {
			if o.LogScrollOffset > 0 {
				o.LogScrollOffset--
			}
			return o, nil
		}
		if key == "down" || key == "j" {
			if o.LogScrollOffset < maxScroll {
				o.LogScrollOffset++
			}
			return o, nil
		}

		// Page up/down (scroll by half page)
		pageSize := logsPerPage / 2
		if pageSize < 1 {
			pageSize = 1
		}
		if key == "pgup" || key == "ctrl+u" {
			o.LogScrollOffset -= pageSize
			if o.LogScrollOffset < 0 {
				o.LogScrollOffset = 0
			}
			return o, nil
		}
		if key == "pgdown" || key == "ctrl+d" {
			o.LogScrollOffset += pageSize
			if o.LogScrollOffset > maxScroll {
				o.LogScrollOffset = maxScroll
			}
			return o, nil
		}

		// Go to top/bottom
		if key == "g" || key == "home" {
			o.LogScrollOffset = 0
			return o, nil
		}
		if key == "G" || key == "end" {
			o.LogScrollOffset = maxScroll
			return o, nil
		}

		// Ignore other keys when log viewer is active
		return o, nil
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

	// Emergency/safety keybindings that bypass the config system
	// Only Ctrl+C is kept as emergency quit
	switch key {
	case "ctrl+c":
		// Emergency quit - always works regardless of config
		o.Cleanup()
		return o, tea.Quit

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
		// Otherwise, rename window
		if len(o.Windows) > 0 && o.FocusedWindow >= 0 {
			focusedWindow := o.GetFocusedWindow()
			if focusedWindow != nil {
				o.RenamingWindow = true
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
		o.AutoTiling = !o.AutoTiling
		if o.AutoTiling {
			o.TileAllWindows()
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
	case "esc":
		// Cancel debug prefix mode
		return o, nil
	default:
		// Unknown debug command, ignore
		return o, nil
	}
}

// handleWorkspaceSwitch handles Alt+1-9 workspace switching (with macOS Option key support)
func handleWorkspaceSwitch(msg tea.KeyPressMsg, o *app.OS) bool {
	keyStr := msg.String()

	// Check for macOS Option+digit keys
	if len(keyStr) > 0 {
		firstRune := []rune(keyStr)[0]
		if digit, ok := IsMacOSOptionKey(firstRune); ok {
			o.SwitchToWorkspace(digit)
			return true
		}
	}

	// Check for standard Alt+digit keys
	switch keyStr {
	case "alt+1":
		o.SwitchToWorkspace(1)
		return true
	case "alt+2":
		o.SwitchToWorkspace(2)
		return true
	case "alt+3":
		o.SwitchToWorkspace(3)
		return true
	case "alt+4":
		o.SwitchToWorkspace(4)
		return true
	case "alt+5":
		o.SwitchToWorkspace(5)
		return true
	case "alt+6":
		o.SwitchToWorkspace(6)
		return true
	case "alt+7":
		o.SwitchToWorkspace(7)
		return true
	case "alt+8":
		o.SwitchToWorkspace(8)
		return true
	case "alt+9":
		o.SwitchToWorkspace(9)
		return true
	default:
		return false
	}
}

// handleWorkspaceMoveAndFollow handles Alt+Shift+1-9 to move window to workspace and follow
func handleWorkspaceMoveAndFollow(msg tea.KeyPressMsg, o *app.OS) {
	if o.FocusedWindow < 0 || o.FocusedWindow >= len(o.Windows) {
		return
	}

	keyStr := msg.String()
	workspace := 0

	// Check for macOS Option+Shift+digit keys
	if len(keyStr) > 0 {
		if digit, ok := IsMacOSOptionShiftKey([]rune(keyStr)[0]); ok {
			workspace = digit
		}
	}

	// Check for standard Alt+Shift+digit keys if not already matched
	if workspace == 0 {
		switch keyStr {
		case "alt+shift+1", "alt+!":
			workspace = 1
		case "alt+shift+2", "alt+@":
			workspace = 2
		case "alt+shift+3", "alt+#":
			workspace = 3
		case "alt+shift+4", "alt+$":
			workspace = 4
		case "alt+shift+5", "alt+%":
			workspace = 5
		case "alt+shift+6", "alt+^":
			workspace = 6
		case "alt+shift+7", "alt+&":
			workspace = 7
		case "alt+shift+8", "alt+*":
			workspace = 8
		case "alt+shift+9", "alt+(":
			workspace = 9
		}
	}

	if workspace > 0 {
		o.MoveWindowToWorkspaceAndFollow(o.FocusedWindow, workspace)
	}
}

// Helper functions for handling various key combinations

func handleNumberKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	num := int(msg.String()[0] - '0')

	if o.AutoTiling || strings.HasPrefix(msg.String(), "ctrl+") {
		// Select window by index in current workspace
		if o.AutoTiling {
			// Count only visible windows in current workspace
			visibleIndex := 0
			for i, win := range o.Windows {
				if win.Workspace == o.CurrentWorkspace && !win.Minimized {
					visibleIndex++
					if visibleIndex == num {
						o.FocusWindow(i)
						break
					}
				}
			}
		} else {
			// Normal selection with Ctrl (windows in current workspace)
			windowsInWorkspace := 0
			for i, win := range o.Windows {
				if win.Workspace == o.CurrentWorkspace {
					windowsInWorkspace++
					if windowsInWorkspace == num {
						o.FocusWindow(i)
						break
					}
				}
			}
		}
	} else if num <= 4 && len(o.Windows) > 0 && o.FocusedWindow >= 0 {
		// Corner snapping (only for 1-4)
		switch num {
		case 1:
			o.Snap(o.FocusedWindow, app.SnapTopLeft)
		case 2:
			o.Snap(o.FocusedWindow, app.SnapTopRight)
		case 3:
			o.Snap(o.FocusedWindow, app.SnapBottomLeft)
		case 4:
			o.Snap(o.FocusedWindow, app.SnapBottomRight)
		}
	}
	return o, nil
}

func handleEscapeKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	// If help is showing and search is active, clear search first
	if o.ShowHelp && o.HelpSearchMode {
		if o.HelpSearchQuery != "" {
			// Clear the search query first
			o.HelpSearchQuery = ""
			return o, nil
		} else {
			// Search query is empty, exit search mode
			o.HelpSearchMode = false
			return o, nil
		}
	}

	// Close help menu if showing (and not in search mode)
	if o.ShowHelp {
		o.ShowHelp = false
		return o, nil
	}

	// Close cache stats if showing
	if o.ShowCacheStats {
		o.ShowCacheStats = false
		return o, nil
	}

	// Close logs if showing
	if o.ShowLogs {
		o.ShowLogs = false
		return o, nil
	}

	if o.SelectionMode && o.FocusedWindow >= 0 && o.FocusedWindow < len(o.Windows) {
		focusedWindow := o.GetFocusedWindow()
		if focusedWindow != nil && focusedWindow.SelectedText != "" {
			// Clear the selection
			focusedWindow.SelectedText = ""
			focusedWindow.IsSelecting = false
			o.ShowNotification("Selection cleared", "info", config.NotificationDuration)
			return o, nil
		}
	}
	// If not in selection mode with text, continue normal processing (exit terminal mode)
	return o, nil
}

func handleUpKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	// Note: help menu scrolling is handled in HandleTerminalModeKey and HandleWindowManagementModeKey
	// This function is only for selection mode and logs when NOT in help mode
	if o.ShowLogs {
		if o.LogScrollOffset > 0 {
			o.LogScrollOffset--
		}
		return o, nil
	}
	// Keyboard-based text selection in selection mode
	if o.SelectionMode && o.FocusedWindow >= 0 && o.FocusedWindow < len(o.Windows) {
		focusedWindow := o.GetFocusedWindow()
		if focusedWindow != nil {
			o.MoveSelectionCursor(focusedWindow, 0, -1, false)
		}
		return o, nil
	}
	return o, nil
}

func handleDownKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	// Note: help menu scrolling is handled in HandleTerminalModeKey and HandleWindowManagementModeKey
	// This function is only for selection mode and logs when NOT in help mode
	if o.ShowLogs {
		o.LogScrollOffset++
		return o, nil
	}
	// Keyboard-based text selection in selection mode
	if o.SelectionMode && o.FocusedWindow >= 0 && o.FocusedWindow < len(o.Windows) {
		focusedWindow := o.GetFocusedWindow()
		if focusedWindow != nil {
			o.MoveSelectionCursor(focusedWindow, 0, 1, false)
		}
		return o, nil
	}
	return o, nil
}

func handleLeftKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	// Help menu category navigation
	if o.ShowHelp && !o.HelpSearchMode {
		if o.HelpCategory > 0 {
			o.HelpCategory--
		}
		return o, nil
	}

	// Keyboard-based text selection in selection mode
	if o.SelectionMode && o.FocusedWindow >= 0 && o.FocusedWindow < len(o.Windows) {
		focusedWindow := o.GetFocusedWindow()
		if focusedWindow != nil {
			o.MoveSelectionCursor(focusedWindow, -1, 0, false)
		}
		return o, nil
	}
	return o, nil
}

func handleRightKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	// Help menu category navigation
	if o.ShowHelp && !o.HelpSearchMode {
		categories := app.GetHelpCategories(o.KeybindRegistry)
		if o.HelpCategory < len(categories)-1 {
			o.HelpCategory++
		}
		return o, nil
	}

	// Keyboard-based text selection in selection mode
	if o.SelectionMode && o.FocusedWindow >= 0 && o.FocusedWindow < len(o.Windows) {
		focusedWindow := o.GetFocusedWindow()
		if focusedWindow != nil {
			o.MoveSelectionCursor(focusedWindow, 1, 0, false)
		}
		return o, nil
	}
	return o, nil
}

func handleCtrlUpKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.FocusedWindow >= 0 && o.FocusedWindow < len(o.Windows) {
		if o.AutoTiling {
			// In tiling mode, swap with window above (same as Shift+K)
			o.SwapWindowUp()
		} else {
			// In manual mode, maximize window
			o.Snap(o.FocusedWindow, app.SnapFullScreen)
		}
	}
	return o, nil
}

func handleCtrlDownKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.FocusedWindow >= 0 && o.FocusedWindow < len(o.Windows) {
		if o.AutoTiling {
			// In tiling mode, swap with window below (same as Shift+J)
			o.SwapWindowDown()
		} else {
			// In manual mode, unsnap window
			o.Snap(o.FocusedWindow, app.Unsnap)
		}
	}
	return o, nil
}

func handleCtrlLeftKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if len(o.Windows) > 0 && o.FocusedWindow >= 0 {
		if o.AutoTiling {
			// In tiling mode, swap with window to the left (same as Shift+H)
			o.SwapWindowLeft()
		} else {
			// In manual mode, snap to left half
			o.Snap(o.FocusedWindow, app.SnapLeft)
		}
	}
	return o, nil
}

func handleCtrlRightKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if len(o.Windows) > 0 && o.FocusedWindow >= 0 {
		if o.AutoTiling {
			// In tiling mode, swap with window to the right (same as Shift+L)
			o.SwapWindowRight()
		} else {
			// In manual mode, snap to right half
			o.Snap(o.FocusedWindow, app.SnapRight)
		}
	}
	return o, nil
}

func handleShiftUpKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.SelectionMode && o.FocusedWindow >= 0 && o.FocusedWindow < len(o.Windows) {
		focusedWindow := o.GetFocusedWindow()
		if focusedWindow != nil {
			o.MoveSelectionCursor(focusedWindow, 0, -1, true) // true = extending selection
		}
		return o, nil
	}
	return o, nil
}

func handleShiftDownKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.SelectionMode && o.FocusedWindow >= 0 && o.FocusedWindow < len(o.Windows) {
		focusedWindow := o.GetFocusedWindow()
		if focusedWindow != nil {
			o.MoveSelectionCursor(focusedWindow, 0, 1, true) // true = extending selection
		}
		return o, nil
	}
	return o, nil
}

func handleShiftLeftKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.SelectionMode && o.FocusedWindow >= 0 && o.FocusedWindow < len(o.Windows) {
		focusedWindow := o.GetFocusedWindow()
		if focusedWindow != nil {
			o.MoveSelectionCursor(focusedWindow, -1, 0, true) // true = extending selection
		}
		return o, nil
	}
	return o, nil
}

func handleShiftRightKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.SelectionMode && o.FocusedWindow >= 0 && o.FocusedWindow < len(o.Windows) {
		focusedWindow := o.GetFocusedWindow()
		if focusedWindow != nil {
			o.MoveSelectionCursor(focusedWindow, 1, 0, true) // true = extending selection
		}
		return o, nil
	}
	return o, nil
}
