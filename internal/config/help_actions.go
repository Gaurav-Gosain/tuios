package config

// Hover help's keys, the ones a config can bind in [keybindings.help]. They
// are read only while the explainer is up and no panel, prefix, menu, hints,
// labels, capture or review owns the keyboard. Help is a view, not a mode:
// every other key reaches the pane, so no default is a key a shell needs
// unmodified. toggle_hover_help, under the leader, is the only way in and out.
const (
	ActionToggleHoverHelp = "toggle_hover_help"
	ActionHelpNext        = "help_next"
	ActionHelpPrev        = "help_prev"
	ActionHelpDeeper      = "help_deeper"
	ActionHelpShallower   = "help_shallower"
	ActionHelpScrollUp    = "help_scroll_up"
	ActionHelpScrollDown  = "help_scroll_down"
	ActionHelpKeys        = "help_keys"
)

// getDefaultHelpKeybinds returns hover help's keys. Help keys are read ahead
// of every other route while the explainer is up, so no default here is a key
// another table binds: the shifted arrows were, and took the pane's scroll,
// copy mode's word moves and the rail's reorder. ctrl+alt with the arrows
// walks, left and right between the components, down and up into a
// component's parts and back out. ctrl+alt with the pages scrolls a long
// explanation, and alt+shift+h shows every key of the target.
func getDefaultHelpKeybinds() map[string][]string {
	return map[string][]string{
		ActionHelpNext:       {"ctrl+alt+right"},
		ActionHelpPrev:       {"ctrl+alt+left"},
		ActionHelpDeeper:     {"ctrl+alt+down"},
		ActionHelpShallower:  {"ctrl+alt+up"},
		ActionHelpScrollUp:   {"ctrl+alt+pgup"},
		ActionHelpScrollDown: {"ctrl+alt+pgdown"},
		ActionHelpKeys:       {"alt+shift+h"},
	}
}
