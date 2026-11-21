package config

import (
	"reflect"
	"slices"
	"strings"
)

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
	for _, menu := range kb.Menus {
		if menu.Keys != nil {
			canonicalizeTable(menu.Keys)
		}
	}
	for _, mode := range kb.Modes {
		if mode.Keys != nil {
			canonicalizeTable(mode.Keys)
		}
	}
	for i, name := range kb.Repeat {
		kb.Repeat[i] = CanonicalAction(name)
	}
	if kb.Repeat != nil {
		kb.Repeat = slices.Compact(kb.Repeat)
	}
}

func canonicalizeTable(table map[string][]string) {
	for name, keys := range table {
		canon := CanonicalAction(name)
		if canon == name {
			continue
		}
		delete(table, name)
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

func canonicalizeMenus(menus map[string]MenuSpec) {
	for name, spec := range menus {
		canonicalizeMenuRows(spec.Items)
		canonicalizeMenuRows(spec.Add)
		if spec.After != "" {
			spec.After = CanonicalAction(strings.TrimSpace(spec.After))
		}
		for i, a := range spec.Remove {
			spec.Remove[i] = CanonicalAction(strings.TrimSpace(a))
		}
		menus[name] = spec
	}
}

func canonicalizeMenuRows(rows []any) {
	for i, row := range rows {
		switch row := row.(type) {
		case string:
			if s := strings.TrimSpace(row); s != MenuSeparator {
				rows[i] = CanonicalAction(s)
			}
		case map[string]any:
			if a, ok := row["action"].(string); ok {
				row["action"] = CanonicalAction(strings.TrimSpace(a))
			}
		}
	}
}
