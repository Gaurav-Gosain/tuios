package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

func armIfRepeatable(o *app.OS, action string) {
	if prefixRepeats(o, action) {
		o.ArmPrefixRepeat()
	} else {
		o.ClearPrefixRepeat()
	}
}

func prefixRepeats(o *app.OS, action string) bool {
	return o.KeybindRegistry != nil && o.KeybindRegistry.PrefixRepeats(action)
}

func tryPrefixRepeat(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd, bool) {
	if !o.PrefixRepeatLive() {
		return o, nil, false
	}
	if o.KeybindRegistry == nil {
		o.ClearPrefixRepeat()
		return o, nil, false
	}
	action := tableAction(o, msg, o.KeybindRegistry, config.TablePrefixMode)
	if !prefixRepeats(o, action) {
		o.ClearPrefixRepeat()
		return o, nil, false
	}
	if app.PrefixWorkActions[action] {
		cmd, handled := runPrefixWork(action, o)
		if !handled {
			o.ClearPrefixRepeat()
			return o, nil, false
		}
		o.ArmPrefixRepeat()
		return o, cmd, true
	}
	m, cmd, ok := dispatchAction(action, msg, o)
	if !ok {
		o.ClearPrefixRepeat()
		return o, nil, false
	}
	m.ArmPrefixRepeat()
	return m, cmd, true
}
