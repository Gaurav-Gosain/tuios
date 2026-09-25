package config

import (
	"testing"
)

// TestOlderConfigStillResolvesTheWorkspaceRenameKey is the guard this repo has
// already needed once: a config file written before a keybind existed must
// still resolve it, or the key works on a fresh install and is dead for
// everyone who already has a config.toml.
func TestOlderConfigStillResolvesTheWorkspaceRenameKey(t *testing.T) {
	// A config from before the action shipped: the workspace prefix section is
	// there with the user's own bindings, and has never heard of renaming.
	cfg := &UserConfig{}
	cfg.Keybindings.WorkspacePrefix = map[string][]string{
		"workspace_prefix_switch_1": {"1"},
		"workspace_prefix_switch_2": {"2"},
		"workspace_prefix_cancel":   {"esc"},
	}

	fillMissingKeybinds(cfg, DefaultConfig())

	keys := cfg.Keybindings.WorkspacePrefix["workspace_prefix_rename"]
	if len(keys) == 0 {
		t.Fatal("an older config did not get the workspace-rename default filled in")
	}

	// The user's own bindings survive the fill.
	if got := cfg.Keybindings.WorkspacePrefix["workspace_prefix_switch_1"]; len(got) == 0 || got[0] != "1" {
		t.Errorf("workspace_prefix_switch_1 = %v, want the config's own 1 kept", got)
	}

	// And the registry the workspace prefix handler asks resolves the key.
	r := NewKeybindRegistry(cfg)
	for _, k := range keys {
		if got := r.GetWorkspacePrefixAction(k); got != "workspace_prefix_rename" {
			t.Errorf("the workspace prefix key %q resolved to %q, want workspace_prefix_rename", k, got)
		}
	}

	// The chord must not have taken a key the numbers need.
	for _, digit := range []string{"1", "2", "3", "9"} {
		if r.GetWorkspacePrefixAction(digit) == "workspace_prefix_rename" {
			t.Errorf("rename swallowed the workspace key %q", digit)
		}
	}
}
