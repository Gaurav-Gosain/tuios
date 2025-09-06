// Package main implements TUIOS input handling and key forwarding.
//
// This module handles keyboard input in both Window Management and Terminal modes.
// Key improvements in this version:
//
// - Leverages Bubble Tea v2 beta Key.Text field for better Unicode/international keyboard support
// - Uses Key.Code and Key.Mod for more reliable modifier key handling
// - Implements proper ANSI/VT escape sequence generation for terminal compatibility
// - Modular design with separate functions for different key types
// - Better handling of complex key combinations (Ctrl+Shift+Alt combinations)
// - Improved function key support with modifier combinations
//
// The getRawKeyBytes function converts Bubble Tea KeyPressMsg to raw terminal bytes
// suitable for PTY forwarding, ensuring applications like vim, emacs, etc. work correctly.
package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea/v2"
)

func (m *OS) handleKeyPress(msg tea.KeyPressMsg) (*OS, tea.Cmd) {
	// Handle rename mode
	if m.RenamingWindow {
		switch msg.String() {
		case "enter":
			// Apply the new name
			if focusedWindow := m.GetFocusedWindow(); focusedWindow != nil {
				focusedWindow.CustomName = m.RenameBuffer
				focusedWindow.InvalidateCache()
			}
			m.RenamingWindow = false
			m.RenameBuffer = ""
			return m, nil
		case "esc":
			// Cancel renaming
			m.RenamingWindow = false
			m.RenameBuffer = ""
			return m, nil
		case "backspace":
			if len(m.RenameBuffer) > 0 {
				m.RenameBuffer = m.RenameBuffer[:len(m.RenameBuffer)-1]
			}
			return m, nil
		default:
			// Add character to buffer if it's a printable character
			if len(msg.String()) == 1 && msg.String()[0] >= 32 && msg.String()[0] < 127 {
				m.RenameBuffer += msg.String()
			}
			return m, nil
		}
	}

	if m.Mode == TerminalMode {
		// Check for prefix key in terminal mode
		if msg.String() == "ctrl+b" {
			// If prefix is already active, send Ctrl+B to terminal
			if m.PrefixActive {
				m.PrefixActive = false
				focusedWindow := m.GetFocusedWindow()
				if focusedWindow != nil {
					// Send literal Ctrl+B
					focusedWindow.SendInput([]byte{0x02})
				}
				return m, nil
			}
			// Activate prefix mode
			m.PrefixActive = true
			m.LastPrefixTime = time.Now()
			return m, nil
		}

		// Handle prefix commands in terminal mode
		if m.PrefixActive {
			m.PrefixActive = false
			switch msg.String() {
			case "d", "esc":
				// Detach/exit terminal mode (like tmux detach)
				m.Mode = WindowManagementMode
				m.ShowNotification("Window Management Mode", "info", time.Duration(NotificationDuration)*time.Millisecond)
				if focusedWindow := m.GetFocusedWindow(); focusedWindow != nil {
					focusedWindow.InvalidateCache()
				}
				return m, nil

			// Window navigation commands work in insert mode
			case "n", "tab":
				// Next window
				m.CycleToNextVisibleWindow()
				// Refresh the new window in terminal mode
				if newFocused := m.GetFocusedWindow(); newFocused != nil {
					newFocused.InvalidateCache()
				}
				return m, nil
			case "p":
				// Previous window
				m.CycleToPreviousVisibleWindow()
				// Refresh the new window in terminal mode
				if newFocused := m.GetFocusedWindow(); newFocused != nil {
					newFocused.InvalidateCache()
				}
				return m, nil
			case "0", "1", "2", "3", "4", "5", "6", "7", "8", "9":
				// Jump to window by number
				num := int(msg.String()[0] - '0')
				if m.AutoTiling {
					// In tiling mode, select visible window in current workspace
					visibleIndex := 0
					for i, win := range m.Windows {
						if win.Workspace == m.CurrentWorkspace && !win.Minimized {
							visibleIndex++
							if visibleIndex == num || (num == 0 && visibleIndex == 10) {
								m.FocusWindow(i)
								break
							}
						}
					}
				} else {
					// Normal mode, select by absolute index in current workspace
					windowsInWorkspace := 0
					for i, win := range m.Windows {
						if win.Workspace == m.CurrentWorkspace {
							windowsInWorkspace++
							if windowsInWorkspace == num || (num == 0 && windowsInWorkspace == 10) {
								m.FocusWindow(i)
								break
							}
						}
					}
				}
				// Refresh the new window in terminal mode
				if newFocused := m.GetFocusedWindow(); newFocused != nil {
					newFocused.InvalidateCache()
				}
				return m, nil

			// Window management
			case "c":
				// Create new window
				m.AddWindow("")
				return m, nil
			case "x", "w":
				// Close current window
				if len(m.Windows) > 0 && m.FocusedWindow >= 0 {
					m.DeleteWindow(m.FocusedWindow)
					// If we still have windows, stay in terminal mode
					if len(m.Windows) > 0 {
						if newFocused := m.GetFocusedWindow(); newFocused != nil {
							newFocused.InvalidateCache()
						}
					} else {
						// No windows left, exit terminal mode
						m.Mode = WindowManagementMode
					}
				}
				return m, nil
			case ",":
				// Rename window - exit terminal mode for this
				if len(m.Windows) > 0 && m.FocusedWindow >= 0 {
					focusedWindow := m.GetFocusedWindow()
					if focusedWindow != nil {
						m.Mode = WindowManagementMode
						m.RenamingWindow = true
						m.RenameBuffer = focusedWindow.CustomName
					}
				}
				return m, nil

			// Layout commands
			case "space":
				// Toggle tiling mode
				m.AutoTiling = !m.AutoTiling
				if m.AutoTiling {
					m.TileAllWindows()
				}
				return m, nil
			case "z":
				// Toggle fullscreen for current window
				if !m.AutoTiling && len(m.Windows) > 0 && m.FocusedWindow >= 0 {
					m.Snap(m.FocusedWindow, SnapFullScreen)
				}
				return m, nil
			case "s":
				// Toggle selection mode (like tmux copy mode)
				if m.SelectionMode {
					// Currently in selection mode, disable it and return to terminal mode
					m.SelectionMode = false
					m.Mode = TerminalMode
					m.ShowNotification("Terminal Mode", "info", time.Duration(NotificationDuration)*time.Millisecond)
					// Clear selection state when switching to terminal mode
					if focusedWindow := m.GetFocusedWindow(); focusedWindow != nil {
						focusedWindow.SelectedText = ""
						focusedWindow.IsSelecting = false
						focusedWindow.InvalidateCache()
					}
				} else {
					// Not in selection mode, enable it and switch to window management mode
					m.Mode = WindowManagementMode
					m.SelectionMode = true
					m.ShowNotification("Selection Mode", "info", time.Duration(NotificationDuration)*time.Millisecond)
				}
				return m, nil

			// Help
			case "?":
				// Toggle help
				m.ShowHelp = !m.ShowHelp
				return m, nil

			default:
				// Unknown prefix command, pass through the key
				focusedWindow := m.GetFocusedWindow()
				if focusedWindow != nil {
					rawInput := getRawKeyBytes(msg)
					if len(rawInput) > 0 {
						focusedWindow.SendInput(rawInput)
					}
				}
			}
			return m, nil
		}

		// Handle Alt+1-9 workspace switching in terminal mode
		switch msg.String() {
		case "alt+1":
			m.SwitchToWorkspace(1)
			return m, nil
		case "alt+2":
			m.SwitchToWorkspace(2)
			return m, nil
		case "alt+3":
			m.SwitchToWorkspace(3)
			return m, nil
		case "alt+4":
			m.SwitchToWorkspace(4)
			return m, nil
		case "alt+5":
			m.SwitchToWorkspace(5)
			return m, nil
		case "alt+6":
			m.SwitchToWorkspace(6)
			return m, nil
		case "alt+7":
			m.SwitchToWorkspace(7)
			return m, nil
		case "alt+8":
			m.SwitchToWorkspace(8)
			return m, nil
		case "alt+9":
			m.SwitchToWorkspace(9)
			return m, nil
		case "alt+shift+1", "alt+!":
			if m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows) {
				m.MoveWindowToWorkspaceAndFollow(m.FocusedWindow, 1)
			}
			return m, nil
		case "alt+shift+2", "alt+@":
			if m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows) {
				m.MoveWindowToWorkspaceAndFollow(m.FocusedWindow, 2)
			}
			return m, nil
		case "alt+shift+3", "alt+#":
			if m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows) {
				m.MoveWindowToWorkspaceAndFollow(m.FocusedWindow, 3)
			}
			return m, nil
		case "alt+shift+4", "alt+$":
			if m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows) {
				m.MoveWindowToWorkspaceAndFollow(m.FocusedWindow, 4)
			}
			return m, nil
		case "alt+shift+5", "alt+%":
			if m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows) {
				m.MoveWindowToWorkspaceAndFollow(m.FocusedWindow, 5)
			}
			return m, nil
		case "alt+shift+6", "alt+^":
			if m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows) {
				m.MoveWindowToWorkspaceAndFollow(m.FocusedWindow, 6)
			}
			return m, nil
		case "alt+shift+7", "alt+&":
			if m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows) {
				m.MoveWindowToWorkspaceAndFollow(m.FocusedWindow, 7)
			}
			return m, nil
		case "alt+shift+8", "alt+*":
			if m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows) {
				m.MoveWindowToWorkspaceAndFollow(m.FocusedWindow, 8)
			}
			return m, nil
		case "alt+shift+9", "alt+(":
			if m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows) {
				m.MoveWindowToWorkspaceAndFollow(m.FocusedWindow, 9)
			}
			return m, nil
		}

		// Handle Ctrl+S to toggle selection mode from terminal mode
		if msg.String() == "ctrl+s" {
			if m.SelectionMode {
				// Currently in selection mode, toggle it off and stay in terminal mode
				m.SelectionMode = false
				m.ShowNotification("Selection Mode Disabled", "info", time.Duration(NotificationDuration)*time.Millisecond)
			} else {
				// Not in selection mode, enable it and switch to window management mode
				m.Mode = WindowManagementMode
				m.SelectionMode = true
				m.ShowNotification("Selection Mode", "info", time.Duration(NotificationDuration)*time.Millisecond)
			}
			return m, nil
		}

		// Handle Ctrl+V paste in terminal mode
		if msg.String() == "ctrl+v" {
			focusedWindow := m.GetFocusedWindow()
			if focusedWindow != nil {
				// Request clipboard content from Bubbletea
				return m, tea.ReadClipboard
			}
			return m, nil
		}

		// Normal terminal mode - pass through all keys
		focusedWindow := m.GetFocusedWindow()
		if focusedWindow != nil {
			rawInput := getRawKeyBytes(msg)
			if len(rawInput) > 0 {
				if err := focusedWindow.SendInput(rawInput); err != nil {
					// Terminal unavailable, switch back to window mode
					m.Mode = WindowManagementMode
					focusedWindow.InvalidateCache()
				}
			}
		} else {
			// No focused window, switch back to window mode
			m.Mode = WindowManagementMode
		}
		return m, nil
	}

	// Check for prefix key activation
	if msg.String() == "ctrl+b" {
		// If prefix is already active, deactivate it (double Ctrl+B cancels)
		if m.PrefixActive {
			m.PrefixActive = false
			return m, nil
		}
		// Activate prefix mode
		m.PrefixActive = true
		m.LastPrefixTime = time.Now()
		return m, nil
	}

	// Handle prefix commands in window management mode
	if m.PrefixActive {
		// Deactivate prefix after handling command
		m.PrefixActive = false

		switch msg.String() {
		// Window management
		case "c":
			// Create new window (like tmux)
			m.AddWindow("")
			return m, nil
		case "x", "w":
			// Close current window
			if len(m.Windows) > 0 && m.FocusedWindow >= 0 {
				m.DeleteWindow(m.FocusedWindow)
			}
			return m, nil
		case ",":
			// Rename window (like tmux)
			if len(m.Windows) > 0 && m.FocusedWindow >= 0 {
				focusedWindow := m.GetFocusedWindow()
				if focusedWindow != nil {
					m.RenamingWindow = true
					m.RenameBuffer = focusedWindow.CustomName
				}
			}
			return m, nil

		// Window navigation
		case "n", "tab":
			// Next window
			if len(m.Windows) > 0 {
				m.CycleToNextVisibleWindow()
			}
			return m, nil
		case "p":
			// Previous window
			if len(m.Windows) > 0 {
				m.CycleToPreviousVisibleWindow()
			}
			return m, nil
		case "0", "1", "2", "3", "4", "5", "6", "7", "8", "9":
			// Jump to window by number
			num := int(msg.String()[0] - '0')
			if m.AutoTiling {
				// In tiling mode, select visible window in current workspace
				visibleIndex := 0
				for i, win := range m.Windows {
					if win.Workspace == m.CurrentWorkspace && !win.Minimized {
						visibleIndex++
						if visibleIndex == num || (num == 0 && visibleIndex == 10) {
							m.FocusWindow(i)
							break
						}
					}
				}
			} else {
				// Normal mode, select by absolute index in current workspace
				windowsInWorkspace := 0
				for i, win := range m.Windows {
					if win.Workspace == m.CurrentWorkspace {
						windowsInWorkspace++
						if windowsInWorkspace == num || (num == 0 && windowsInWorkspace == 10) {
							m.FocusWindow(i)
							break
						}
					}
				}
			}
			return m, nil

		// Layout commands
		case "space":
			// Toggle tiling mode
			m.AutoTiling = !m.AutoTiling
			if m.AutoTiling {
				m.TileAllWindows()
			}
			return m, nil
		case "z":
			// Toggle fullscreen for current window
			if !m.AutoTiling && len(m.Windows) > 0 && m.FocusedWindow >= 0 {
				m.Snap(m.FocusedWindow, SnapFullScreen)
			}
			return m, nil

		// Help
		case "?":
			// Toggle help
			m.ShowHelp = !m.ShowHelp
			return m, nil

		// Exit prefix mode
		case "esc", "ctrl+c":
			// Just cancel prefix mode
			return m, nil

		default:
			// Unknown command, ignore
			return m, nil
		}
	}

	// Timeout prefix mode after 2 seconds
	if m.PrefixActive && time.Since(m.LastPrefixTime) > 2*time.Second {
		m.PrefixActive = false
	}

	// Non-prefix keybindings (immediate actions)
	switch msg.String() {
	case "ctrl+c", "q":
		// Quit
		return m, tea.Quit

	// Workspace switching with Alt+1-9
	case "alt+1":
		m.SwitchToWorkspace(1)
		return m, nil
	case "alt+2":
		m.SwitchToWorkspace(2)
		return m, nil
	case "alt+3":
		m.SwitchToWorkspace(3)
		return m, nil
	case "alt+4":
		m.SwitchToWorkspace(4)
		return m, nil
	case "alt+5":
		m.SwitchToWorkspace(5)
		return m, nil
	case "alt+6":
		m.SwitchToWorkspace(6)
		return m, nil
	case "alt+7":
		m.SwitchToWorkspace(7)
		return m, nil
	case "alt+8":
		m.SwitchToWorkspace(8)
		return m, nil
	case "alt+9":
		m.SwitchToWorkspace(9)
		return m, nil

	// Move window to workspace and follow with Alt+Shift+1-9
	case "alt+shift+1", "alt+!":
		if m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows) {
			m.MoveWindowToWorkspaceAndFollow(m.FocusedWindow, 1)
		}
		return m, nil
	case "alt+shift+2", "alt+@":
		if m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows) {
			m.MoveWindowToWorkspaceAndFollow(m.FocusedWindow, 2)
		}
		return m, nil
	case "alt+shift+3", "alt+#":
		if m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows) {
			m.MoveWindowToWorkspaceAndFollow(m.FocusedWindow, 3)
		}
		return m, nil
	case "alt+shift+4", "alt+$":
		if m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows) {
			m.MoveWindowToWorkspaceAndFollow(m.FocusedWindow, 4)
		}
		return m, nil
	case "alt+shift+5", "alt+%":
		if m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows) {
			m.MoveWindowToWorkspaceAndFollow(m.FocusedWindow, 5)
		}
		return m, nil
	case "alt+shift+6", "alt+^":
		if m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows) {
			m.MoveWindowToWorkspaceAndFollow(m.FocusedWindow, 6)
		}
		return m, nil
	case "alt+shift+7", "alt+&":
		if m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows) {
			m.MoveWindowToWorkspaceAndFollow(m.FocusedWindow, 7)
		}
		return m, nil
	case "alt+shift+8", "alt+*":
		if m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows) {
			m.MoveWindowToWorkspaceAndFollow(m.FocusedWindow, 8)
		}
		return m, nil
	case "alt+shift+9", "alt+(":
		if m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows) {
			m.MoveWindowToWorkspaceAndFollow(m.FocusedWindow, 9)
		}
		return m, nil

	// Window management
	case "n":
		// New window
		m.AddWindow("")
		return m, nil
	case "w", "x":
		// Close window
		if len(m.Windows) > 0 && m.FocusedWindow >= 0 {
			m.DeleteWindow(m.FocusedWindow)
		}
		return m, nil
	case "r":
		// Rename window
		if len(m.Windows) > 0 && m.FocusedWindow >= 0 {
			focusedWindow := m.GetFocusedWindow()
			if focusedWindow != nil {
				m.RenamingWindow = true
				m.RenameBuffer = focusedWindow.CustomName
			}
		}
		return m, nil

	// Window navigation
	case "tab":
		// Next window
		m.CycleToNextVisibleWindow()
		return m, nil
	case "shift+tab":
		// Previous window
		m.CycleToPreviousVisibleWindow()
		return m, nil

	// Window manipulation
	case "m":
		// Minimize window
		if len(m.Windows) > 0 && m.FocusedWindow >= 0 {
			focusedWindow := m.GetFocusedWindow()
			if focusedWindow != nil && !focusedWindow.Minimized {
				m.MinimizeWindow(m.FocusedWindow)
			}
		}
		return m, nil
	case "M", "shift+m":
		// Restore all minimized windows
		for i := range m.Windows {
			if m.Windows[i].Minimized {
				m.RestoreWindow(i)
			}
		}
		return m, nil

	// Mode switching
	case "i", "enter":
		// Enter terminal/insert mode
		if len(m.Windows) > 0 && m.FocusedWindow >= 0 {
			m.Mode = TerminalMode
			m.ShowNotification("Terminal Mode", "info", time.Duration(NotificationDuration)*time.Millisecond)
			// Clear selection state when entering terminal mode
			focusedWindow := m.GetFocusedWindow()
			if focusedWindow != nil {
				focusedWindow.SelectedText = ""
				focusedWindow.IsSelecting = false
				focusedWindow.InvalidateCache()
			}
		}
		return m, nil
	case "t":
		// Toggle tiling mode
		m.AutoTiling = !m.AutoTiling
		if m.AutoTiling {
			m.TileAllWindows()
			m.ShowNotification("Tiling Mode Enabled ▦", "success", time.Duration(NotificationDuration)*time.Millisecond)
		} else {
			m.ShowNotification("Tiling Mode Disabled", "info", time.Duration(NotificationDuration)*time.Millisecond)
		}
		return m, nil

	// Help
	case "?":
		// Toggle help
		m.ShowHelp = !m.ShowHelp
		if m.ShowHelp {
			m.HelpScrollOffset = 0 // Reset scroll when opening
		}
		return m, nil

	// Selection mode
	case "s":
		// Toggle selection mode for focused window
		if m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows) {
			focusedWindow := m.GetFocusedWindow()
			if focusedWindow != nil {
				m.SelectionMode = !m.SelectionMode
				if m.SelectionMode {
					// Reset selection when entering selection mode
					focusedWindow.IsSelecting = false
					focusedWindow.SelectedText = ""
					// Initialize selection cursor at terminal cursor position
					if focusedWindow.Terminal != nil && focusedWindow.Terminal.Screen() != nil {
						cursor := focusedWindow.Terminal.Screen().Cursor()
						focusedWindow.SelectionCursor.X = cursor.X
						focusedWindow.SelectionCursor.Y = cursor.Y
					}
					m.ShowNotification("Selection Mode", "info", time.Duration(NotificationDuration)*time.Millisecond)
				} else {
					// Clear selection when exiting
					focusedWindow.IsSelecting = false
					focusedWindow.SelectedText = ""
					m.ShowNotification("Selection Mode Disabled", "info", time.Duration(NotificationDuration)*time.Millisecond)
				}
			}
		}
		return m, nil

	// Copy selected text to clipboard (only in selection mode)
	case "c":
		if m.SelectionMode && m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows) {
			focusedWindow := m.GetFocusedWindow()
			if focusedWindow != nil && focusedWindow.SelectedText != "" {
				// Copy to clipboard using Bubbletea's native support
				textToCopy := focusedWindow.SelectedText
				m.ShowNotification(fmt.Sprintf("Copied %d characters to clipboard", len(textToCopy)), "success", time.Duration(NotificationDuration)*time.Millisecond)
				// Auto-unselect text after successful copy
				focusedWindow.SelectedText = ""
				focusedWindow.IsSelecting = false
				focusedWindow.InvalidateCache()
				return m, tea.SetClipboard(textToCopy)
			}
			m.ShowNotification("No text selected", "warning", time.Duration(NotificationDuration)*time.Millisecond)
			return m, nil
		}
		// If not in selection mode, continue normal processing

	// Toggle selection mode from window management mode (return to terminal mode when disabling)
	case "ctrl+s":
		if m.SelectionMode {
			// Currently in selection mode, disable it and return to terminal mode
			m.SelectionMode = false
			m.Mode = TerminalMode
			m.ShowNotification("Terminal Mode", "info", time.Duration(NotificationDuration)*time.Millisecond)
			// Clear selection state when switching to terminal mode
			if focusedWindow := m.GetFocusedWindow(); focusedWindow != nil {
				focusedWindow.SelectedText = ""
				focusedWindow.IsSelecting = false
				focusedWindow.InvalidateCache()
			}
		} else {
			// Not in selection mode, enable it (already in window management mode)
			m.SelectionMode = true
			m.ShowNotification("Selection Mode", "info", time.Duration(NotificationDuration)*time.Millisecond)
		}
		return m, nil

	// Paste from clipboard to terminal (both window management and selection mode)
	case "ctrl+v":
		if m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows) {
			focusedWindow := m.GetFocusedWindow()
			if focusedWindow != nil {
				// Request clipboard content from Bubbletea
				return m, tea.ReadClipboard
			}
		}
		return m, nil

	// Clear selection in selection mode
	case "esc":
		if m.SelectionMode && m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows) {
			focusedWindow := m.GetFocusedWindow()
			if focusedWindow != nil && focusedWindow.SelectedText != "" {
				// Clear the selection
				focusedWindow.SelectedText = ""
				focusedWindow.IsSelecting = false
				m.ShowNotification("Selection cleared", "info", time.Duration(NotificationDuration)*time.Millisecond)
				return m, nil
			}
		}
		// If not in selection mode with text, continue normal processing (exit terminal mode)

	// Log viewer
	case "ctrl+l":
		// Toggle log viewer
		m.ShowLogs = !m.ShowLogs
		if m.ShowLogs {
			m.LogScrollOffset = 0 // Reset scroll when opening
			m.LogInfo("Log viewer opened")
		}
		return m, nil

	// Arrow keys for scrolling help/logs when they're open
	case "up":
		if m.ShowHelp {
			if m.HelpScrollOffset > 0 {
				m.HelpScrollOffset--
			}
			return m, nil
		}
		if m.ShowLogs {
			if m.LogScrollOffset > 0 {
				m.LogScrollOffset--
			}
			return m, nil
		}
		// Keyboard-based text selection in selection mode
		if m.SelectionMode && m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows) {
			focusedWindow := m.GetFocusedWindow()
			if focusedWindow != nil {
				m.moveSelectionCursor(focusedWindow, 0, -1, false)
				return m, nil
			}
		}
		// Otherwise fall through to normal handling
	case "down":
		if m.ShowHelp {
			m.HelpScrollOffset++
			return m, nil
		}
		if m.ShowLogs {
			m.LogScrollOffset++
			return m, nil
		}
		// Keyboard-based text selection in selection mode
		if m.SelectionMode && m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows) {
			focusedWindow := m.GetFocusedWindow()
			if focusedWindow != nil {
				m.moveSelectionCursor(focusedWindow, 0, 1, false)
				return m, nil
			}
		}
		// Otherwise fall through to normal handling

	case "ctrl+up":
		if m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows) {
			if m.AutoTiling {
				// In tiling mode, swap with window above (same as Shift+K)
				m.SwapWithUp()
			} else {
				// In manual mode, maximize window
				m.Snap(m.FocusedWindow, SnapFullScreen)
			}
		}
		return m, nil

	case "ctrl+down":
		if m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows) {
			if m.AutoTiling {
				// In tiling mode, swap with window below (same as Shift+J)
				m.SwapWithDown()
			} else {
				// In manual mode, unsnap window
				m.Snap(m.FocusedWindow, Unsnap)
			}
		}
		return m, nil

	// Left arrow key handling
	case "left":
		// Keyboard-based text selection in selection mode
		if m.SelectionMode && m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows) {
			focusedWindow := m.GetFocusedWindow()
			if focusedWindow != nil {
				m.moveSelectionCursor(focusedWindow, -1, 0, false)
				return m, nil
			}
		}
		// Otherwise fall through to normal handling
	case "right":
		// Keyboard-based text selection in selection mode
		if m.SelectionMode && m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows) {
			focusedWindow := m.GetFocusedWindow()
			if focusedWindow != nil {
				m.moveSelectionCursor(focusedWindow, 1, 0, false)
				return m, nil
			}
		}
		// Otherwise fall through to normal handling

	// Shift+Arrow keys for extending selection
	case "shift+up":
		if m.SelectionMode && m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows) {
			focusedWindow := m.GetFocusedWindow()
			if focusedWindow != nil {
				m.moveSelectionCursor(focusedWindow, 0, -1, true)
				return m, nil
			}
		}
		return m, nil
	case "shift+down":
		if m.SelectionMode && m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows) {
			focusedWindow := m.GetFocusedWindow()
			if focusedWindow != nil {
				m.moveSelectionCursor(focusedWindow, 0, 1, true)
				return m, nil
			}
		}
		return m, nil
	case "shift+left":
		if m.SelectionMode && m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows) {
			focusedWindow := m.GetFocusedWindow()
			if focusedWindow != nil {
				m.moveSelectionCursor(focusedWindow, -1, 0, true)
				return m, nil
			}
		}
		return m, nil
	case "shift+right":
		if m.SelectionMode && m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows) {
			focusedWindow := m.GetFocusedWindow()
			if focusedWindow != nil {
				m.moveSelectionCursor(focusedWindow, 1, 0, true)
				return m, nil
			}
		}
		return m, nil

	// Snapping (non-tiling mode only)
	case "h":
		if !m.AutoTiling && len(m.Windows) > 0 && m.FocusedWindow >= 0 {
			m.Snap(m.FocusedWindow, SnapLeft)
		}
		return m, nil
	case "l":
		if !m.AutoTiling && len(m.Windows) > 0 && m.FocusedWindow >= 0 {
			m.Snap(m.FocusedWindow, SnapRight)
		}
		return m, nil
	case "k":
		if !m.AutoTiling && len(m.Windows) > 0 && m.FocusedWindow >= 0 {
			m.Snap(m.FocusedWindow, SnapFullScreen)
		}
		return m, nil
	case "j":
		if !m.AutoTiling && len(m.Windows) > 0 && m.FocusedWindow >= 0 {
			m.Snap(m.FocusedWindow, Unsnap)
		}
		return m, nil
	case "ctrl+left":
		if len(m.Windows) > 0 && m.FocusedWindow >= 0 {
			if m.AutoTiling {
				// In tiling mode, swap with window to the left (same as Shift+H)
				m.SwapWithLeft()
			} else {
				// In manual mode, snap to left half
				m.Snap(m.FocusedWindow, SnapLeft)
			}
		}
		return m, nil
	case "ctrl+right":
		if len(m.Windows) > 0 && m.FocusedWindow >= 0 {
			if m.AutoTiling {
				// In tiling mode, swap with window to the right (same as Shift+L)
				m.SwapWithRight()
			} else {
				// In manual mode, snap to right half
				m.Snap(m.FocusedWindow, SnapRight)
			}
		}
		return m, nil
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		// In tiling mode or with Ctrl, select window by index
		// Otherwise, use for corner snapping (1-4 only)
		num := int(msg.String()[0] - '0')

		if m.AutoTiling || strings.HasPrefix(msg.String(), "ctrl+") {
			// Select window by index in current workspace
			if m.AutoTiling {
				// Count only visible windows in current workspace
				visibleIndex := 0
				for i, win := range m.Windows {
					if win.Workspace == m.CurrentWorkspace && !win.Minimized {
						visibleIndex++
						if visibleIndex == num {
							m.FocusWindow(i)
							break
						}
					}
				}
			} else {
				// Normal selection with Ctrl (windows in current workspace)
				windowsInWorkspace := 0
				for i, win := range m.Windows {
					if win.Workspace == m.CurrentWorkspace {
						windowsInWorkspace++
						if windowsInWorkspace == num {
							m.FocusWindow(i)
							break
						}
					}
				}
			}
		} else if num <= 4 && len(m.Windows) > 0 && m.FocusedWindow >= 0 {
			// Corner snapping (only for 1-4)
			switch num {
			case 1:
				m.Snap(m.FocusedWindow, SnapTopLeft)
			case 2:
				m.Snap(m.FocusedWindow, SnapTopRight)
			case 3:
				m.Snap(m.FocusedWindow, SnapBottomLeft)
			case 4:
				m.Snap(m.FocusedWindow, SnapBottomRight)
			}
		}
		return m, nil
	case "f":
		// Fullscreen
		if !m.AutoTiling && len(m.Windows) > 0 && m.FocusedWindow >= 0 {
			m.Snap(m.FocusedWindow, SnapFullScreen)
		}
		return m, nil
	case "u":
		// Unsnap/restore window position
		if !m.AutoTiling && len(m.Windows) > 0 && m.FocusedWindow >= 0 {
			m.Snap(m.FocusedWindow, Unsnap)
		}
		return m, nil
	// Shift+1 through Shift+9 to restore minimized windows
	case "!", "shift+1":
		m.RestoreMinimizedByIndex(0)
		return m, nil
	case "@", "shift+2":
		m.RestoreMinimizedByIndex(1)
		return m, nil
	case "#", "shift+3":
		m.RestoreMinimizedByIndex(2)
		return m, nil
	case "$", "shift+4":
		m.RestoreMinimizedByIndex(3)
		return m, nil
	case "%", "shift+5":
		m.RestoreMinimizedByIndex(4)
		return m, nil
	case "^", "shift+6":
		m.RestoreMinimizedByIndex(5)
		return m, nil
	case "&", "shift+7":
		m.RestoreMinimizedByIndex(6)
		return m, nil
	case "*", "shift+8":
		m.RestoreMinimizedByIndex(7)
		return m, nil
	case "(", "shift+9":
		m.RestoreMinimizedByIndex(8)
		return m, nil
	case "H", "shift+h":
		// In tiling mode, swap with window to the left
		if m.AutoTiling && m.FocusedWindow >= 0 {
			m.SwapWithLeft()
		}
		return m, nil
	case "L", "shift+l":
		// In tiling mode, swap with window to the right
		if m.AutoTiling && m.FocusedWindow >= 0 {
			m.SwapWithRight()
		}
		return m, nil
	case "K", "shift+k":
		// In tiling mode, swap with window above
		if m.AutoTiling && m.FocusedWindow >= 0 {
			m.SwapWithUp()
		}
		return m, nil
	case "J", "shift+j":
		// In tiling mode, swap with window below
		if m.AutoTiling && m.FocusedWindow >= 0 {
			m.SwapWithDown()
		}
		return m, nil
	default:
		return m, nil
	}
	return m, nil
}

