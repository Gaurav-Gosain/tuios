package config

import (
	"slices"
	"strings"
	"testing"
)

// The shipped defaults must resolve every key to exactly one action.
//
// This is the invariant, and it is the one that was missing. Four bindings
// (select_window_1 through _4) had never fired in any default install, because
// the layout table put snap_corner_1 through _4 on the same digits and is
// merged into the window-mode keymap after window_management. Nothing caught
// it: findConflicts kept its own idea of which actions could compete and
// suppressed exactly this pair, so the only thing that ever said so was the
// keybind manager's Conflicts tab, on a config the user had never edited.
//
// A stock config that opens its own conflicts panel showing four dead bindings
// teaches the reader to distrust the panel, which is worse than the four dead
// bindings.

// TestDefaultConfigHasNoConflicts fails the build if any two default actions
// contest a key in one scope.
//
// Negative control, run and confirmed failing: put "1" back on snap_corner_1 in
// getDefaultLayoutKeybinds and this reports the collision by name. That is the
// exact state the tree shipped in.
func TestDefaultConfigHasNoConflicts(t *testing.T) {
	collisions := NewKeybindRegistry(DefaultConfig()).Collisions()
	if len(collisions) == 0 {
		return
	}
	var lines []string
	for _, c := range collisions {
		var dead []string
		for _, l := range c.Losers {
			dead = append(dead, l.Action+" ["+l.Section+"]")
		}
		lines = append(lines, "  "+c.Press+" in "+c.ScopeName+": runs "+c.Winner+
			" ["+winnerSection(c)+"], never runs "+strings.Join(dead, ", "))
	}
	t.Fatalf("the shipped defaults contest %d key(s). A stock config must resolve every key to one action:\n%s",
		len(collisions), strings.Join(lines, "\n"))
}

// winnerSection is the config table the winning binding came from. Collision
// does not carry it, and a failure message that named the losers' tables but
// not the winner's would send the reader to the wrong half of the file.
func winnerSection(c Collision) string {
	for _, b := range NewKeybindRegistry(DefaultConfig()).Bindings() {
		if b.Scope == c.Scope && b.Action == c.Winner && lookupForm(b.Key) == lookupForm(c.Key) {
			return b.Section
		}
	}
	return "?"
}

// TestDefaultConfigLeavesNoActionUnbound is the other half of the same
// invariant: nothing should be resolved by taking a default action's only key
// away. Without this, the cheapest way to pass the test above would be to
// unbind one side of every clash.
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

// TestDefaultDigitsSelectWindows. 5 through 9 always focused a window and 1
// through 4 never did, which is the incoherence a user actually meets before
// they ever open the conflicts panel.
//
// Negative control, run and confirmed failing: this fails on the unfixed tree
// for 1 through 4, which resolve to snap_corner_N.
func TestDefaultDigitsSelectWindows(t *testing.T) {
	r := NewKeybindRegistry(DefaultConfig())
	for _, d := range []string{"1", "2", "3", "4", "5", "6", "7", "8", "9"} {
		want := "select_window_" + d
		if got := r.GetAction(d); got != want {
			t.Errorf("pressing %q in window mode runs %q, want %q", d, got, want)
		}
	}
}

// TestCornerSnapIsStillReachable. Resolving the clash must not quietly delete a
// feature: the action keeps a key, just not a bare digit.
//
// Negative control: drop snap_corner_N from the defaults entirely instead of
// moving it, and this fails.
func TestCornerSnapIsStillReachable(t *testing.T) {
	r := NewKeybindRegistry(DefaultConfig())
	for i, d := range []string{"1", "2", "3", "4"} {
		want := "snap_corner_" + d
		if got := r.GetLayoutPrefixAction(d); got != want {
			t.Errorf("the layout chord then %q runs %q, want %q", d, got, want)
		}
		if keys := r.GetKeys(want); len(keys) == 0 {
			t.Errorf("%s has no keys at all", want)
		}
		_ = i
	}
}

// TestCornerSnapLeftTheWindowScope, stated separately so a future default that
// puts it back on a bare digit fails here with the reason rather than only in
// the count above.
//
// Negative control: leave snap_corner_1 in the layout table and this fails.
func TestCornerSnapLeftTheWindowScope(t *testing.T) {
	cfg := DefaultConfig()
	for i := 1; i <= 4; i++ {
		action := "snap_corner_" + string(rune('0'+i))
		if keys, ok := cfg.Keybindings.Layout[action]; ok {
			t.Errorf("%s is back in [keybindings.layout] on %v, where it shadows select_window_%d",
				action, keys, i)
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

// TestConflictWarningNamesTheWinnerAndAVerb. A warning that lists two action
// names leaves the reader with a fact and nothing to do about it.
//
// Negative control: restore the old "is bound to multiple actions" message and
// this fails.
func TestConflictWarningNamesTheWinnerAndAVerb(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Keybindings.Layout["snap_corner_1"] = []string{"1"}

	var line string
	for _, l := range ConfigWarnings(cfg) {
		if strings.Contains(l, "snap_corner_1") {
			line = l
			break
		}
	}
	if line == "" {
		t.Fatal("no warning about the clash")
	}
	if !strings.Contains(line, "runs snap_corner_1") {
		t.Errorf("the warning does not say which action wins: %q", line)
	}
	if !strings.Contains(line, "keybinds unbind") {
		t.Errorf("the warning does not say how to act on it: %q", line)
	}
}

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
