package input

import (
	"slices"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// Key modes: see config/key_modes.go for what a mode is, and app/key_modes.go
// for the stack of modes that are on.

// handleEnterMode builds the handler of an enter_mode:NAME action. A name no
// [keybindings.modes.NAME] table declares turns on nothing; validation warns
// about it.
func handleEnterMode(action string) ActionHandler {
	name, _ := config.ModeNameOf(action)
	return func(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
		o.EnterKeyMode(name)
		return o, nil
	}
}

// handleLeaveMode turns off the key mode on top.
func handleLeaveMode(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.LeaveKeyMode(-1)
	return o, nil
}

// routeKeyMode gives msg to the key modes that are on, top first. It reads
// no key while the leader, a menu, an overlay, copy mode or nest mode owns
// the keyboard, and never the leader, so the leader works in every mode.
//
// esc turns off the top mode. Otherwise the top mode runs the action it
// binds to the key. A mode that is not exclusive passes a key it does not
// bind to the mode under it, and past the last one the key goes on to the
// window keys or the pane. An exclusive mode drops it. A once mode turns off
// after one key it reads: before its action runs, so an action that turns
// on another mode puts it on the stack that is left.
//
// ok is false when the key goes on.
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
