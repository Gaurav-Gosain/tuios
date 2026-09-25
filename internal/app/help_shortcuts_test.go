package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestHelpDebugKeysAreRealKeys guards the help overlay against listing a key
// chord that does not exist. The debug entries were once generated from action
// names by slicing off a prefix, which printed the action ("cache_stats") where
// the key ("c") belongs, and used a lowercase "d" for a chord that needs
// Shift+D.
func TestHelpDebugKeysAreRealKeys(t *testing.T) {
	registry := config.NewKeybindRegistry(config.DefaultConfig())
	categories := GetHelpCategories(registry, &config.Global)

	var debug []HelpBinding
	for _, cat := range categories {
		for _, b := range cat.Bindings {
			for _, key := range b.Keys {
				if strings.Contains(key, "_") {
					t.Errorf("help binding %q lists key %q, which is an action name, not a key chord",
						b.Description, key)
				}
			}
		}
		if cat.Name == "Debug" {
			debug = cat.Bindings
		}
	}

	if len(debug) == 0 {
		t.Fatal("help has no Debug category; the debug chords are undiscoverable")
	}

	// The real chords are leader, Shift+D, then one of l/c/k/a.
	want := map[string]bool{
		config.Global.LeaderKey + ", D, l": true,
		config.Global.LeaderKey + ", D, c": true,
		config.Global.LeaderKey + ", D, k": true,
		config.Global.LeaderKey + ", D, a": true,
	}
	for _, b := range debug {
		for _, key := range b.Keys {
			if !want[key] {
				t.Errorf("Debug category lists unexpected chord %q", key)
			}
			delete(want, key)
		}
	}
	for key := range want {
		t.Errorf("Debug category is missing chord %q", key)
	}
}
