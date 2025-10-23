// Package input implements keyboard event handling for TUIOS.
package input

import (
	"fmt"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	tea "github.com/charmbracelet/bubbletea/v2"
)

// HandleTerminalModeKey handles keyboard input in terminal mode
func HandleTerminalModeKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	focusedWindow := o.GetFocusedWindow()

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

	// Handle Ctrl+S to toggle selection mode from terminal mode
	if msg.String() == "ctrl+s" {
		return handleTerminalSelectionToggle(msg, o)
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
	case "s", "[":
		// Enter copy mode (vim-style scrollback/selection, replaces old scrollback mode)
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

// handleTerminalSelectionToggle toggles selection mode from terminal mode
func handleTerminalSelectionToggle(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.SelectionMode {
		// Currently in selection mode, toggle it off and stay in terminal mode
		o.SelectionMode = false
		o.ShowNotification("Selection Mode Disabled", "info", config.NotificationDuration)
		// Reset scrollback offset when exiting selection mode
		if focusedWindow := o.GetFocusedWindow(); focusedWindow != nil {
			focusedWindow.ScrollbackOffset = 0
			focusedWindow.InvalidateCache()
		}
	} else {
		// Not in selection mode, enable it and switch to window management mode
		o.Mode = app.WindowManagementMode
		o.SelectionMode = true
		o.ShowNotification("Selection Mode", "info", config.NotificationDuration)
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

	// Non-prefix keybindings (immediate actions)
	switch msg.String() {
	case "ctrl+c":
		// Quit
		o.Cleanup()
		return o, tea.Quit

	case "q":
		// Close help if showing
		if o.ShowHelp {
			o.ShowHelp = false
			return o, nil
		}
		// Exit selection mode if active
		if o.SelectionMode {
			o.SelectionMode = false
			o.ShowNotification("Selection Mode Exited", "info", config.NotificationDuration)
			if focusedWindow := o.GetFocusedWindow(); focusedWindow != nil {
				focusedWindow.SelectedText = ""
				focusedWindow.IsSelecting = false
				focusedWindow.ScrollbackOffset = 0
				focusedWindow.InvalidateCache()
			}
			return o, nil
		}
		// Quit application
		o.Cleanup()
		return o, tea.Quit

	// Workspace switching with Alt+1-9
	// On macOS, Option+number generates special characters, so we handle both
	case "alt+1", "¡", "alt+2", "™", "alt+3", "£", "alt+4", "¢", "alt+5", "∞",
		"alt+6", "§", "alt+7", "¶", "alt+8", "•", "alt+9", "ª":
		handleWorkspaceSwitch(msg, o)
		return o, nil

	// Move window to workspace and follow with Alt+Shift+1-9
	case "alt+shift+1", "alt+!", "⁄", "alt+shift+2", "alt+@", "€", "alt+shift+3", "alt+#", "‹",
		"alt+shift+4", "alt+$", "›", "alt+shift+5", "alt+%", "ﬁ", "alt+shift+6", "alt+^", "ﬂ",
		"alt+shift+7", "alt+&", "‡", "alt+shift+8", "alt+*", "°", "alt+shift+9", "alt+(", "·":
		handleWorkspaceMoveAndFollow(msg, o)
		return o, nil

	// Window management
	case "n":
		// New window
		o.AddWindow("")
		return o, nil
	case "w", "x":
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

	// Window navigation
	case "tab":
		// Next window
		o.CycleToNextVisibleWindow()
		return o, nil
	case "shift+tab":
		// Previous window
		o.CycleToPreviousVisibleWindow()
		return o, nil

	// Window manipulation
	case "m":
		// Minimize window
		if len(o.Windows) > 0 && o.FocusedWindow >= 0 {
			focusedWindow := o.GetFocusedWindow()
			if focusedWindow != nil && !focusedWindow.Minimized {
				o.MinimizeWindow(o.FocusedWindow)
			}
		}
		return o, nil
	case "M", "shift+m":
		// Restore all minimized windows in current workspace
		for i := range o.Windows {
			if o.Windows[i].Minimized && o.Windows[i].Workspace == o.CurrentWorkspace {
				o.RestoreWindow(i)
			}
		}
		// Retile if in tiling mode
		if o.AutoTiling {
			o.TileAllWindows()
		}
		return o, nil

	// Mode switching
	case "i", "enter":
		// Enter terminal/insert mode
		if len(o.Windows) > 0 && o.FocusedWindow >= 0 {
			o.ShowNotification("Terminal Mode", "info", config.NotificationDuration)
			// Clear selection state when entering terminal mode
			focusedWindow := o.GetFocusedWindow()
			if focusedWindow != nil {
				focusedWindow.SelectedText = ""
				focusedWindow.IsSelecting = false
				focusedWindow.InvalidateCache()
			}
			// Enter terminal mode and start raw input reader
			return o, o.EnterTerminalMode()
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

	// Help
	case "?":
		// Toggle help
		o.ShowHelp = !o.ShowHelp
		if o.ShowHelp {
			o.HelpScrollOffset = 0 // Reset scroll when opening
		}
		return o, nil

	// Selection mode
	case "s":
		return handleSelectionModeToggle(msg, o)

	// Copy selected text to clipboard (only in selection mode)
	case "c":
		return handleCopyToClipboard(msg, o)

	// Toggle selection mode from window management mode (return to terminal mode when disabling)
	case "ctrl+s":
		return handleCtrlSSelectionToggle(msg, o)

	// Paste from clipboard to terminal (both window management and selection mode)
	case "ctrl+v":
		if o.FocusedWindow >= 0 && o.FocusedWindow < len(o.Windows) {
			focusedWindow := o.GetFocusedWindow()
			if focusedWindow != nil {
				// Request clipboard content from Bubbletea
				return o, tea.ReadClipboard
			}
		}
		return o, nil

	// Clear selection in selection mode
	case "esc":
		return handleEscapeKey(msg, o)

	// Log viewer
	case "ctrl+l":
		// Toggle log viewer
		o.ShowLogs = !o.ShowLogs
		if o.ShowLogs {
			o.LogScrollOffset = 0 // Reset scroll when opening
			o.LogInfo("Log viewer opened")
		}
		return o, nil

	// Cache statistics viewer
	case "C": // Capital C to toggle cache stats
		o.ShowCacheStats = !o.ShowCacheStats
		if o.ShowCacheStats {
			o.LogInfo("Cache statistics viewer opened")
		}
		return o, nil

	// Arrow keys for scrolling help/logs when they're open, or selection
	case "up":
		return handleUpKey(msg, o)
	case "down":
		return handleDownKey(msg, o)
	case "left":
		return handleLeftKey(msg, o)
	case "right":
		return handleRightKey(msg, o)

	// Ctrl+Arrow keys for snapping or swapping
	case "ctrl+up":
		return handleCtrlUpKey(msg, o)
	case "ctrl+down":
		return handleCtrlDownKey(msg, o)
	case "ctrl+left":
		return handleCtrlLeftKey(msg, o)
	case "ctrl+right":
		return handleCtrlRightKey(msg, o)

	// Shift+Arrow keys for extending selection
	case "shift+up":
		return handleShiftUpKey(msg, o)
	case "shift+down":
		return handleShiftDownKey(msg, o)
	case "shift+left":
		return handleShiftLeftKey(msg, o)
	case "shift+right":
		return handleShiftRightKey(msg, o)

	// Snapping (non-tiling mode only)
	case "h":
		if !o.AutoTiling && len(o.Windows) > 0 && o.FocusedWindow >= 0 {
			o.Snap(o.FocusedWindow, app.SnapLeft)
		}
		return o, nil
	case "l":
		if !o.AutoTiling && len(o.Windows) > 0 && o.FocusedWindow >= 0 {
			o.Snap(o.FocusedWindow, app.SnapRight)
		}
		return o, nil
	case "k":
		if !o.AutoTiling && len(o.Windows) > 0 && o.FocusedWindow >= 0 {
			o.Snap(o.FocusedWindow, app.SnapFullScreen)
		}
		return o, nil
	case "j":
		if !o.AutoTiling && len(o.Windows) > 0 && o.FocusedWindow >= 0 {
			o.Snap(o.FocusedWindow, app.Unsnap)
		}
		return o, nil
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		return handleNumberKey(msg, o)
	case "f":
		// Fullscreen
		if !o.AutoTiling && len(o.Windows) > 0 && o.FocusedWindow >= 0 {
			o.Snap(o.FocusedWindow, app.SnapFullScreen)
		}
		return o, nil
	case "u":
		// Unsnap/restore window position
		if !o.AutoTiling && len(o.Windows) > 0 && o.FocusedWindow >= 0 {
			o.Snap(o.FocusedWindow, app.Unsnap)
		}
		return o, nil
	// Shift+1 through Shift+9 to restore minimized windows
	case "!", "shift+1":
		o.RestoreMinimizedByIndex(0)
		return o, nil
	case "@", "shift+2":
		o.RestoreMinimizedByIndex(1)
		return o, nil
	case "#", "shift+3":
		o.RestoreMinimizedByIndex(2)
		return o, nil
	case "$", "shift+4":
		o.RestoreMinimizedByIndex(3)
		return o, nil
	case "%", "shift+5":
		o.RestoreMinimizedByIndex(4)
		return o, nil
	case "^", "shift+6":
		o.RestoreMinimizedByIndex(5)
		return o, nil
	case "&", "shift+7":
		o.RestoreMinimizedByIndex(6)
		return o, nil
	case "*", "shift+8":
		o.RestoreMinimizedByIndex(7)
		return o, nil
	case "(", "shift+9":
		o.RestoreMinimizedByIndex(8)
		return o, nil
	case "H", "shift+h":
		// In tiling mode, swap with window to the left
		if o.AutoTiling && o.FocusedWindow >= 0 {
			o.SwapWindowLeft()
		}
		return o, nil
	case "L", "shift+l":
		// In tiling mode, swap with window to the right
		if o.AutoTiling && o.FocusedWindow >= 0 {
			o.SwapWindowRight()
		}
		return o, nil
	case "K", "shift+k":
		// In tiling mode, swap with window above
		if o.AutoTiling && o.FocusedWindow >= 0 {
			o.SwapWindowUp()
		}
		return o, nil
	case "J", "shift+j":
		// In tiling mode, swap with window below
		if o.AutoTiling && o.FocusedWindow >= 0 {
			o.SwapWindowDown()
		}
		return o, nil
	default:
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

func handleSelectionModeToggle(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.FocusedWindow >= 0 && o.FocusedWindow < len(o.Windows) {
		focusedWindow := o.GetFocusedWindow()
		if focusedWindow != nil {
			o.SelectionMode = !o.SelectionMode
			if o.SelectionMode {
				// Reset selection when entering selection mode
				focusedWindow.IsSelecting = false
				focusedWindow.SelectedText = ""
				focusedWindow.SelectionMode = 0 // Default to character mode
				// Initialize selection cursor at terminal cursor position
				if focusedWindow.Terminal != nil {
					cursor := focusedWindow.Terminal.CursorPosition()
					focusedWindow.SelectionCursor.X = cursor.X
					focusedWindow.SelectionCursor.Y = cursor.Y
				}
				o.ShowNotification("Selection Mode", "info", config.NotificationDuration)
			} else {
				// Clear selection when exiting
				focusedWindow.IsSelecting = false
				focusedWindow.SelectedText = ""
				focusedWindow.SelectionMode = 0
				o.ShowNotification("Selection Mode Disabled", "info", config.NotificationDuration)
			}
		}
	}
	return o, nil
}

func handleCopyToClipboard(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.SelectionMode && o.FocusedWindow >= 0 && o.FocusedWindow < len(o.Windows) {
		focusedWindow := o.GetFocusedWindow()
		if focusedWindow != nil && focusedWindow.SelectedText != "" {
			// Copy to clipboard using Bubbletea's native support
			textToCopy := focusedWindow.SelectedText
			o.ShowNotification(fmt.Sprintf("Copied %d characters to clipboard", len(textToCopy)), "success", config.NotificationDuration)
			// Auto-unselect text after successful copy
			focusedWindow.SelectedText = ""
			focusedWindow.IsSelecting = false
			focusedWindow.InvalidateCache()
			return o, tea.SetClipboard(textToCopy)
		}
		o.ShowNotification("No text selected", "warning", config.NotificationDuration)
		return o, nil
	}
	// If not in selection mode, continue normal processing
	return o, nil
}

func handleCtrlSSelectionToggle(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.SelectionMode {
		// Currently in selection mode, disable it and return to terminal mode
		o.SelectionMode = false
		o.ShowNotification("Terminal Mode", "info", config.NotificationDuration)
		// Clear selection state when switching to terminal mode
		if focusedWindow := o.GetFocusedWindow(); focusedWindow != nil {
			focusedWindow.SelectedText = ""
			focusedWindow.IsSelecting = false
			focusedWindow.ScrollbackOffset = 0 // Reset scrollback offset
			focusedWindow.InvalidateCache()
		}
		// Enter terminal mode and start raw input reader
		return o, o.EnterTerminalMode()
	} else {
		// Not in selection mode, enable it (already in window management mode)
		o.SelectionMode = true
		o.ShowNotification("Selection Mode", "info", config.NotificationDuration)
	}
	return o, nil
}

func handleEscapeKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	// Close help menu if showing
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
	if o.ShowHelp {
		if o.HelpScrollOffset > 0 {
			o.HelpScrollOffset--
		}
		return o, nil
	}
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
	if o.ShowHelp {
		o.HelpScrollOffset++
		return o, nil
	}
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
