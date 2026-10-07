package config

// The actions that enter copy mode and open its search prompt in one key, the
// way tmux does it with "copy-mode \; send-keys ?". They have no default key.
// Bind them in any section, for example prefix_mode or global.
const (
	ActionCopyModeSearchForward  = "copy_mode_search_forward"
	ActionCopyModeSearchBackward = "copy_mode_search_backward"
)

// The copy-mode motions a config can bind, in [keybindings.copy_mode]. They
// are live only while a pane is in copy mode, in normal and visual selection.
// The vim keys 0 and $ do the same and are not bindings: copy mode reads them
// itself, with the other vim motions.
const (
	ActionCopyModeLineStart = "copy_mode_line_start"
	ActionCopyModeLineEnd   = "copy_mode_line_end"
)

// getDefaultCopyModeKeybinds returns copy mode's bindable keys. ctrl+a and
// ctrl+e are not defaults: ctrl+a is a common leader key, and ctrl+e scrolls
// one line in tmux's vi copy mode. Bind them here if you want them.
func getDefaultCopyModeKeybinds() map[string][]string {
	return map[string][]string{
		ActionCopyModeLineStart: {"home"},
		ActionCopyModeLineEnd:   {"end"},
	}
}
