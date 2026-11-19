package config

// keybindings.window_mode: a config without window-management mode
// (discussion #2249). The model is in internal/app/window_mode_off.go. What is
// here is what the config and the keybind registry need to know about it.

// WindowModeOn reports whether window-management mode is on: window_mode is
// unset or true.
func (k *KeybindingsConfig) WindowModeOn() bool {
	return k.WindowMode == nil || *k.WindowMode
}

// windowModeActions are the actions whose only work is to enter window
// mode. With window mode off they do nothing but say so, so no help page,
// palette hint or leader menu lists a key for them.
var windowModeActions = map[string]bool{
	"enter_window_mode":  true,
	"terminal_exit_mode": true,
	"prefix_exit_mode":   true,
	"hold_window_mode":   true,
}

// IsWindowModeAction reports whether action only enters window mode.
func IsWindowModeAction(action string) bool { return windowModeActions[action] }

// hidesWithoutWindowMode reports whether a binding is one a reader cannot
// use while window mode is off: a key of the window-mode table, which is
// never live then, or a key whose action only enters window mode.
func (r *KeybindRegistry) hidesWithoutWindowMode(b Binding) bool {
	if r.config == nil || r.config.Keybindings.WindowModeOn() {
		return false
	}
	return b.Scope == ScopeWindowMode || windowModeActions[b.Action]
}