// getRawKeyBytes converts a Bubble Tea KeyPressMsg to raw bytes for PTY forwarding.
func getRawKeyBytes(msg tea.KeyPressMsg) []byte {
	key := msg.Key()

	// Handle modifier combinations first
	if key.Mod != 0 {
		// Handle Ctrl+letter combinations (standard control codes)
		if key.Mod&tea.ModCtrl != 0 {
			// Handle common Ctrl combinations
			switch key.Code {
			case tea.KeySpace:
				return []byte{0x00} // Ctrl+Space = NUL
			case tea.KeyBackspace:
				return []byte{0x08} // Ctrl+H
			case tea.KeyTab:
				return []byte{0x09} // Ctrl+I
			case tea.KeyEnter:
				return []byte{0x0A} // Ctrl+J
			case tea.KeyEscape:
				return []byte{0x1B} // Ctrl+[
			default:
				// For Ctrl+letter, convert to control codes (1-26)
				if key.Code >= 'a' && key.Code <= 'z' {
					return []byte{byte(key.Code - 'a' + 1)}
				}
				if key.Code >= 'A' && key.Code <= 'Z' {
					return []byte{byte(key.Code - 'A' + 1)}
				}
				// Handle other Ctrl+symbol combinations
				switch key.Code {
				case '@':
					return []byte{0x00} // Ctrl+@
				case '[':
					return []byte{0x1B} // Ctrl+[
				case '\\':
					return []byte{0x1C} // Ctrl+\\
				case ']':
					return []byte{0x1D} // Ctrl+]
				case '^':
					return []byte{0x1E} // Ctrl+^
				case '_':
					return []byte{0x1F} // Ctrl+_
				case '?':
					return []byte{0x7F} // Ctrl+?
				}
			}
		}

		// Handle Alt+letter combinations (ESC prefix)
		if key.Mod&tea.ModAlt != 0 {
			switch key.Code {
			case tea.KeyBackspace:
				return []byte{0x1b, 0x7f}
			default:
				// Alt+character sends ESC followed by character
				if key.Text != "" && len(key.Text) == 1 {
					return []byte{0x1b, key.Text[0]}
				}
				if key.Code >= 32 && key.Code <= 126 {
					return []byte{0x1b, byte(key.Code)}
				}
			}
		}

		// Handle other modifier combinations (function keys, etc.)
		if modSeq := handleModifierKeys(key); len(modSeq) > 0 {
			return modSeq
		}
	}

	// Handle special keys (no modifiers)
	switch key.Code {
	case tea.KeyEnter:
		return []byte{'\r'}
	case tea.KeyTab:
		return []byte{'\t'}
	case tea.KeyBackspace:
		return []byte{0x7f}
	case tea.KeyEscape:
		return []byte{0x1b}
	case tea.KeySpace:
		return []byte{' '}
	case tea.KeyDelete:
		return []byte{0x1b, '[', '3', '~'}
	case tea.KeyInsert:
		return []byte{0x1b, '[', '2', '~'}
	case tea.KeyPgUp:
		return []byte{0x1b, '[', '5', '~'}
	case tea.KeyPgDown:
		return []byte{0x1b, '[', '6', '~'}
	case tea.KeyUp:
		return []byte{0x1b, '[', 'A'}
	case tea.KeyDown:
		return []byte{0x1b, '[', 'B'}
	case tea.KeyRight:
		return []byte{0x1b, '[', 'C'}
	case tea.KeyLeft:
		return []byte{0x1b, '[', 'D'}
	case tea.KeyHome:
		return []byte{0x1b, '[', 'H'}
	case tea.KeyEnd:
		return []byte{0x1b, '[', 'F'}
	}

	// Handle function keys
	if fnSeq := getFunctionKeyBytes(key.Code); len(fnSeq) > 0 {
		return fnSeq
	}

	// For printable characters, use Key.Text if available (handles Unicode, shifted keys)
	if key.Text != "" {
		return []byte(key.Text)
	}

	// Fallback for simple printable characters
	if key.Code >= 32 && key.Code <= 126 {
		return []byte{byte(key.Code)}
	}

	return []byte{}
}

