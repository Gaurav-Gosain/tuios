package config

// DefaultPrefixRepeat is the prefix commands that stay armed when
// keybindings.repeat is unset, so the same key pressed again continues rather
// than being typed into a pane.
//
// They are the ones with a direction or a size: walking panes, moving one,
// resizing one, cycling a width, stepping through workspaces. What they have
// in common is that one press is rarely the whole intention. tmux marks the
// same kind of binding with -r, and all seventeen of its repeatable defaults
// are of this shape.
//
// Nothing destructive is here, and nothing that opens a panel. A second press
// of those is either a mistake or a dismissal, and neither should be made
// easier to do by accident.
//
// It is derived: an action is in it when its entry in the action table has
// Repeat set.
var DefaultPrefixRepeat = prefixRepeatActions()
