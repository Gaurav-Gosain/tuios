package config

// The review overlay's hunk and file motions, the keys a config can bind in
// [keybindings.review]. They are live only while the review overlay is open
// and no note is being typed. Its other keys are its own and are not
// bindings. ] [ } { need Option on some layouts, AZERTY among them, where
// Option is often the Alt that tuios reads (#585).
const (
	ActionReviewNextHunk = "review_next_hunk"
	ActionReviewPrevHunk = "review_prev_hunk"
	ActionReviewNextFile = "review_next_file"
	ActionReviewPrevFile = "review_prev_file"
)

// getDefaultReviewKeybinds returns the review overlay's bindable keys, the
// ones it has always used.
func getDefaultReviewKeybinds() map[string][]string {
	return map[string][]string{
		ActionReviewNextHunk: {"]"},
		ActionReviewPrevHunk: {"["},
		ActionReviewNextFile: {"}"},
		ActionReviewPrevFile: {"{"},
	}
}
