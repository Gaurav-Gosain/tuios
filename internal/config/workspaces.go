package config

import (
	"maps"
	"strings"
)

// WorkspacesConfig is the [workspaces] section. The daemon reads
// return_when_empty: it owns the window set, so it is the side that sees a
// workspace lose its last pane. The client reads new_window_when_empty: it
// is the side that knows a switch came from the person and not from a
// command that brings its own panes.
type WorkspacesConfig struct {
	// BaseIndex is the number the first workspace shows and is addressed by:
	// 1 (the default) numbers workspaces 1-9, 0 numbers them 0-8 and shifts
	// every workspace digit key down with the label. The daemon still numbers
	// workspaces from 1 internally; base_index is only the number a person
	// sees and presses. A pointer so an explicit 0 is told apart from a file
	// that never mentions it (default: 1).
	BaseIndex *int `toml:"base_index"`
	// ReturnWhenEmpty switches the session back to the workspace the person
	// came from when the workspace on screen loses its last pane, instead of
	// leaving the splash screen up. A pointer so an explicit false in the
	// file is told apart from a file that never mentions it (default: true).
	ReturnWhenEmpty *bool `toml:"return_when_empty"`
	// NewWindowWhenEmpty opens a window when the person switches to a
	// workspace that has no panes, as the new-window key would. The window
	// starts in the directory of the pane focused on the workspace they
	// came from (default: false).
	NewWindowWhenEmpty bool `toml:"new_window_when_empty"`
}

// BaseIndexInUse reports the base index in effect: 1 unless the file asks for
// 0. Any other value is treated as the default, so a bad value cannot shift
// only the keys or only the labels.
func (w WorkspacesConfig) BaseIndexInUse() int {
	if w.BaseIndex != nil && *w.BaseIndex == 0 {
		return 0
	}
	return 1
}

// IndexOffset is how far a displayed workspace number sits below the number
// the daemon uses: 0 with base_index = 1, 1 with base_index = 0. Its zero
// value is the default, so a Settings that never set it reads one-based.
func (w WorkspacesConfig) IndexOffset() int { return 1 - w.BaseIndexInUse() }

// ReturnsWhenEmpty reports whether return_when_empty is on. Unset means on.
func (w WorkspacesConfig) ReturnsWhenEmpty() bool {
	return w.ReturnWhenEmpty == nil || *w.ReturnWhenEmpty
}

// workspaceDigitPrefixes are the action-name prefixes whose trailing digit is
// a workspace number. base_index shifts that digit in their keys along with
// the number drawn on the pill, so every way of addressing a workspace moves
// together: the switch, move-and-follow, send-to-workspace and both prefix
// chords.
var workspaceDigitPrefixes = [...]string{
	"switch_workspace_",
	"move_and_follow_",
	"send_to_workspace_",
	"workspace_prefix_switch_",
	"workspace_prefix_move_",
}

// digitToShiftedSpell is the character shift+digit makes on a US keyboard.
// The workspace prefix's move keys use those characters rather than "shift+N".
var digitToShiftedSpell = map[byte]byte{
	'0': ')', '1': '!', '2': '@', '3': '#', '4': '$',
	'5': '%', '6': '^', '7': '&', '8': '*', '9': '(',
}

// workspaceActionNumber is the workspace number named by an action such as
// switch_workspace_3.
func workspaceActionNumber(action string) (int, bool) {
	for _, prefix := range workspaceDigitPrefixes {
		if rest, ok := strings.CutPrefix(action, prefix); ok &&
			len(rest) == 1 && rest[0] >= '1' && rest[0] <= '9' {
			return int(rest[0] - '0'), true
		}
	}
	return 0, false
}

// shiftWorkspaceDigitKey rewrites one default key so its workspace digit is
// want ('0' to '9'). It handles the three spellings the defaults use: a digit
// after '+', a bare digit, and the shifted character of a bare digit. A key
// with no workspace digit, such as a hand-bound function key, is left alone.
func shiftWorkspaceDigitKey(key string, want byte) (string, bool) {
	if i := strings.LastIndexByte(key, '+'); i >= 0 && i+1 < len(key) {
		d := key[i+1:]
		if len(d) == 1 && d[0] >= '0' && d[0] <= '9' {
			return key[:i+1] + string(want), true
		}
	}
	if len(key) == 1 {
		if key[0] >= '0' && key[0] <= '9' {
			return string(want), true
		}
		for _, shifted := range digitToShiftedSpell {
			if key[0] == shifted {
				return string(digitToShiftedSpell[want]), true
			}
		}
	}
	return key, false
}

// shiftedWorkspaceKeys is action's shipped keys with their workspace digit
// moved to the number a person sees when base_index is 0.
func shiftedWorkspaceKeys(keys []string, number int) []string {
	out := make([]string, len(keys))
	for i, key := range keys {
		out[i], _ = shiftWorkspaceDigitKey(key, byte('0'+number-1))
	}
	return out
}

// zeroBasedWorkspaceDefaults shifts defaults before user bindings are merged,
// so explicit bindings and the usual collision rules keep their precedence.
func zeroBasedWorkspaceDefaults(kb KeybindingsConfig) KeybindingsConfig {
	for _, table := range keybindTables {
		target := maps.Clone(*table.field(&kb))
		for action, keys := range target {
			if number, ok := workspaceActionNumber(action); ok {
				target[action] = shiftedWorkspaceKeys(keys, number)
			}
		}
		*table.field(&kb) = target
	}
	return kb
}
