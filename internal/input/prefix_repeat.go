package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// armIfRepeatable keeps the prefix live when the command just run is one worth
// pressing again.
func armIfRepeatable(o *app.OS, action string) {
	if prefixRepeats(o, action) {
		o.ArmPrefixRepeat()
	} else {
		o.ClearPrefixRepeat()
	}
}

// prefixRepeats reports whether action stays armed after the prefix: one of
// keybindings.repeat, or of config.DefaultPrefixRepeat when that is unset.
func prefixRepeats(o *app.OS, action string) bool {
	return o.KeybindRegistry != nil && o.KeybindRegistry.PrefixRepeats(action)
}

// tryPrefixRepeat runs a repeatable prefix command without a second prefix
// press, when one arrives inside the window the last one left open.
//
// Only a repeatable command continues. A key inside the window that means
// something else, or nothing, closes the window and is handled as the ordinary
// key it is: the window exists to make one command easy to repeat, not to
// swallow whatever is typed next.
func tryPrefixRepeat(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd, bool) {
	if !o.PrefixRepeatLive() {
		return o, nil, false
	}
	if o.KeybindRegistry == nil {
		// Nothing can be looked up, so nothing can repeat. The window closes
		// rather than staying open against a registry that is not there.
		o.ClearPrefixRepeat()
		return o, nil, false
	}
	action := lookupAction(o, msg, o.KeybindRegistry.GetPrefixAction)
	if !prefixRepeats(o, action) {
		o.ClearPrefixRepeat()
		return o, nil, false
	}
	// A work action that did nothing closes the window and leaves the key to
	// the ordinary path, so it is not swallowed while its work is unbuilt.
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
