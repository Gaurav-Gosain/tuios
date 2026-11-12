package config

import (
	"reflect"
	"slices"
)

// canonicalizeKeybindings rewrites every action name in the keybinding
// tables and the repeat list to the action's own name, so an older name a
// config file, a preset or a tape still uses binds the action itself. When a
// table names an action by both names, the keys of both stay, its own name's
// first.
func canonicalizeKeybindings(kb *KeybindingsConfig) {
	v := reflect.ValueOf(kb).Elem()
	mapType := reflect.TypeFor[map[string][]string]()
	for i := range v.NumField() {
		f := v.Field(i)
		if f.Type() != mapType || f.IsNil() {
			continue
		}
		canonicalizeTable(f.Interface().(map[string][]string))
	}
	for i, name := range kb.Repeat {
		kb.Repeat[i] = CanonicalAction(name)
	}
	if kb.Repeat != nil {
		kb.Repeat = slices.Compact(kb.Repeat)
	}
}

// canonicalizeTable rewrites one table's action names in place.
func canonicalizeTable(table map[string][]string) {
	for name, keys := range table {
		canon := CanonicalAction(name)
		if canon == name {
			continue
		}
		delete(table, name)
		// An empty list unbinds the action, so the action's own name has to
		// be there even with no keys, or the defaults fill it back in.
		if _, ok := table[canon]; !ok {
			table[canon] = []string{}
		}
		for _, k := range keys {
			if !slices.Contains(table[canon], k) {
				table[canon] = append(table[canon], k)
			}
		}
	}
}
