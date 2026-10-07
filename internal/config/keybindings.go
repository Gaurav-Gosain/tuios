package config

// Keybinding is one line of a which-key panel: a key and what it does.
type Keybinding struct {
	Key         string
	Description string
	// Submenu marks a key that opens another prefix menu rather than doing
	// something itself. The panel draws it with a leading + so nested menus
	// can be told from actions at a glance.
	Submenu bool
	// Action names the row: the first action the menu built it from. It
	// stays the same when the row's keys are rebound, so code that finds a
	// row finds it by this and never by the keys a config may have moved.
	Action string
}

// KeybindingGroup is a titled section of a which-key panel. A panel with one
// untitled group is a plain list.
type KeybindingGroup struct {
	Title    string
	Bindings []Keybinding
}
