package config

import (
	"slices"
	"testing"
)

// TestLegacyEscapeBindingMovesOffDetach covers the one binding whose meaning
// changed when the prefix handlers started honouring the config. Older configs
// list esc under prefix_detach, but the old hard-coded handler made esc leave
// terminal mode and never detach. Loading such a config must not turn esc into a
// detach key.
func TestLegacyEscapeBindingMovesOffDetach(t *testing.T) {
	cfg := &UserConfig{}
	cfg.Keybindings.PrefixMode = map[string][]string{
		"prefix_detach": {"d", "esc"},
	}

	fillMissingKeybinds(cfg, DefaultConfig())

	detach := cfg.Keybindings.PrefixMode["prefix_detach"]
	if slices.Contains(detach, "esc") {
		t.Errorf("prefix_detach = %v, want esc removed", detach)
	}
	if !slices.Contains(detach, "d") {
		t.Errorf("prefix_detach = %v, want d kept", detach)
	}
	if exit := cfg.Keybindings.PrefixMode["prefix_exit_mode"]; !slices.Contains(exit, "esc") {
		t.Errorf("prefix_exit_mode = %v, want esc", exit)
	}
}

// TestMigrationLeavesADeliberateEscapeDetachAlone checks the migration does not
// fight a user who has since said what they want: once prefix_exit_mode is
// present, the config was written by a version that knew about the split, and
// esc on prefix_detach is a deliberate choice.
func TestMigrationLeavesADeliberateEscapeDetachAlone(t *testing.T) {
	cfg := &UserConfig{}
	cfg.Keybindings.PrefixMode = map[string][]string{
		"prefix_detach":    {"esc"},
		"prefix_exit_mode": {"q"},
	}

	fillMissingKeybinds(cfg, DefaultConfig())

	if exit := cfg.Keybindings.PrefixMode["prefix_exit_mode"]; !slices.Contains(exit, "q") {
		t.Errorf("prefix_exit_mode = %v, want the user's own q binding kept", exit)
	}
}
