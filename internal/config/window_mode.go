package config

// WindowModeOn reports whether window-management mode is on: window_mode is
// unset or true.
func (k *KeybindingsConfig) WindowModeOn() bool {
	return k.WindowMode == nil || *k.WindowMode
}

var windowModeActions = map[string]bool{
	"enter_window_mode":  true,
	"terminal_exit_mode": true,
	"prefix_exit_mode":   true,
	"hold_window_mode":   true,
}

// IsWindowModeAction reports whether action only enters window mode.
func IsWindowModeAction(action string) bool { return windowModeActions[action] }

func (r *KeybindRegistry) hidesWithoutWindowMode(b Binding) bool {
	if r.config == nil || r.config.Keybindings.WindowModeOn() {
		return false
	}
	return b.Scope == ScopeWindowMode || windowModeActions[b.Action]
}
