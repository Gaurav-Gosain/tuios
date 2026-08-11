package config

import (
	"strings"
	"testing"
)

// whichKeyRowKeys expands one which-key row's key column into the keys it
// stands for. The rows are written for a reader, so they use ranges ("0-9"),
// alternates ("|/\\", "d/Esc") and shift notation that the keymap spells out.
func whichKeyRowKeys(key string) []string {
	switch key {
	case "0-9":
		return []string{"0", "1", "2", "3", "4", "5", "6", "7", "8", "9"}
	case "1-9":
		return []string{"1", "2", "3", "4", "5", "6", "7", "8", "9"}
	case "Shift+1-9":
		return strings.Split("!@#$%^&*(", "")
	case "Shift+M":
		return []string{"M"}
	case "Shift+Tab":
		return []string{"shift+tab"}
	}
	var keys []string
	for k := range strings.SplitSeq(key, "/") {
		keys = append(keys, normalizeWhichKey(k))
	}
	return keys
}

// normalizeWhichKey folds a key the way the registry's lookup does: a single
// letter keeps its case, so M (restore all) stays distinct from m (minimize),
// and everything else is compared lowercased.
func normalizeWhichKey(k string) string {
	if len([]rune(k)) == 1 {
		return k
	}
	return strings.ToLower(k)
}

// prefixTables pairs every which-key panel with the config section it claims to
// describe. "layout" has no section of its own: its two rows are handled inline
// by the layout prefix, so it is checked for phantom rows only.
var prefixTables = []struct {
	name    string
	section func(*UserConfig) map[string][]string
}{
	{"", func(c *UserConfig) map[string][]string { return c.Keybindings.PrefixMode }},
	{"window", func(c *UserConfig) map[string][]string { return c.Keybindings.WindowPrefix }},
	{"minimize", func(c *UserConfig) map[string][]string { return c.Keybindings.MinimizePrefix }},
	{"workspace", func(c *UserConfig) map[string][]string { return c.Keybindings.WorkspacePrefix }},
	{"debug", func(c *UserConfig) map[string][]string { return c.Keybindings.DebugPrefix }},
	{"tape", func(c *UserConfig) map[string][]string { return c.Keybindings.TapePrefix }},
}

// TestWhichKeyTablesMatchTheKeymap is the guard that keeps the which-key panels
// honest. They are hand-written tables, so a command added to a prefix section
// is invisible until someone remembers to add a row; ctrl+b b and ctrl+b o
// shipped and stayed unlisted for exactly that reason. Every action a prefix
// binds must have a row, and every row must name a key that prefix binds.
func TestWhichKeyTablesMatchTheKeymap(t *testing.T) {
	cfg := DefaultConfig()

	for _, table := range prefixTables {
		name := table.name
		if name == "" {
			name = "leader"
		}
		t.Run(name, func(t *testing.T) {
			section := table.section(cfg)

			listed := map[string]bool{}
			for _, row := range GetPrefixKeybindings(table.name) {
				for _, k := range whichKeyRowKeys(row.Key) {
					listed[k] = true
				}
			}

			bound := map[string]string{}
			for action, keys := range section {
				for _, k := range keys {
					bound[normalizeWhichKey(k)] = action
				}
			}

			for action, keys := range section {
				covered := false
				for _, k := range keys {
					if listed[normalizeWhichKey(k)] {
						covered = true
						break
					}
				}
				if !covered {
					t.Errorf("%s prefix binds %q to %v, and the which-key panel lists none of them",
						name, action, keys)
				}
			}

			for _, row := range GetPrefixKeybindings(table.name) {
				for _, k := range whichKeyRowKeys(row.Key) {
					if bound[k] == "" {
						t.Errorf("the %s which-key panel offers %q (%q), which that prefix does not bind",
							name, row.Key, row.Description)
					}
				}
			}
		})
	}
}

// TestWhichKeyLeaderCoversTheDaemonRows checks the daemon variant of the leader
// panel, which swaps the detach and quit rows, still names only real keys.
func TestWhichKeyLeaderCoversTheDaemonRows(t *testing.T) {
	cfg := DefaultConfig()
	bound := map[string]bool{}
	for _, keys := range cfg.Keybindings.PrefixMode {
		for _, k := range keys {
			bound[normalizeWhichKey(k)] = true
		}
	}
	for _, row := range GetPrefixKeybindings("", true) {
		for _, k := range whichKeyRowKeys(row.Key) {
			if !bound[k] {
				t.Errorf("the daemon leader panel offers %q (%q), which the leader does not bind",
					row.Key, row.Description)
			}
		}
	}
}