// handleModifierKeys handles keys with complex modifier combinations
func handleModifierKeys(key tea.Key) []byte {
	// Handle function keys with modifiers
	if fnSeq := getFunctionKeySequence(key.Code, getModParam(key.Mod)); fnSeq != nil {
		return fnSeq
	}

	// Handle cursor keys with modifiers
	if cursorSeq := getCursorSequence(key.Code); cursorSeq != nil {
		modParam := getModParam(key.Mod)
		if modParam > 1 {
			// Insert modifier parameter: ESC[1;{mod}{letter}
			result := make([]byte, 0, 8)
			result = append(result, 0x1b, '[', '1', ';', byte('0'+modParam))
			result = append(result, cursorSeq[len(cursorSeq)-1]) // Last character (A,B,C,D,H,F)
			return result
		}
	}

	return []byte{}
}

// getModParam calculates modifier parameter for CSI sequences
func getModParam(mod tea.KeyMod) int {
	modParam := 1
	if mod&tea.ModShift != 0 {
		modParam++
	}
	if mod&tea.ModAlt != 0 {
		modParam += 2
	}
	if mod&tea.ModCtrl != 0 {
		modParam += 4
	}
	return modParam
}

// getFunctionKeyBytes returns bytes for function keys (no modifiers)
func getFunctionKeyBytes(code rune) []byte {
	switch code {
	case tea.KeyF1:
		return []byte{0x1b, 'O', 'P'}
	case tea.KeyF2:
		return []byte{0x1b, 'O', 'Q'}
	case tea.KeyF3:
		return []byte{0x1b, 'O', 'R'}
	case tea.KeyF4:
		return []byte{0x1b, 'O', 'S'}
	case tea.KeyF5:
		return []byte{0x1b, '[', '1', '5', '~'}
	case tea.KeyF6:
		return []byte{0x1b, '[', '1', '7', '~'}
	case tea.KeyF7:
		return []byte{0x1b, '[', '1', '8', '~'}
	case tea.KeyF8:
		return []byte{0x1b, '[', '1', '9', '~'}
	case tea.KeyF9:
		return []byte{0x1b, '[', '2', '0', '~'}
	case tea.KeyF10:
		return []byte{0x1b, '[', '2', '1', '~'}
	case tea.KeyF11:
		return []byte{0x1b, '[', '2', '3', '~'}
	case tea.KeyF12:
		return []byte{0x1b, '[', '2', '4', '~'}
	}
	return []byte{}
}

