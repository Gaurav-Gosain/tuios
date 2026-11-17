package config

import (
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// TestKeybindingsDocNamesRealTablesAndActions reads docs/KEYBINDINGS.md and
// fails on a [keybindings.NAME] table that config.toml does not have, in the
// text or in a TOML example, and on a key of a TOML example that is not an
// action of its table. An example that names a removed or renamed action is
// one a person copies into a config that tuios then ignores.
func TestKeybindingsDocNamesRealTablesAndActions(t *testing.T) {
	data, err := os.ReadFile("../../docs/KEYBINDINGS.md")
	if err != nil {
		t.Fatal(err)
	}
	fixed := map[string]bool{}
	// defaults holds the actions each table binds by default. A table such
	// as [keybindings.sidebar] has actions of its own, such as cursor_down,
	// that LookupAction does not list.
	defaults := map[string]map[string]bool{}
	defaultKeys := DefaultConfig().Keybindings
	for _, tb := range keybindTables {
		fixed[tb.name] = true
		defaults[tb.name] = map[string]bool{}
		for action := range *tb.field(&defaultKeys) {
			defaults[tb.name][action] = true
		}
	}
	kbType := reflect.TypeFor[KeybindingsConfig]()
	// tableKind says what a [keybindings.NAME...] table holds: actions, the
	// fields of a struct, or nothing tuios reads.
	type tableKind struct {
		known   bool
		actions bool
		table   string
		fields  reflect.Type
	}
	kindOf := func(path []string) tableKind {
		switch {
		case len(path) == 0:
			return tableKind{known: true, fields: kbType}
		case fixed[path[0]] && len(path) == 1:
			return tableKind{known: true, actions: true, table: path[0]}
		case path[0] == "menus" && len(path) == 2:
			return tableKind{known: true, actions: true}
		case len(path) == 1:
			f, ok := kbStructField(kbType, path[0])
			if !ok {
				return tableKind{}
			}
			for f.Kind() == reflect.Pointer || f.Kind() == reflect.Slice || f.Kind() == reflect.Map {
				f = f.Elem()
			}
			if f.Kind() != reflect.Struct {
				return tableKind{known: true}
			}
			return tableKind{known: true, fields: f}
		}
		return tableKind{}
	}
	ref := regexp.MustCompile(`\[\[?keybindings((?:\.[a-z0-9_]+)*)\]\]?`)
	key := regexp.MustCompile(`^#?\s*([a-z][a-z0-9_]*)\s*=`)
	// The page names [keybindings.terminal] on purpose, as a table name that
	// tuios reports as wrong.
	wrongOnPurpose := map[string]bool{"[keybindings.terminal]": true}
	tables, keys := 0, 0
	var current *tableKind
	inTOML, fenced := false, false
	for i, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "```") {
			fenced = !fenced
			inTOML = fenced && strings.HasPrefix(line, "```toml")
			current = nil
			continue
		}
		for _, m := range ref.FindAllStringSubmatch(line, -1) {
			tables++
			path := strings.Split(strings.TrimPrefix(m[1], "."), ".")
			if m[1] == "" {
				path = nil
			}
			if !kindOf(path).known && !wrongOnPurpose[m[0]] {
				t.Errorf("KEYBINDINGS.md:%d: config.toml has no table %s", i+1, m[0])
			}
		}
		if !inTOML {
			continue
		}
		text := strings.TrimSpace(line)
		if strings.HasPrefix(strings.TrimLeft(text, "# "), "[") {
			current = nil
			if m := ref.FindStringSubmatch(text); m != nil {
				path := strings.Split(strings.TrimPrefix(m[1], "."), ".")
				if m[1] == "" {
					path = nil
				}
				k := kindOf(path)
				current = &k
			}
			continue
		}
		m := key.FindStringSubmatch(text)
		if m == nil || current == nil {
			continue
		}
		keys++
		switch {
		case current.actions:
			if _, ok := LookupAction(m[1]); !ok && !defaults[current.table][m[1]] && scopedDescriptions[current.table][m[1]] == "" {
				t.Errorf("KEYBINDINGS.md:%d: %q is not an action: %s", i+1, m[1], text)
			}
		case current.fields != nil:
			if _, ok := kbStructField(current.fields, m[1]); !ok {
				t.Errorf("KEYBINDINGS.md:%d: %q is not a key of its table: %s", i+1, m[1], text)
			}
		}
	}
	if tables < 20 || keys < 20 {
		t.Fatalf("found %d tables and %d keys in KEYBINDINGS.md, want the whole page", tables, keys)
	}
}

// kbStructField finds the field of struct t whose toml name is name.
func kbStructField(t reflect.Type, name string) (reflect.Type, bool) {
	for f := range t.Fields() {
		if tag, _, _ := strings.Cut(f.Tag.Get("toml"), ","); tag == name {
			return f.Type, true
		}
	}
	return nil, false
}
