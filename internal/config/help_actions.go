package config

// The help mode's navigation keys, the keys a config can bind in
// [keybindings.help]. They are live only while help mode is up: the pane reads
// them before anything else, and every key it does not bind is the app's, so a
// scope of its own keeps them from appearing as plain window-mode keys that
// would do the same thing everywhere. See help_mode.go in internal/app and
// help_mode_input.go in internal/input.
const (
	// ActionHelpNext and ActionHelpPrev walk the components on screen.
	ActionHelpNext = "help_next"
	ActionHelpPrev = "help_prev"
	// ActionHelpDeeper goes into the selected component's parts, or to its
	// next sibling once inside one.
	ActionHelpDeeper = "help_deeper"
	// ActionHelpShallower comes back out to the previous sibling, or to the
	// component itself.
	ActionHelpShallower = "help_shallower"
	// ActionHelpScrollUp and ActionHelpScrollDown move the explanation where
	// it overflows; the title and the footer stay put.
	ActionHelpScrollUp   = "help_scroll_up"
	ActionHelpScrollDown = "help_scroll_down"
	// ActionHelpKeys shows or hides the selected component's whole
	// keybinding set.
	ActionHelpKeys = "help_keys"
	// ActionHelpExit leaves help mode. It is a chord because esc is what a
	// pane wants for itself, and help must not take it.
	ActionHelpExit = "help_exit"
)

// getDefaultHelpKeybinds returns the keys help mode answers to. Each is a
// chord rather than a bare letter: the bare ones write into the pane once the
// explanation is gone, and h is the only letter the pane keeps.
func getDefaultHelpKeybinds() map[string][]string {
	return map[string][]string{
		// The shift arrows are what a person reaches for on a component and
		// what the docs name; shift+tab is the pair the previous binding used,
		// kept so a habit formed on it still walks.
		ActionHelpNext:       {"shift+right", "shift+tab"},
		ActionHelpPrev:       {"shift+left", "alt+shift+tab"},
		ActionHelpDeeper:     {"shift+down"},
		ActionHelpShallower:  {"shift+up"},
		ActionHelpScrollUp:   {"up"},
		ActionHelpScrollDown: {"down"},
		ActionHelpKeys:       {"h"},
		// shift+esc rather than esc: a pane may want esc, and the shifted form
		// is what the terminal reports apart from it. shift+f1, the other way
		// in, also leaves, through toggle_help_mode's own bind.
		ActionHelpExit: {"shift+esc"},
	}
}