// getCursorSequence returns ANSI escape sequence for cursor movement keys
func getCursorSequence(code rune) []byte {
	switch code {
	case tea.KeyUp:
		return []byte{0x1b, '[', 'A'}
	case tea.KeyDown:
		return []byte{0x1b, '[', 'B'}
	case tea.KeyRight:
		return []byte{0x1b, '[', 'C'}
	case tea.KeyLeft:
		return []byte{0x1b, '[', 'D'}
	case tea.KeyHome:
		return []byte{0x1b, '[', 'H'}
	case tea.KeyEnd:
		return []byte{0x1b, '[', 'F'}
	}
	return nil
}

// getFunctionKeySequence returns ANSI sequence for function keys with optional modifiers
func getFunctionKeySequence(code rune, modParam int) []byte {
	var baseSeq []byte

	switch code {
	case tea.KeyF1:
		baseSeq = []byte{0x1b, 'O', 'P'}
	case tea.KeyF2:
		baseSeq = []byte{0x1b, 'O', 'Q'}
	case tea.KeyF3:
		baseSeq = []byte{0x1b, 'O', 'R'}
	case tea.KeyF4:
		baseSeq = []byte{0x1b, 'O', 'S'}
	case tea.KeyF5:
		return buildCSISequence(15, modParam)
	case tea.KeyF6:
		return buildCSISequence(17, modParam)
	case tea.KeyF7:
		return buildCSISequence(18, modParam)
	case tea.KeyF8:
		return buildCSISequence(19, modParam)
	case tea.KeyF9:
		return buildCSISequence(20, modParam)
	case tea.KeyF10:
		return buildCSISequence(21, modParam)
	case tea.KeyF11:
		return buildCSISequence(23, modParam)
	case tea.KeyF12:
		return buildCSISequence(24, modParam)
	default:
		return nil
	}

	// F1-F4 with modifiers need different handling
	if modParam > 1 && baseSeq != nil {
		// Convert to CSI format: ESC[1;{mod}{P,Q,R,S}
		result := []byte{0x1b, '[', '1', ';', byte('0' + modParam)}
		result = append(result, baseSeq[len(baseSeq)-1]) // Last char (P,Q,R,S)
		return result
	}

	return baseSeq
}

