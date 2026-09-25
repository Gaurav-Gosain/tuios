package config

import (
	"slices"
	"testing"
)

// TestCornerSnapMigrationMovesTheStaleDigits. Every config ever written carries
// snap_corner_N = ["N"], and fillMapDefaults only adds actions that are
// missing, so without a migration the fix would reach new installs only.
//
// Negative control, run and confirmed failing: drop the migrateCornerSnapDigits
// call from fillMissingKeybinds and this fails, which is the state in which the
// maintainer's own config would still show four conflicts after the fix.
func TestCornerSnapMigrationMovesTheStaleDigits(t *testing.T) {
	cfg := loadFromTOML(t, `
[keybindings.layout]
snap_corner_1 = ["1"]
snap_corner_2 = ["2"]
snap_corner_3 = ["3"]
snap_corner_4 = ["4"]
`)
	for i := 1; i <= 4; i++ {
		action := "snap_corner_" + string(rune('0'+i))
		if keys, ok := cfg.Keybindings.Layout[action]; ok {
			t.Errorf("%s kept its stale digit %v in [keybindings.layout]", action, keys)
		}
	}
	if got := NewKeybindRegistry(cfg).Collisions(); len(got) != 0 {
		t.Errorf("an old config still has %d conflict(s) after loading: %+v", len(got), got)
	}
	// And the feature came back under the chord.
	if got := NewKeybindRegistry(cfg).GetLayoutPrefixAction("1"); got != "snap_corner_1" {
		t.Errorf("the layout chord then 1 runs %q after the migration", got)
	}
}

// TestCornerSnapMigrationLeavesAChosenBindingAlone. Taking away a binding
// someone chose, in order to quiet a warning, is the worse bug.
//
// Negative control: drop the len(keys) == 1 && keys[0] == digit test in
// migrateCornerSnapDigits and both rows here fail.
func TestCornerSnapMigrationLeavesAChosenBindingAlone(t *testing.T) {
	cfg := loadFromTOML(t, `
[keybindings.layout]
snap_corner_1 = ["ctrl+alt+1"]
snap_corner_2 = ["2", "ctrl+alt+2"]
`)
	if got := cfg.Keybindings.Layout["snap_corner_1"]; !slices.Contains(got, "ctrl+alt+1") {
		t.Errorf("a deliberate binding was removed: snap_corner_1 = %v", got)
	}
	if got := cfg.Keybindings.Layout["snap_corner_2"]; !slices.Contains(got, "ctrl+alt+2") {
		t.Errorf("a deliberate binding was removed: snap_corner_2 = %v", got)
	}
}

// TestCornerSnapMigrationRunsOnce. A config written after the move already says
// where corner snapping lives, and the migration must not reach into it: a user
// who deliberately put a corner back on a digit gets to keep it.
//
// Negative control: drop the early return that looks for a corner under
// layout_prefix and this fails.
func TestCornerSnapMigrationRunsOnce(t *testing.T) {
	cfg := loadFromTOML(t, `
[keybindings.layout]
snap_corner_1 = ["1"]

[keybindings.layout_prefix]
snap_corner_2 = ["2"]
`)
	if got := cfg.Keybindings.Layout["snap_corner_1"]; !slices.Contains(got, "1") {
		t.Errorf("a config that already knew about the move was migrated again: snap_corner_1 = %v", got)
	}
}
