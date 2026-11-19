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
	o.LeaveKeyMode()
	return o, nil
}

// keyModeReads reports whether the key modes read msg: a mode is on, and
// nothing that owns the keyboard ahead of them is up. The leader, its menus,
// the overlays, copy mode and nest mode keep their keys, so a person can
// always reach the leader from inside a mode, and an exclusive mode cannot
// take the keys of a panel opened over it.
func keyModeReads(msg tea.KeyPressMsg, o *app.OS) bool {
	if !o.KeyModeOn() || o.MenuOpen() || o.PanelOwnsKeyboard() || o.Nested() {
		return false
	}
	if w := o.GetFocusedWindow(); w != nil && w.CopyModeVisible() {
		return false
	}
	return !isLeaderKey(msg, &o.Settings, o.HostBaseCode(msg))
}

// routeKeyMode gives msg to the key modes that are on, top first. The top
// mode runs the action it binds to the key. A mode that is not exclusive
// passes a key it does not bind to the mode under it, and past the last one
// the key goes on to the window keys or the pane. An exclusive mode drops it.
//
// esc, when no mode on the stack binds it, turns off the top mode. A once
// mode turns off after one key it reads: before its action runs, so an
// action that turns on another mode puts it on the stack that is left.
//
// ok is false when the key goes on.
func routeKeyMode(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd, bool) {
	if !keyModeReads(msg, o) {
		return o, nil, false
	}
	kb := &o.KeybindRegistry.GetConfig().Keybindings
	modes := o.KeyModes()
	for i, name := range slices.Backward(modes) {
		mode, ok := kb.Mode(name)
		if !ok {
			continue
		}
		action := lookupAction(o, msg, func(key string) string { return o.KeybindRegistry.GetModeAction(name, key) })
		if action != "" {
			if mode.Once {
				o.LeaveKeyModeAt(i)
				if action == config.ActionLeaveMode {
					return o, nil, true
				}
			}
			m, cmd, _ := dispatchAction(action, msg, o)
			return m, cmd, true
		}
		if mode.Exclusive {
			break
		}
	}
	if msg.String() == "esc" {
		o.LeaveKeyMode()
		return o, nil, true
	}
	top, _ := kb.Mode(modes[len(modes)-1])
	if top.Exclusive {
		if top.Once {
			o.LeaveKeyMode()
		}
		return o, nil, true
	}
	return o, nil, false
}
