package config

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// User-declared prefix menus, for keymaps that nest as deep as a tmux
// which-key tree. A menu is a table of its own, and any section binds the
// action open_menu:NAME to open it, so a menu can sit under the leader, under
// another menu, or on a key of its own:
//
//	[keybindings.menus.split]
//	title = "Split"
//
//	[keybindings.menus.split.keys]
//	prefix_split_vertical   = ["y"]
//	prefix_split_horizontal = ["x"]
//
//	[keybindings.window_prefix]
//	"open_menu:split" = ["s"]
//
// leader, t, s, y then splits. Nothing here ships bound: a menu exists only
// when a config declares it.

// OpenMenuPrefix starts the action that opens a declared menu.
const OpenMenuPrefix = "open_menu:"

// MenuSectionPrefix starts the section name of a declared menu's keys, as the
// report, doctor and unbind name the table: "menus.split".
const MenuSectionPrefix = "menus."

// MenuConfig is one [keybindings.menus.NAME] table.
type MenuConfig struct {
	// Title heads the which-key panel. The menu's name when empty.
	Title string `toml:"title,omitempty"`
	// Sticky keeps the menu open after a bound key runs, until esc or a key
	// the menu does not bind: tmux's -r, for keys pressed several times in a
	// row. The key that closes it is passed to the pane in terminal mode.
	Sticky bool `toml:"sticky,omitempty"`
	// Keys binds actions to keys, as every other section does.
	Keys map[string][]string `toml:"keys,omitempty"`
}

// OpenMenuAction is the action that opens the menu called name.
func OpenMenuAction(name string) string { return OpenMenuPrefix + name }

// MenuNameOf is the menu an open_menu:NAME action opens.
func MenuNameOf(action string) (string, bool) {
	name, ok := strings.CutPrefix(action, OpenMenuPrefix)
	return name, ok && name != ""
}

// validMenuName reports whether name can name a menu: lowercase letters,
// digits, dash and underscore, so it is a bare TOML key and has no dot to
// split a table path on.
func validMenuName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

