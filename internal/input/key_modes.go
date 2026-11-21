package input

import (
	"slices"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

func handleEnterMode(action string) ActionHandler {
	name, _ := config.ModeNameOf(action)
	return func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		o.EnterKeyMode(name)
		return o, nil
	}
}

func handleLeaveMode(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.LeaveKeyMode(-1)
	return o, nil
}

func routeKeyMode(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd, bool) {
	if !o.KeyModeOn() || o.MenuOpen() || o.PanelOwnsKeyboard() || o.Nested() ||
		isLeaderKey(msg, &o.Settings, o.HostBaseCode(msg)) {
		return o, nil, false
	}
	if w := o.GetFocusedWindow(); w != nil && w.CopyModeVisible() {
		return o, nil, false
	}
	if msg.String() == "esc" {
		o.LeaveKeyMode(-1)
		return o, nil, true
	}
	kb := &o.UserConfig.Keybindings
	for i, name := range slices.Backward(o.KeyModes()) {
		mode, _ := kb.Mode(name)
		action := lookupAction(o, msg, func(key string) string { return o.KeybindRegistry.GetModeAction(name, key) })
		if action == "" && !mode.Exclusive {
			continue
		}
		if mode.Once {
			o.LeaveKeyMode(i)
		}
		if action == "" || (mode.Once && action == config.ActionLeaveMode) {
			return o, nil, true
		}
		m, cmd, _ := dispatchAction(action, msg, o)
		return m, cmd, true
	}
	return o, nil, false
}
