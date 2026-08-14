// Package input implements keyboard event handling for TUIOS.
package input

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

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

// The arrow keys belong to whatever overlay is up. Help scrolling is handled in
// HandleTerminalModeKey and HandleWindowManagementModeKey, so what is left here
// is the log viewer and the help's category strip.
func handleUpKey(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.ShowLogs {
		if o.LogScrollOffset > 0 {
			o.LogScrollOffset--
		}
		return o, nil
	}
	return o, nil
}

func handleDownKey(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.ShowLogs {
		o.LogScrollOffset++
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
	return o, nil
}
