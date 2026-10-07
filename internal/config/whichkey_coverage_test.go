package config

import (
	"slices"
	"testing"
)

// prefixTables pairs every which-key panel with the config section it
// describes.
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
	{"layout", func(c *UserConfig) map[string][]string { return c.Keybindings.LayoutPrefix }},
}

// TestWhichKeyTablesMatchTheKeymap keeps the which-key layouts honest. The
// keys come from the registry, but which actions a menu shows, and what each
// row says, is written by hand. An action added to a prefix section with no
// row falls to the menu's untitled end with the registry's generic text;
// ctrl+b b and ctrl+b o once shipped unlisted for exactly that reason. Every
// action a prefix binds by default must have a row, and every row must name
// an action that prefix binds, in both the local and the daemon layout.
func TestWhichKeyTablesMatchTheKeymap(t *testing.T) {
	cfg := DefaultConfig()
	for _, table := range prefixTables {
		name := table.name
		if name == "" {
			name = "leader"
		}
		for _, daemon := range []bool{false, true} {
			t.Run(name, func(t *testing.T) {
				section := table.section(cfg)
				rows := PrefixMenuActions(table.name, MenuState{Daemon: daemon})
				for action := range section {
					if !slices.Contains(rows, action) {
						t.Errorf("%s prefix (daemon %v) binds %q, and the which-key layout has no row for it", name, daemon, action)
					}
				}
				for _, action := range rows {
					if _, ok := section[action]; !ok {
						t.Errorf("the %s which-key layout (daemon %v) has a row for %q, which that prefix does not bind", name, daemon, action)
					}
				}
			})
		}
	}
}
