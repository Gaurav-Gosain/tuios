package config

import "testing"

// TestOlderConfigStillResolvesTheCloseSessionKey is the guard for the failure
// this repo has already had: a config file written before a keybind existed
// must still resolve it. Otherwise a new key works on a fresh install and is
// dead for every user who already has a config.toml, which is the whole
// installed base.
func TestOlderConfigStillResolvesTheCloseSessionKey(t *testing.T) {
	// A config from before the action shipped: the prefix section is present
	// and carries the user's own bindings, the new entry is not in it.
	cfg := &UserConfig{}
	cfg.Keybindings.PrefixMode = map[string][]string{
		"prefix_detach":    {"d"},
		"prefix_exit_mode": {"esc"},
		"prefix_quit":      {"q"},
	}

	fillMissingKeybinds(cfg, DefaultConfig())

	keys := cfg.Keybindings.PrefixMode["prefix_close_session"]
	if len(keys) == 0 {
		t.Fatal("an older config did not get the close-session default filled in")
	}

	// The user's own bindings survive the fill.
	if got := cfg.Keybindings.PrefixMode["prefix_detach"]; len(got) == 0 || got[0] != "d" {
		t.Errorf("prefix_detach = %v, want the config's own d kept", got)
	}

	// And the registry the prefix handler asks resolves the key to the action.
	r := NewKeybindRegistry(cfg)
	for _, k := range keys {
		if got := r.GetPrefixAction(k); got != "prefix_close_session" {
			t.Errorf("the prefix key %q resolved to %q, want prefix_close_session", k, got)
		}
	}

	// The key is one shift away from the key that closes a single pane, and the
	// two must never be the same key: only one of them can be undone.
	if r.GetPrefixAction("x") == "prefix_close_session" {
		t.Error("closing a pane and closing the session resolve to the same key")
	}
}