// buildCSISequence builds a CSI sequence like ESC[{num};{mod}~ or ESC[{num}~
func buildCSISequence(num, modParam int) []byte {
	seq := []byte{0x1b, '['}

	// Add number
	if num >= 10 {
		seq = append(seq, byte('0'+num/10), byte('0'+num%10))
	} else {
		seq = append(seq, byte('0'+num))
	}

	// Add modifier if present
	if modParam > 1 {
		seq = append(seq, ';', byte('0'+modParam))
	}

	seq = append(seq, '~')
	return seq
}

// Fast hit testing without expensive canvas generation
func (m *OS) findClickedWindow(x, y int) int {
	// Find the topmost window (highest Z) that contains the click point
	topWindow := -1
	topZ := -1

	for i, window := range m.Windows {
		// Skip windows not in current workspace
		if window.Workspace != m.CurrentWorkspace {
			continue
		}
		// Skip minimized windows
		if window.Minimized {
			continue
		}
		// Check if click is within window bounds
		if x >= window.X && x < window.X+window.Width &&
			y >= window.Y && y < window.Y+window.Height {
			// This window contains the click - check if it's the topmost so far
			if window.Z > topZ {
				topZ = window.Z
				topWindow = i
			}
		}
	}

	return topWindow
}

// Find which dock item was clicked
func (m *OS) findDockItemClicked(x, y int) int {
	// Count minimized windows in current workspace
	minimizedWindows := make([]int, 0)
	for i, window := range m.Windows {
		if window.Workspace == m.CurrentWorkspace && window.Minimized {
			minimizedWindows = append(minimizedWindows, i)
			if len(minimizedWindows) >= 9 {
				break // Only first 9 items are shown
			}
		}
	}

	if len(minimizedWindows) == 0 {
		return -1
	}

	// Calculate actual dock item widths (matching render.go logic)
	dockItemsWidth := 0
	itemNumber := 1
	itemWidths := make([]int, 0, len(minimizedWindows))

	for _, windowIndex := range minimizedWindows {
		window := m.Windows[windowIndex]

		// Get window name (only custom names)
		windowName := window.CustomName

		// Format label based on whether we have a custom name
		var labelText string
		if windowName != "" {
			// Truncate if too long (max 12 chars for dock item)
			if len(windowName) > 12 {
				windowName = windowName[:9] + "..."
			}
			labelText = fmt.Sprintf(" %d:%s ", itemNumber, windowName)
		} else {
			// Just show the number if no custom name
			labelText = fmt.Sprintf(" %d ", itemNumber)
		}

		// Calculate width: 2 for circles + label width
		itemWidth := 2 + len(labelText)
		itemWidths = append(itemWidths, itemWidth)

		// Add spacing between items
		if itemNumber > 1 {
			dockItemsWidth++ // Space between items
		}
		dockItemsWidth += itemWidth

		itemNumber++
	}

	// Calculate center position considering system info on sides
	leftInfoWidth := 30  // Mode + workspace indicators (matching render.go)
	rightInfoWidth := 20 // CPU graph (matching render.go)
	availableSpace := m.Width - leftInfoWidth - rightInfoWidth - dockItemsWidth
	leftSpacer := max(availableSpace/2, 0)

	startX := leftInfoWidth + leftSpacer

	// Check which item was clicked
	currentX := startX
	for i, windowIndex := range minimizedWindows {
		itemWidth := itemWidths[i]

		// Check if click is within this dock item (single line dock at bottom)
		if x >= currentX && x < currentX+itemWidth && y == m.Height-1 {
			return windowIndex
		}

		currentX += itemWidth
		if i < len(minimizedWindows)-1 {
			currentX++ // Space between items
		}
	}

	return -1
}

