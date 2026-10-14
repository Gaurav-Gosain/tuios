package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// selectWindowByIndex focuses the num-th window of the current workspace, where
// num is 1-based and comes from the action name rather than from the key.
//
// Reading the digit back out of msg.String() would make the action a lie:
// select_window_3 rebound to f3 would parse 'f' as a digit and focus nothing,
// with no way to tell that from the window simply not existing. An action that
// only works on the key it happens to ship with is not a binding.
//
// It never falls through to corner-snapping. That is snap_corner_N's job and
// is registered as such, so a user who unbinds corner snap does not get it
// anyway from the action they bound instead.
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

// The left and right arrows walk the help's category strip. The help intercepts
// them by key in HandleTerminalModeKey and HandleWindowManagementModeKey and
// calls these directly; they are not bound actions.
func handleLeftKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	// Help menu category navigation
	if o.ShowHelp && !o.HelpSearchMode {
		o.HelpStepCategory(-1)
		return o, nil
	}
	return o, nil
}

func handleRightKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	// Help menu category navigation
	if o.ShowHelp && !o.HelpSearchMode {
		o.HelpStepCategory(1)
		return o, nil
	}
	return o, nil
}
