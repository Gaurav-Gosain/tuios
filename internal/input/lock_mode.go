package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

type menuKey struct {
	before app.MenuState
	held   bool
}

func startMenuKey(o *app.OS) menuKey {
	k := menuKey{before: o.MenuState(), held: o.LockHeld}
	o.LockHeld = false
	o.SetMenuState(app.MenuState{})
	return k
}

func (k menuKey) leaves(msg tea.KeyPressMsg) bool {
	if !k.held {
		return false
	}
	s := msg.String()
	return s == "esc" || s == "ctrl+c"
}

func (k menuKey) refuses(o *app.OS, action string) bool {
	return k.held && !prefixRepeats(o, action)
}

func (k menuKey) ran(o *app.OS, action string) bool {
	if !o.Settings.Lock || o.Mode != app.TerminalMode || o.MenuOpen() || !prefixRepeats(o, action) {
		return false
	}
	o.SetMenuState(k.before)
	o.LockHeld = true
	o.ClearPrefixRepeat()
	return true
}

func (k menuKey) unbound(msg tea.KeyPressMsg, o *app.OS) bool {
	if !k.held || o.Mode != app.TerminalMode {
		return false
	}
	typeIntoPane(msg, o, false)
	return true
}

func leaderWhileHeld(o *app.OS) {
	if o.LockHeld {
		o.LockHeld = false
		o.SetMenuState(app.MenuState{})
	}
}
