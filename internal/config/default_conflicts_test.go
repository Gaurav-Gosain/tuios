package config

import (
	"slices"
	"strings"
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

// TestDefaultConfigLeavesNoActionUnbound is the other half of the invariant
// e2e/tui TestStockConfigOpensNoConflicts holds: nothing should be resolved by
// taking a default action's only key away. Without this, the cheapest way to
// pass that test would be to unbind one side of every clash.
//
// Negative control: resolve the digit clash by emptying select_window_1 instead
// of moving corner snapping, and this fails.
func TestDefaultConfigLeavesNoActionUnbound(t *testing.T) {
	for _, b := range NewKeybindRegistry(DefaultConfig()).Bindings() {
		if b.Unbound {
			t.Errorf("the defaults ship %s [%s] with no key", b.Action, b.Section)
		}
	}
}

// TestValidateAgreesWithTheKeybindReport. Two conflict detectors that disagree
// means the quieter one is misleading, and the quiet one is the one that runs
// at startup. findConflicts now delegates, so this is a guard against anyone
// giving it opinions of its own again.
//
// Negative control, run and confirmed failing: restore the tilingModeActions
// and nonTilingModeActions partition in findConflicts and this fails on a
// config with the old digit clash, which the partition suppresses and the
// report reports.
func TestValidateAgreesWithTheKeybindReport(t *testing.T) {
	cfg := DefaultConfig()
	// The exact clash that shipped: corner snapping back on the bare digits.
	cfg.Keybindings.Layout["snap_corner_1"] = []string{"1"}

	report := NewKeybindRegistry(cfg).Collisions()
	if len(report) == 0 {
		t.Fatal("the keybind report does not see the clash, so this case proves nothing")
	}

	warned := false
	for _, line := range ConfigWarnings(cfg) {
		if strings.Contains(line, "select_window_1") && strings.Contains(line, "snap_corner_1") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("the keybind report names the clash and config validation stays silent:\n%s",
			strings.Join(ConfigWarnings(cfg), "\n"))
	}
}