func (m *OS) handleMouseClick(msg tea.MouseClickMsg) (*OS, tea.Cmd) {
	mouse := msg.Mouse()
	X := mouse.X
	Y := mouse.Y

	// Note: Mouse forwarding to terminals removed to prevent corruption
	// Applications that need mouse support (vim, less) will handle it themselves
	// when they enable mouse tracking modes

	// Check if click is in the dock area (always reserved)
	if Y >= m.Height-DockHeight {
		// Handle dock click only if there are minimized windows
		if m.HasMinimizedWindows() {
			dockIndex := m.findDockItemClicked(X, Y)
			if dockIndex != -1 {
				m.RestoreWindow(dockIndex)
			}
		}
		return m, nil
	}

	// Fast hit testing - find which window was clicked without expensive canvas generation
	clickedWindowIndex := m.findClickedWindow(X, Y)
	if clickedWindowIndex == -1 {
		// Consume the event even if no window is hit to prevent leaking
		return m, nil
	}

	// IMMEDIATELY focus the clicked window and bring to front Z-index
	// This ensures instant visual feedback when clicking
	m.FocusWindow(clickedWindowIndex)
	if m.Mode == TerminalMode {
		m.Mode = WindowManagementMode
	}

	// Now set interaction mode to prevent expensive rendering during drag/resize
	m.InteractionMode = true

	clickedWindow := m.Windows[clickedWindowIndex]
	leftMost := clickedWindow.X + clickedWindow.Width

	// cross (close button) - rightmost button
	if mouse.Button == tea.MouseLeft && X >= leftMost-5 && X <= leftMost-3 && Y == clickedWindow.Y {
		m.DeleteWindow(clickedWindowIndex)
		m.InteractionMode = false
		return m, nil
	}

	// square (maximize button) - middle button
	if mouse.Button == tea.MouseLeft && X >= leftMost-8 && X <= leftMost-6 && Y == clickedWindow.Y {
		// Toggle fullscreen for now (maximize functionality)
		m.Snap(clickedWindowIndex, SnapFullScreen)
		m.InteractionMode = false
		return m, nil
	}

	// dash (minimize button) - leftmost button
	if mouse.Button == tea.MouseLeft && X >= leftMost-11 && X <= leftMost-9 && Y == clickedWindow.Y {
		m.MinimizeWindow(clickedWindowIndex)
		m.InteractionMode = false
		return m, nil
	}

	// Calculate drag offset based on the clicked window
	m.DragOffsetX = X - clickedWindow.X
	m.DragOffsetY = Y - clickedWindow.Y

	switch mouse.Button {
	case tea.MouseRight:
		// Prevent resizing in tiling mode
		if m.AutoTiling {
			m.InteractionMode = false
			return m, nil
		}

		// Already in interaction mode, now set resize-specific flags
		m.Resizing = true
		m.Windows[clickedWindowIndex].IsBeingManipulated = true
		m.ResizeStartX = mouse.X
		m.ResizeStartY = mouse.Y
		// Save state for resize calculations (avoid mutex copying)
		m.PreResizeState = Window{
			Title:  clickedWindow.Title,
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
			m.ResizeCorner = BottomLeft
			if mouse.Y < midY && mouse.Y >= minY {
				m.ResizeCorner = TopLeft
			}
		} else {
			m.ResizeCorner = BottomRight
			if mouse.Y < midY && mouse.Y >= minY {
				m.ResizeCorner = TopRight
			}
		}

	case tea.MouseLeft:
		// Check if we're in selection mode
		if m.SelectionMode {
			// Calculate terminal coordinates relative to window content
			terminalX := X - clickedWindow.X - 1 // Account for border
			terminalY := Y - clickedWindow.Y - 1 // Account for border

			// Start text selection
			if terminalX >= 0 && terminalY >= 0 &&
				terminalX < clickedWindow.Width-2 && terminalY < clickedWindow.Height-2 {
				clickedWindow.IsSelecting = true
				clickedWindow.SelectionStart.X = terminalX
				clickedWindow.SelectionStart.Y = terminalY
				clickedWindow.SelectionEnd = clickedWindow.SelectionStart
				m.InteractionMode = false
				return m, nil
			}
		}

		// Already in interaction mode, now set drag-specific flags
		m.Dragging = true
		m.DragStartX = mouse.X
		m.DragStartY = mouse.Y
		m.Windows[clickedWindowIndex].IsBeingManipulated = true
		m.DraggedWindowIndex = clickedWindowIndex

		// Store original position for tiling mode swaps
		if m.AutoTiling {
			m.TiledX = clickedWindow.X
			m.TiledY = clickedWindow.Y
			m.TiledWidth = clickedWindow.Width
			m.TiledHeight = clickedWindow.Height
		}
	}
	return m, nil
}

