package config

import (
	"fmt"
	"maps"
	"slices"
)

// The review overlay's hunk and file motions and its stage keys, the keys a
// config can bind in [keybindings.review]. They are live only while the review overlay is open
// and no note is being typed. Its other keys are its own and are not
// bindings. ] [ } { need Option on some layouts, AZERTY among them, where
// Option is often the Alt that tuios reads (#585).
const (
	ActionReviewNextHunk = "review_next_hunk"
	ActionReviewPrevHunk = "review_prev_hunk"
	ActionReviewNextFile = "review_next_file"
	ActionReviewPrevFile = "review_prev_file"
	// The stage keys: the hunk under the cursor or the whole file into the
	// index or out of it, and the last of them undone.
	ActionReviewStageHunk   = "review_stage_hunk"
	ActionReviewUnstageHunk = "review_unstage_hunk"
	ActionReviewStageFile   = "review_stage_file"
	ActionReviewUnstageFile = "review_unstage_file"
	ActionReviewUndoStage   = "review_undo_stage"
)

// getDefaultReviewKeybinds returns the review overlay's bindable keys.
func getDefaultReviewKeybinds() map[string][]string {
	return map[string][]string{
		ActionReviewNextHunk: {"]"},
		ActionReviewPrevHunk: {"["},
		ActionReviewNextFile: {"}"},
		ActionReviewPrevFile: {"{"},
		// s, S and u were the overlay's own keys before staging came, so
		// stage is a (git add), unstage d, and undo z.
		ActionReviewStageHunk:   {"a"},
		ActionReviewUnstageHunk: {"d"},
		ActionReviewStageFile:   {"A"},
		ActionReviewUnstageFile: {"D"},
		ActionReviewUndoStage:   {"z"},
	}
}

// reviewFixedKeys are the keys the review overlay reads itself, in
// internal/input/review_input.go, outside the compare view and the note
// editor. A [keybindings.review] binding is looked up before them, so a
// binding on one of them takes the key from the overlay. Bind esc and q both
// and nothing closes it.
var reviewFixedKeys = map[string]bool{
	"j": true, "k": true, "down": true, "up": true,
	"pgdown": true, "pgup": true, "ctrl+d": true, "ctrl+u": true, "space": true,
	"g": true, "G": true, "home": true, "end": true,
	"tab": true, "enter": true, "c": true, "C": true, "e": true, "x": true,
	"S": true, "s": true, "h": true, "l": true, "left": true, "right": true,
	"u": true, "b": true, "w": true, "r": true, "esc": true, "q": true,
}

// ReviewProblem is a [keybindings.review] key that the review overlay also
// reads itself.
type ReviewProblem struct {
	Action  string `json:"action"`
	Key     string `json:"key"`
	Problem string `json:"problem"`
}

// ReviewProblems lists the [keybindings.review] keys that are also one of
// the review overlay's own keys, by action and then key. validateReviewKeys
// warns with it at load, and the doctor prints it.
func (k *KeybindingsConfig) ReviewProblems() []ReviewProblem {
	var out []ReviewProblem
	for _, action := range slices.Sorted(maps.Keys(k.Review)) {
		for _, key := range k.Review[action] {
			if canon := CanonicalKey(key); canon != "" && reviewFixedKeys[canon] {
				out = append(out, ReviewProblem{Action: action, Key: key, Problem: fmt.Sprintf(
					"The review overlay uses the key %s. This binding takes the key from the overlay. Use a different key.", key)})
			}
		}
	}
	return out
}

// validateReviewKeys warns about each review key that the review overlay
// also reads itself.
func validateReviewKeys(cfg *UserConfig, result *ValidationResult) {
	for _, p := range cfg.Keybindings.ReviewProblems() {
		result.Warnings = append(result.Warnings, ValidationError{Field: "keybindings.review." + p.Action, Key: p.Key, Message: p.Problem})
	}
}
