package config

import "testing"

// TestDefaultHelpKeysAreBoundInNoOtherTable holds the rule that
// getDefaultHelpKeybinds states. The help keys are read ahead of every other
// route, so a default shared with another table takes that table's key while
// the explainer is up. The input package's TestHoverHelpShadowsNoDefaultBinding
// presses every such key; this test also covers the tables it cannot press,
// such as the GUI client's, which reads its keys before tuios sees them.
func TestDefaultHelpKeysAreBoundInNoOtherTable(t *testing.T) {
	k := DefaultConfig().Keybindings
	help := map[string]string{}
	for action, keys := range k.Help {
		for _, key := range keys {
			help[CanonicalKey(key)] = action
		}
	}
	for i := range keybindTables {
		table := keybindTables[i]
		if Table(i) == TableHelp {
			continue
		}
		for action, keys := range *table.field(&k) {
			for _, key := range keys {
				if helpAction, ok := help[CanonicalKey(key)]; ok {
					t.Errorf("%s is the default of help.%s and of %s.%s", key, helpAction, table.name, action)
				}
			}
		}
	}
}
