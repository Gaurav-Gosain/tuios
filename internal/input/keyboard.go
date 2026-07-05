// Package input implements keyboard event handling for TUIOS.
package input

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// handleWindowCycle handles Alt+Tab/Opt+Tab window cycling in terminal mode.
// This allows cycling through windows without needing the prefix key.
// On macOS, opt+tab produces ⇥ and opt+shift+tab produces ⇤.
func handleWindowCycle(msg tea.KeyPressMsg, o *app.OS) bool {
	keyStr := msg.String()

	// Check for macOS Option+Tab unicode characters first
	if len(keyStr) > 0 {
		if dir := IsMacOSOptionTab([]rune(keyStr)[0]); dir != "" {
			if o.AutoTiling && o.UseScrollingLayout {
				if dir == "next" {
					o.ScrollingFocusRight()
				} else {
					o.ScrollingFocusLeft()
				}
			} else {
				if dir == "next" {
					o.CycleToNextVisibleWindow()
				} else {
					o.CycleToPreviousVisibleWindow()
				}
			}
			if newFocused := o.GetFocusedWindow(); newFocused != nil {
				newFocused.InvalidateCache()
			}
			return true
		}
	}

	// Linux/Windows alt+n/alt+p fallback (alt+tab conflicts with OS window switcher)
	switch keyStr {
	case "alt+n":
		if o.AutoTiling && o.UseScrollingLayout {
			o.ScrollingFocusRight()
		} else {
			o.CycleToNextVisibleWindow()
		}
		if newFocused := o.GetFocusedWindow(); newFocused != nil {
			newFocused.InvalidateCache()
		}
		return true
	case "alt+p":
		if o.AutoTiling && o.UseScrollingLayout {
			o.ScrollingFocusLeft()
		} else {
			o.CycleToPreviousVisibleWindow()
		}
		if newFocused := o.GetFocusedWindow(); newFocused != nil {
			newFocused.InvalidateCache()
		}
		return true
	}
	return false
}

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

func handleUpKey(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
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

func handleDownKey(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
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

func handleShiftUpKey(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.SelectionMode && o.FocusedWindow >= 0 && o.FocusedWindow < len(o.Windows) {
		focusedWindow := o.GetFocusedWindow()
		if focusedWindow != nil {
			o.MoveSelectionCursor(focusedWindow, 0, -1, true) // true = extending selection
		}
		return o, nil
	}
	return o, nil
}

func handleShiftDownKey(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.SelectionMode && o.FocusedWindow >= 0 && o.FocusedWindow < len(o.Windows) {
		focusedWindow := o.GetFocusedWindow()
		if focusedWindow != nil {
			o.MoveSelectionCursor(focusedWindow, 0, 1, true) // true = extending selection
		}
		return o, nil
	}
	return o, nil
}

func handleShiftLeftKey(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.SelectionMode && o.FocusedWindow >= 0 && o.FocusedWindow < len(o.Windows) {
		focusedWindow := o.GetFocusedWindow()
		if focusedWindow != nil {
			o.MoveSelectionCursor(focusedWindow, -1, 0, true) // true = extending selection
		}
		return o, nil
	}
	return o, nil
}

func handleShiftRightKey(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.SelectionMode && o.FocusedWindow >= 0 && o.FocusedWindow < len(o.Windows) {
		focusedWindow := o.GetFocusedWindow()
		if focusedWindow != nil {
			o.MoveSelectionCursor(focusedWindow, 1, 0, true) // true = extending selection
		}
		return o, nil
	}
	return o, nil
}
