// Package input implements keyboard event handling for TUIOS.
package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// selectWindowByIndex focuses the num-th window of the current workspace, where
// num is 1-based and comes from the action name rather than from the key.
//
// It used to read the digit back out of msg.String(), which made the action a
// lie: select_window_3 rebound to f3 parsed 'f' as a digit and focused nothing,
// and there was no way to tell that from the window simply not existing. An
// action that only works on the key it happens to ship with is not a binding.
//
// The same function also used to fall through to corner-snapping when the key
// carried no ctrl and tiling was off, which is snap_corner_N's job and is
// registered as such. Reaching it from here meant a user who unbound corner
// snap got corner snap anyway, from the action they had bound instead.
func selectWindowByIndex(num int, o *app.OS) {
	// With tiling on, the index counts what is on screen, so the numbers match
	// what the user can see rather than a list that includes hidden panes.
	count := 0
	for i, win := range o.Windows {
		if win.Workspace != o.CurrentWorkspace {
			continue
		}
		if o.AutoTiling && win.Minimized {
			continue
		}
		count++
		if count == num {
			o.FocusWindow(i)
			return
		}
	}
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
