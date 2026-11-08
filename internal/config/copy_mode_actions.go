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
// The vim keys 0, ^ and $ do the same and are not bindings: copy mode reads
// them itself, with the other vim motions. The line end and ^ go to the last
// and first nonblank characters of the row, not to the pane edge.
const (
	ActionCopyModeLineStart     = "copy_mode_line_start"
	ActionCopyModeFirstNonBlank = "copy_mode_first_nonblank"
	ActionCopyModeLineEnd       = "copy_mode_line_end"
	ActionCopyModeWordForward   = "copy_mode_word_forward"
	ActionCopyModeWordBackward  = "copy_mode_word_backward"
)

// ActionCopyModeExit leaves copy mode and gives the pane the keyboard back,
// as q and esc do. From a selection it copies the selection first, as y
// does. It is on enter by default, as tmux's copy-mode-vi has it (issue
// 1637). In the search prompt enter still runs the search. Set it to [] for
// the old behaviour, where enter does nothing in copy mode.
const ActionCopyModeExit = "copy_mode_exit"

// ActionEditScrollback writes the focused pane's scrollback to a file and
// opens it in the editor. It is bound after the leader and in copy mode.
const ActionEditScrollback = "edit_scrollback"

// ActionHints labels the text on the pane for copying, as the leader and F
// do. It is bound in copy mode too, where the labels go on the scrolled-back
// view the person is reading.
const ActionHints = "hints"

// ActionCopyMode starts copy mode over the text in the quick look preview,
// inside the file. It is the quick_look_inside binding for what the leader
// and [ do for a pane.
const ActionCopyMode = "copy_mode"

// ActionClearScrollback clears the focused pane's scrollback and the rows
// above its prompt, as cmd+k does in Ghostty and iTerm2. It has no default
// key: the host terminal takes cmd+k for itself unless told not to. Bind it
// in any section, global for a key that works while typing.
const ActionClearScrollback = "clear_scrollback"

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
	"v": true, "V": true, "ctrl+v": true, "o": true, "y": true, "Y": true, "c": true,
	"i": true, "q": true, "esc": true, "ctrl+c": true, "tab": true,
}

// CopyModeFixedKey reports whether copy mode reads key itself. A focus key
// on one of them keeps its copy-mode meaning while a pane is in copy mode.
func CopyModeFixedKey(key string) bool {
	return copyModeFixedKeys[key] || len(key) == 1 && key[0] >= '0' && key[0] <= '9'
}

// CopyModeProblem is a [keybindings.copy_mode] key that another copy-mode key
// also uses.
type CopyModeProblem struct {
	Action  string `json:"action"`
	Key     string `json:"key"`
	Problem string `json:"problem"`
	// Override is a key copy mode reads itself, taken on purpose by the
	// binding. The binding works, so it is reported by the doctor and not
	// warned about at startup. A key a copy pipe also uses is not an override:
	// the binding never runs.
	Override bool `json:"override,omitempty"`
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
			case CopyModeFixedKey(canon):
				out = append(out, CopyModeProblem{Action: action, Key: key, Override: true, Problem: fmt.Sprintf(
					"Copy mode uses the key %s. This binding takes the key from copy mode. Use a different key, for example home or end.", key)})
			}
		}
	}
	return out
}

// validateCopyModeKeys warns about each copy_mode key a copy pipe entry also
// uses, so the binding never runs. A key taken from copy mode's own keys is a
// choice that works, and only `tuios keybinds doctor` lists it.
func validateCopyModeKeys(cfg *UserConfig, result *ValidationResult) {
	for _, p := range cfg.Keybindings.CopyModeProblems() {
		if p.Override {
			continue
		}
		result.Warnings = append(result.Warnings, ValidationError{Field: "keybindings.copy_mode." + p.Action, Key: p.Key, Message: p.Problem})
	}
}

// getDefaultCopyModeKeybinds returns copy mode's bindable keys. ctrl+a and
// ctrl+e are not defaults: ctrl+a is a common leader key, and ctrl+e scrolls
// one line in tmux's vi copy mode. Bind them here if you want them.
//
// The editor is on ctrl+g. zellij puts it on e, which is the vim word-end
// motion here, and E (WORD end) and ctrl+e (scroll a line in tmux and vim)
// are taken the same way. ctrl+g is the key that opens a reply draft in the
// editor, so one key means "this text in my editor" in both places.
//
// Hints are on s, which copy mode does not read. F opens them after the
// leader, but F is the backward character search here.
func getDefaultCopyModeKeybinds() map[string][]string {
	return map[string][]string{
		ActionCopyModeLineStart:    {"home"},
		ActionCopyModeLineEnd:      {"end"},
		ActionCopyModeExit:         {"enter"},
		ActionCopyModeWordForward:  {"shift+right"},
		ActionCopyModeWordBackward: {"shift+left"},
		ActionEditScrollback:       {"ctrl+g"},
		ActionHints:                {"s"},
	}
}