func (m *OS) handleMouseMotion(msg tea.MouseMotionMsg) (*OS, tea.Cmd) {
	mouse := msg.Mouse()

	m.X = mouse.X
	m.Y = mouse.Y
	m.LastMouseX = mouse.X
	m.LastMouseY = mouse.Y

	// Mouse motion forwarding removed to prevent terminal corruption
	// Terminal applications will handle their own mouse tracking when needed

	// Handle text selection motion
	if m.SelectionMode {
		focusedWindow := m.GetFocusedWindow()
		if focusedWindow != nil && focusedWindow.IsSelecting {
			// Calculate terminal coordinates
			terminalX := mouse.X - focusedWindow.X - 1
			terminalY := mouse.Y - focusedWindow.Y - 1

			// Update selection end position
			if terminalX >= 0 && terminalY >= 0 &&
				terminalX < focusedWindow.Width-2 && terminalY < focusedWindow.Height-2 {
				focusedWindow.SelectionEnd.X = terminalX
				focusedWindow.SelectionEnd.Y = terminalY
				return m, nil
			}
		}
	}

	if !m.Dragging && !m.Resizing {
		// Always consume motion events to prevent leaking to terminals
		return m, nil
	}

	focusedWindow := m.GetFocusedWindow()
	if focusedWindow == nil {
		m.Dragging = false
		m.Resizing = false
		m.InteractionMode = false
		return m, nil
	}

	if m.Dragging && m.InteractionMode {
		// Allow windows to go outside screen bounds but not into dock area
		newX := mouse.X - m.DragOffsetX
		newY := mouse.Y - m.DragOffsetY

		// Prevent dragging into dock area
		maxY := m.GetUsableHeight() - focusedWindow.Height
		if newY > maxY {
			newY = maxY
		}

		focusedWindow.X = newX
		focusedWindow.Y = newY
		focusedWindow.MarkPositionDirty()
		return m, nil
	}

	if m.Resizing && m.InteractionMode {
		// Prevent resizing in tiling mode
		if m.AutoTiling {
			return m, nil
		}

		xOffset := mouse.X - m.ResizeStartX
		yOffset := mouse.Y - m.ResizeStartY

		newX := focusedWindow.X
		newY := focusedWindow.Y
		newWidth := focusedWindow.Width
		newHeight := focusedWindow.Height

		switch m.ResizeCorner {
		case TopLeft:
			newX = m.PreResizeState.X + xOffset
			newY = m.PreResizeState.Y + yOffset
			newWidth = m.PreResizeState.Width - xOffset
			newHeight = m.PreResizeState.Height - yOffset
		case TopRight:
			newY = m.PreResizeState.Y + yOffset
			newWidth = m.PreResizeState.Width + xOffset
			newHeight = m.PreResizeState.Height - yOffset
		case BottomLeft:
			newX = m.PreResizeState.X + xOffset
			newWidth = m.PreResizeState.Width - xOffset
			newHeight = m.PreResizeState.Height + yOffset
		case BottomRight:
			newWidth = m.PreResizeState.Width + xOffset
			newHeight = m.PreResizeState.Height + yOffset
		}

		if newWidth < DefaultWindowWidth {
			newWidth = DefaultWindowWidth
			if m.ResizeCorner == TopLeft || m.ResizeCorner == BottomLeft {
				newX = m.PreResizeState.X + m.PreResizeState.Width - DefaultWindowWidth
			}
		}
		if newHeight < DefaultWindowHeight {
			newHeight = DefaultWindowHeight
			if m.ResizeCorner == TopLeft || m.ResizeCorner == TopRight {
				newY = m.PreResizeState.Y + m.PreResizeState.Height - DefaultWindowHeight
			}
		}

		// Prevent resizing into dock area
		maxY := m.GetUsableHeight()
		if newY+newHeight > maxY {
			if m.ResizeCorner == BottomLeft || m.ResizeCorner == BottomRight {
				newHeight = maxY - newY
			}
		}
		if newY+newHeight > maxY {
			newY = maxY - newHeight
		}

		// Apply the resize
		focusedWindow.X = newX
		focusedWindow.Y = newY
		focusedWindow.Width = max(newWidth, DefaultWindowWidth)
		focusedWindow.Height = max(newHeight, DefaultWindowHeight)

		focusedWindow.Resize(focusedWindow.Width, focusedWindow.Height)
		focusedWindow.MarkPositionDirty()

		return m, nil
	}

	return m, nil
}

