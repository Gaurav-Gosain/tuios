package config_test

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestLayoutPrefixDefaultsYieldToUserKeys is config pipeline behaviour: the
// master-stack keys (#321) became defaults under layout_prefix, and a config
// that already bound one of those keys to another action must keep it. Filling
// the default on the same key made the chord a coin flip between the two.
//
// The positive half: a key the user did not bind still gets its default.
func TestLayoutPrefixDefaultsYieldToUserKeys(t *testing.T) {
	cfg := writeConfig(t, "[keybindings]\nleader_key = \"ctrl+b\"\n\n"+
		"[keybindings.layout_prefix]\ntoggle_tiling = [\"o\"]\nrestore_all = [\"enter\"]\n")
	r := config.NewKeybindRegistry(cfg)

	for range 50 {
		if got := r.GetLayoutPrefixAction("o"); got != "toggle_tiling" {
			t.Fatalf("layout prefix o = %q, want the user's toggle_tiling", got)
		}
		if got := r.GetLayoutPrefixAction("enter"); got != "restore_all" {
			t.Fatalf("layout prefix enter = %q, want the user's restore_all", got)
		}
	}
	if keys, ok := cfg.Keybindings.LayoutPrefix["cycle_master_position"]; ok {
		t.Errorf("cycle_master_position was filled with %v over the user's o", keys)
	}
	if got := r.GetLayoutPrefixAction("m"); got != "focus_master" {
		t.Errorf("layout prefix m = %q, want the default focus_master", got)
	}
}
