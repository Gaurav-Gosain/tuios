package config

import (
	"sort"
	"strings"
)

// SubPrefixKeybindingGroups is the which-key panel of a nested prefix menu
// (see SubPrefixes), drawn from the bindings in force rather than a
// hand-written table: these menus exist to be rearranged in config.toml, so
// the panel shows the keys the config gives them. A key that opens another
// menu is marked as one. Menus come first, then the keys in order, and the
// menu's own cancel last.
func SubPrefixKeybindingGroups(k *KeybindingsConfig, section string) []KeybindingGroup {
	sp, ok := SubPrefixFor(section)
	if !ok || k == nil {
		return nil
	}
	gates := map[string]string{}
	for _, other := range SubPrefixes {
		gates[other.Gate] = other.Title
	}
	var rows []Keybinding
	var cancel *Keybinding
	for action, keys := range k.section(section) {
		if len(keys) == 0 {
			continue
		}
		desc := ActionDescriptions[action]
		if desc == "" {
			desc = action
		}
		row := Keybinding{Key: strings.Join(keys, "/"), Description: desc, Action: action}
		if title, gate := gates[action]; gate {
			row.Description, row.Submenu = title, true
		}
		if action == sp.CancelAction() {
			cancel = &row
			continue
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
	if cancel != nil {
		rows = append(rows, *cancel)
	}
	return []KeybindingGroup{{Bindings: rows}}
}
