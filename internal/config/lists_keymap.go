package config

import "github.com/Gaurav-Gosain/tuios/internal/listnav"

// ListKeymap is the keys [keybindings.lists] gives each list motion. An action
// the section does not name keeps its default keys, and one set to [] has none.
// Every list in tuios moves through it: the overlays, the explorers behind
// keybinds browse and config browse, the composer's pager and the agents view.
func ListKeymap(cfg *UserConfig) *listnav.Keymap {
	if cfg == nil {
		return nil
	}
	lists := cfg.Keybindings.Lists
	bindings := map[listnav.Motion][]string{}
	for action, motion := range map[string]listnav.Motion{
		ActionListUp:       listnav.Up,
		ActionListDown:     listnav.Down,
		ActionListPageUp:   listnav.PageUp,
		ActionListPageDown: listnav.PageDown,
		ActionListFirst:    listnav.Home,
		ActionListLast:     listnav.End,
	} {
		if keys, ok := lists[action]; ok {
			bindings[motion] = keys
		}
	}
	return listnav.NewKeymap(bindings)
}