// MenuNames is the menus tuios uses, in name order: the ones with a valid
// name. validateMenus warns about the others.
func (k *KeybindingsConfig) MenuNames() []string {
	if len(k.Menus) == 0 {
		return nil
	}
	names := make([]string, 0, len(k.Menus))
	for name := range k.Menus {
		if validMenuName(name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// Menu returns the menu called name.
func (k *KeybindingsConfig) Menu(name string) (MenuConfig, bool) {
	if !validMenuName(name) {
		return MenuConfig{}, false
	}
	m, ok := k.Menus[name]
	return m, ok
}

// MenuTitle is what heads the menu's panel.
func (k *KeybindingsConfig) MenuTitle(name string) string {
	if m, ok := k.Menu(name); ok && strings.TrimSpace(m.Title) != "" {
		return strings.TrimSpace(m.Title)
	}
	return name
}

// menuSection is the keys of the menu a "menus.NAME" section name refers to.
func (k *KeybindingsConfig) menuSection(section string) (map[string][]string, bool) {
	name, ok := strings.CutPrefix(section, MenuSectionPrefix)
	if !ok {
		return nil, false
	}
	m, ok := k.Menu(name)
	return m.Keys, ok
}

// SectionNamesFor is SectionNames with the menus this config declares.
func (k *KeybindingsConfig) SectionNamesFor() []string {
	names := SectionNames()
	for _, name := range k.MenuNames() {
		names = append(names, MenuSectionPrefix+name)
	}
	return names
}

// menuScopes is the scope of each declared menu, for ScopesFor. Its chord is
// what opens it: the chord of the scope that binds open_menu:NAME, then that
// key, found through however many menus it sits under. A menu nothing opens
// gets open_menu:NAME in the chord's place, which is what binding it would
// take.
func menuScopes(kb *KeybindingsConfig, scopes []Scope) []Scope {
	names := kb.MenuNames()
	if len(names) == 0 {
		return nil
	}
	chords := map[string]string{}
	var resolve func(name string, visiting map[string]bool) string
	resolve = func(name string, visiting map[string]bool) string {
		if c, ok := chords[name]; ok {
			return c
		}
		if visiting[name] {
			return ""
		}
		visiting[name] = true
		action := OpenMenuAction(name)
		for _, s := range scopes {
			for _, sec := range s.Sections {
				for _, key := range kb.section(sec)[action] {
					if key = strings.TrimSpace(key); key != "" {
						chords[name] = press(s.Chord, key)
						return chords[name]
					}
				}
			}
		}
		for _, parent := range names {
			if parent == name {
				continue
			}
			for _, key := range kb.Menus[parent].Keys[action] {
				if key = strings.TrimSpace(key); key == "" {
					continue
				}
				if p := resolve(parent, visiting); p != "" {
					chords[name] = press(p, key)
					return chords[name]
				}
			}
		}
		return ""
	}
	out := make([]Scope, 0, len(names))
	for _, name := range names {
		chord := resolve(name, map[string]bool{})
		if chord == "" {
			chord = OpenMenuAction(name)
		}
		out = append(out, Scope{
			ID: "prefix.menu." + name, Name: kb.MenuTitle(name) + " menu", Chord: chord,
			Sections: []string{MenuSectionPrefix + name},
			Reaches:  ReachChorded,
		})
	}
	return out
}

// MenuKeybindingGroups is the which-key panel of a declared menu, drawn from
// the keys it has: menus come first, then the keys in order, then the cancel
// key. A key that opens another menu is marked as one and shows that menu's
// title.
func MenuKeybindingGroups(kb *KeybindingsConfig, name string) []KeybindingGroup {
	menu, ok := kb.Menu(name)
	if !ok {
		return nil
	}
	var rows []Keybinding
	escBound := false
	for action, keys := range menu.Keys {
		keys = slices.DeleteFunc(slices.Clone(keys), func(k string) bool { return strings.TrimSpace(k) == "" })
		if len(keys) == 0 {
			continue
		}
		if slices.Contains(keys, "esc") {
			escBound = true
		}
		row := Keybinding{Key: strings.Join(keys, "/"), Description: menuRowDescription(kb, action), Action: action}
		if target, ok := MenuNameOf(action); ok {
			row.Description, row.Submenu = kb.MenuTitle(target), true
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Submenu != rows[j].Submenu {
			return rows[i].Submenu
		}
		a, b := strings.ToLower(rows[i].Key), strings.ToLower(rows[j].Key)
		if a != b {
			return a < b
		}
		return rows[i].Key > rows[j].Key // h before H
	})
	if !escBound {
		rows = append(rows, Keybinding{Key: "esc", Description: "Cancel", Action: "menu_cancel"})
	}
	return []KeybindingGroup{{Bindings: rows}}
}

// menuRowDescription is the sentence for an action in a menu's panel.
func menuRowDescription(kb *KeybindingsConfig, action string) string {
	if c, ok := kb.CommandFor(action); ok {
		return c.Label()
	}
	return describeAction(action)
}

// validateMenus warns about a menu tuios cannot use or a key that opens one
// that is not there.
func validateMenus(cfg *UserConfig, result *ValidationResult) {
	kb := &cfg.Keybindings
	for name := range kb.Menus {
		if !validMenuName(name) {
			result.Warnings = append(result.Warnings, ValidationError{
				Field: "keybindings.menus", Key: name,
				Message: fmt.Sprintf("The menu name %q is not usable and the menu is ignored. Use lowercase letters, digits, - and _.", name),
			})
		}
	}
	opened := map[string]bool{}
	check := func(section string, table map[string][]string) {
		for _, action := range sortedActions(table) {
			target, ok := MenuNameOf(action)
			if !ok || len(table[action]) == 0 {
				continue
			}
			if _, declared := kb.Menu(target); !declared {
				result.Warnings = append(result.Warnings, ValidationError{
					Field: section, Key: action,
					Message: fmt.Sprintf("%s opens a menu that is not declared, so the key does nothing. Add a [keybindings.menus.%s] table.", action, target),
				})
				continue
			}
			opened[target] = true
		}
	}
	for _, section := range keySections(kb) {
		check(section.name, section.keys)
	}
	for _, name := range kb.MenuNames() {
		if !opened[name] {
			result.Warnings = append(result.Warnings, ValidationError{
				Field: "keybindings.menus", Key: name,
				Message: fmt.Sprintf("Nothing opens the menu %s. Bind %s in a section, for example [keybindings.prefix_mode].", name, OpenMenuAction(name)),
			})
		}
	}
}

func sortedActions(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
