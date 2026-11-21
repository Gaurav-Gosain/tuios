package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

func selectWindowByIndex(num int, o *app.OS) {
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

func handleLeftKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.ShowHelp && !o.HelpSearchMode {
		o.HelpStepCategory(-1)
		return o, nil
	}
	return o, nil
}

func handleRightKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.ShowHelp && !o.HelpSearchMode {
		o.HelpStepCategory(1)
		return o, nil
	}
	return o, nil
}