func (m *OS) handleMouseRelease(msg tea.MouseReleaseMsg) (*OS, tea.Cmd) {
	// Mouse release forwarding removed to prevent terminal corruption

	// Always consume release events to prevent leaking to terminals

	// Handle text selection completion
	if m.SelectionMode {
		focusedWindow := m.GetFocusedWindow()
		if focusedWindow != nil && focusedWindow.IsSelecting {
			// Extract selected text from terminal
			selectedText := m.extractSelectedText(focusedWindow)
			if selectedText != "" {
				focusedWindow.SelectedText = selectedText
				m.ShowNotification(fmt.Sprintf("Selected %d chars - Press 'c' to copy", len(selectedText)), "success", time.Duration(NotificationDuration)*time.Millisecond)
			}
			focusedWindow.IsSelecting = false
			return m, nil
		}
	}

	// Handle window drop in tiling mode
	if m.Dragging && m.AutoTiling && m.DraggedWindowIndex >= 0 && m.DraggedWindowIndex < len(m.Windows) {
		mouse := msg.Mouse()

		// Find which window is under the cursor (excluding the dragged window)
		targetWindowIndex := -1
		for i := range m.Windows {
			if i == m.DraggedWindowIndex || m.Windows[i].Minimized || m.Windows[i].Minimizing {
				continue
			}
			// Only consider windows in current workspace
			if m.Windows[i].Workspace != m.CurrentWorkspace {
				continue
			}

			w := m.Windows[i]
			if mouse.X >= w.X && mouse.X < w.X+w.Width &&
				mouse.Y >= w.Y && mouse.Y < w.Y+w.Height {
				targetWindowIndex = i
				break
			}
		}

		if targetWindowIndex >= 0 && targetWindowIndex != m.DraggedWindowIndex {
			// Swap windows - dragged window goes to target's position, target goes to dragged window's original position
			m.SwapWindowsWithOriginal(m.DraggedWindowIndex, targetWindowIndex, m.TiledX, m.TiledY, m.TiledWidth, m.TiledHeight)
		} else {
			// No swap, just retile to restore proper positions
			m.TileAllWindows()
		}
		m.DraggedWindowIndex = -1
	}

	// Clean up interaction state on mouse release
	if m.Dragging || m.Resizing {
		m.Dragging = false
		m.Resizing = false
		m.InteractionMode = false

		for i := range m.Windows {
			m.Windows[i].IsBeingManipulated = false
		}
	} else {
		// Even if we weren't dragging/resizing, clear interaction mode from click
		m.InteractionMode = false
	}

	// Mouse edge snapping disabled - use keyboard shortcuts for snapping

	return m, nil
}

// moveSelectionCursor moves the selection cursor and handles text selection logic.
// Parameters:
//   - window: The window to operate on
//   - dx, dy: Direction to move cursor (-1, 0, 1)
//   - extending: true if extending selection (Shift+Arrow), false if just moving cursor
func (m *OS) moveSelectionCursor(window *Window, dx, dy int, extending bool) {
	if window.Terminal == nil {
		return
	}

	screen := window.Terminal.Screen()
	if screen == nil {
		return
	}

	// Get terminal dimensions (account for borders)
	maxX := window.Width - 2
	maxY := window.Height - 2

	// Initialize selection cursor if not set (only for non-extending moves)
	if !extending && !window.IsSelecting {
		// Position at terminal cursor when starting cursor movement
		cursor := screen.Cursor()
		window.SelectionCursor.X = cursor.X
		window.SelectionCursor.Y = cursor.Y
	}

	// Move cursor
	newX := window.SelectionCursor.X + dx
	newY := window.SelectionCursor.Y + dy

	// Boundary checking
	if newX < 0 {
		newX = 0
	}
	if newX >= maxX {
		newX = maxX - 1
	}
	if newY < 0 {
		newY = 0
	}
	if newY >= maxY {
		newY = maxY - 1
	}

	// Update cursor position
	window.SelectionCursor.X = newX
	window.SelectionCursor.Y = newY

	if extending {
		// Extending selection - update selection end
		if !window.IsSelecting {
			// Start selection
			window.IsSelecting = true
			window.SelectionStart = window.SelectionCursor
		}
		window.SelectionEnd = window.SelectionCursor

		// Extract selected text
		selectedText := m.extractSelectedText(window)
		window.SelectedText = selectedText

	} else {
		// Just moving cursor - start new selection
		if window.IsSelecting || window.SelectedText != "" {
			// Clear existing selection
			window.IsSelecting = false
			window.SelectedText = ""
		}

		// Start new selection at cursor position
		window.SelectionStart = window.SelectionCursor
		window.SelectionEnd = window.SelectionCursor
		window.IsSelecting = true
	}

	window.InvalidateCache()
}

// handleMouseWheel handles mouse wheel events
func (m *OS) handleMouseWheel(msg tea.MouseWheelMsg) (*OS, tea.Cmd) {
	// Handle scrolling in help and log viewers
	if m.ShowHelp {
		switch msg.Button {
		case tea.MouseWheelUp:
			if m.HelpScrollOffset > 0 {
				m.HelpScrollOffset--
			}
		case tea.MouseWheelDown:
			m.HelpScrollOffset++
		}
		return m, nil
	}

	if m.ShowLogs {
		switch msg.Button {
		case tea.MouseWheelUp:
			if m.LogScrollOffset > 0 {
				m.LogScrollOffset--
			}
		case tea.MouseWheelDown:
			m.LogScrollOffset++
		}
		return m, nil
	}

	// Mouse wheel forwarding to terminals removed to prevent corruption
	// Terminal applications handle their own scrolling when they need it

	return m, nil
}

// handleClipboardPaste processes clipboard content and sends it to the focused terminal
func (m *OS) handleClipboardPaste() {
	if m.FocusedWindow < 0 || m.FocusedWindow >= len(m.Windows) {
		return
	}

	focusedWindow := m.GetFocusedWindow()
	if focusedWindow == nil {
		return
	}

	if m.ClipboardContent == "" {
		m.ShowNotification("Clipboard is empty", "warning", time.Duration(NotificationDuration)*time.Millisecond)
		return
	}

	// Use bracketed paste mode if supported (most modern terminals)
	var inputData []byte

	// Check if terminal likely supports bracketed paste
	termEnv := os.Getenv("TERM")
	supportsBracketedPaste := strings.Contains(termEnv, "xterm") ||
		strings.Contains(termEnv, "screen") ||
		strings.Contains(termEnv, "tmux") ||
		termEnv == "alacritty" || termEnv == "kitty"

	if supportsBracketedPaste {
		// Use bracketed paste mode to preserve formatting and prevent command execution
		inputData = []byte("\x1b[200~" + m.ClipboardContent + "\x1b[201~")
	} else {
		// Direct paste for terminals that don't support bracketed paste
		inputData = []byte(m.ClipboardContent)
	}

	err := focusedWindow.SendInput(inputData)
	if err != nil {
		m.ShowNotification(fmt.Sprintf("Failed to paste: %v", err), "error", time.Duration(NotificationDuration)*time.Millisecond)
	} else {
		m.ShowNotification(fmt.Sprintf("Pasted %d characters", len(m.ClipboardContent)), "success", time.Duration(NotificationDuration)*time.Millisecond)
	}
}
