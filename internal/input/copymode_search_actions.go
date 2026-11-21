package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

func handleCopyModeSearchForward(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.EnterCopyModeSearch(false)
	return o, nil
}

func handleCopyModeSearchBackward(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.EnterCopyModeSearch(true)
	return o, nil
}

func copyModeSearchActionKey(msg tea.KeyPressMsg, o *app.OS, window *terminal.Window) (*app.OS, tea.Cmd, bool) {
	cm := window.CopyMode
	if cm == nil || cm.State != terminal.CopyModeNormal ||
		cm.Pending != terminal.CopyPendingNone || cm.PendingCount > 0 {
		return o, nil, false
	}
	isSearchAction := func(a string) bool {
		return a == config.ActionCopyModeSearchForward || a == config.ActionCopyModeSearchBackward ||
			a == config.ActionClearScrollback
	}
	action := sectionAction(msg, o, config.TableGlobal)
	if !isSearchAction(action) {
		if o.Mode == app.TerminalMode {
			action = sectionAction(msg, o, config.TableTerminalMode)
		} else if o.KeybindRegistry != nil {
			action = lookupAction(o, msg, o.KeybindRegistry.GetAction)
		}
	}
	if !isSearchAction(action) {
		return o, nil, false
	}
	return dispatchAction(action, msg, o)
}
