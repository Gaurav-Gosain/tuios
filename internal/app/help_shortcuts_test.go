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
	categories := GetHelpCategories(registry)

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
		config.LeaderKey + ", D, l": true,
		config.LeaderKey + ", D, c": true,
		config.LeaderKey + ", D, k": true,
		config.LeaderKey + ", D, a": true,
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

// TestHelpListsDebugChordsOnce checks that the debug chords are not duplicated
// into the Prefix category, which is where the mangled copies used to live.
func TestHelpListsDebugChordsOnce(t *testing.T) {
	registry := config.NewKeybindRegistry(config.DefaultConfig())

	seen := map[string]int{}
	for _, cat := range GetHelpCategories(registry) {
		for _, b := range cat.Bindings {
			for _, key := range b.Keys {
				if strings.Contains(key, ", D, ") || strings.Contains(key, ", d, ") {
					seen[key]++
				}
			}
		}
	}

	for key, count := range seen {
		if count > 1 {
			t.Errorf("debug chord %q is listed %d times in the help overlay", key, count)
		}
	}
}

// TestCommandPaletteShortcutsExist checks the shortcut hints the palette prints
// beside each command. A hint that names a chord the input layer does not
// implement is worse than no hint: it teaches the user a key that does nothing.
func TestCommandPaletteShortcutsExist(t *testing.T) {
	items := GetCommandPaletteItems()

	byName := make(map[string]CommandPaletteItem, len(items))
	for _, item := range items {
		byName[item.Name] = item
	}

	// Rename is leader+r. Leader+, opens the settings page.
	rename, ok := byName["Rename Window"]
	if !ok {
		t.Fatal("command palette has no Rename Window entry")
	}
	if rename.Shortcut != "prefix+r" {
		t.Errorf("Rename Window shortcut = %q, want %q", rename.Shortcut, "prefix+r")
	}

	settings, ok := byName["Settings"]
	if !ok {
		t.Fatal("command palette has no Settings entry")
	}
	if settings.Shortcut != "prefix+," {
		t.Errorf("Settings shortcut = %q, want %q", settings.Shortcut, "prefix+,")
	}

	// The aggregate view has no default binding, so it must not advertise one.
	aggregate, ok := byName["Aggregate View (All Windows)"]
	if !ok {
		t.Fatal("command palette has no Aggregate View entry")
	}
	if aggregate.Shortcut != "" {
		t.Errorf("Aggregate View advertises shortcut %q, but no key is bound to it", aggregate.Shortcut)
	}
}
