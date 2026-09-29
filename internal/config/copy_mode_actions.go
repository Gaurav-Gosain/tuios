package config

// The actions that enter copy mode and open its search prompt in one key, the
// way tmux does it with "copy-mode \; send-keys ?". They have no default key.
// Bind them in any section, for example prefix_mode or global.
const (
	ActionCopyModeSearchForward  = "copy_mode_search_forward"
	ActionCopyModeSearchBackward = "copy_mode_search_backward"
)
