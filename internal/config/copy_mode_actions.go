package config

import (
	"fmt"
	"maps"
	"slices"
)

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

// copyModeFixedKeys are the keys copy mode reads itself, in
// internal/input/copymode_handlers.go and copymode_multi.go, besides the
// digits of a count. A [keybindings.copy_mode] binding on one of them takes
// the key from copy mode.
var copyModeFixedKeys = map[string]bool{
	"h": true, "j": true, "k": true, "l": true,
	"left": true, "down": true, "up": true, "right": true,
	"w": true, "b": true, "e": true, "W": true, "B": true, "E": true,
	"0": true, "^": true, "$": true, "g": true, "G": true,
	"H": true, "M": true, "L": true, "{": true, "}": true, "%": true,
	"f": true, "F": true, "t": true, "T": true, ";": true, ",": true,
	"ctrl+u": true, "ctrl+d": true, "ctrl+b": true, "ctrl+f": true,
	"pgup": true, "pgdown": true, "ctrl+l": true,
	"/": true, "?": true, "n": true, "N": true,
	"v": true, "V": true, "y": true, "Y": true, "c": true,
	"i": true, "q": true, "esc": true, "tab": true,
}

// copyModeFixedKey reports whether copy mode reads key itself.
func copyModeFixedKey(key string) bool {
	return copyModeFixedKeys[key] || len(key) == 1 && key[0] >= '0' && key[0] <= '9'
}

// CopyModeProblem is a [keybindings.copy_mode] key that another copy-mode key
// also uses.
type CopyModeProblem struct {
	Action  string `json:"action"`
	Key     string `json:"key"`
	Problem string `json:"problem"`
}

// CopyModeProblems lists the [keybindings.copy_mode] keys that a copy pipe
// entry or one of copy mode's own keys also uses, by action and then key.
// validateCopyModeKeys warns with it at load, and the doctor prints it.
func (k *KeybindingsConfig) CopyModeProblems() []CopyModeProblem {
	var out []CopyModeProblem
	for _, action := range slices.Sorted(maps.Keys(k.CopyMode)) {
		for _, key := range k.CopyMode[action] {
			canon := CanonicalKey(key)
			_, piped := k.CopyPipeFor(canon)
			switch {
			case canon == "":
				continue
			case piped:
				out = append(out, CopyModeProblem{Action: action, Key: key, Problem: fmt.Sprintf(
					"A [[keybindings.copy_pipe]] entry uses the key %s. The copy pipe runs, and %s does not. Use a different key.", key, action)})
			case copyModeFixedKey(canon):
				out = append(out, CopyModeProblem{Action: action, Key: key, Problem: fmt.Sprintf(
					"Copy mode uses the key %s. This binding takes the key from copy mode. Use a different key, for example home or end.", key)})
			}
		}
	}
	return out
}

// validateCopyModeKeys warns about each copy_mode key that another copy-mode
// key also uses.
func validateCopyModeKeys(cfg *UserConfig, result *ValidationResult) {
	for _, p := range cfg.Keybindings.CopyModeProblems() {
		result.Warnings = append(result.Warnings, ValidationError{Field: "keybindings.copy_mode." + p.Action, Key: p.Key, Message: p.Problem})
	}
}

// getDefaultCopyModeKeybinds returns copy mode's bindable keys. ctrl+a and
// ctrl+e are not defaults: ctrl+a is a common leader key, and ctrl+e scrolls
// one line in tmux's vi copy mode. Bind them here if you want them.
func getDefaultCopyModeKeybinds() map[string][]string {
	return map[string][]string{
		ActionCopyModeLineStart: {"home"},
		ActionCopyModeLineEnd:   {"end"},
	}
}
