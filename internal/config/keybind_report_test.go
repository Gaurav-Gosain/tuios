package config

import (
	"testing"
)

// registryFor builds a registry over a config with only the sections a case
// needs, so a test states its own world rather than inheriting every default.
func registryFor(t *testing.T, mutate func(*UserConfig)) *KeybindRegistry {
	t.Helper()
	cfg := DefaultConfig()
	if mutate != nil {
		mutate(cfg)
	}
	return NewKeybindRegistry(cfg)
}

// The strongest check available: for every key the report calls contested, the
// action it names as the live one must be the action the registry's own lookup
// returns. Anything else means the overlay is describing a different program
// than the one running.
func TestReportAgreesWithTheRegistryOnEveryContestedKey(t *testing.T) {
	r := registryFor(t, nil)
	for _, c := range r.Collisions() {
		if c.Scope != ScopeWindowMode {
			continue // only the window-mode scope is reachable through GetAction
		}
		if got := r.GetAction(c.Key); got != c.Winner {
			t.Errorf("key %q: registry runs %q, report names %q as the winner", c.Key, got, c.Winner)
		}
	}
}

// A digit claimed by both select_window_N and snap_corner_N is reported as a
// cross-section collision, with the layout claim winning.
//
// This was the shipped default until corner snapping moved to the layout
// prefix, and this case used to assert it against DefaultConfig() on the
// grounds that surfacing such a finding is what the overlay is for. That was
// the wrong fixture: it made the test pass precisely because the defaults were
// broken, so the defaults could not be fixed without it failing, and it is the
// reason four dead bindings survived as long as they did. The property is real
// and worth keeping, so it keeps the arrangement and drops the claim that the
// arrangement is what tuios ships. TestStockConfigOpensNoConflicts in e2e/tui now
// owns the question of what the defaults may contain.
func TestCrossSectionDigitClashIsReported(t *testing.T) {
	r := registryFor(t, func(c *UserConfig) {
		c.Keybindings.WindowManagement["select_window_1"] = []string{"1"}
		c.Keybindings.Layout["snap_corner_1"] = []string{"1"}
	})
	found := false
	for _, c := range r.Collisions() {
		if c.Scope != ScopeWindowMode || c.Key != "1" {
			continue
		}
		found = true
		if c.Winner != "snap_corner_1" {
			t.Errorf("winner = %q, want snap_corner_1: layout is copied over window_management", c.Winner)
		}
		if !c.CrossSection {
			t.Error("the two claims are in different tables, so this is a cross-section collision")
		}
		var sawLoser bool
		for _, l := range c.Losers {
			if l.Action == "select_window_1" {
				sawLoser = true
			}
		}
		if !sawLoser {
			t.Errorf("select_window_1 lost the key and must be listed: %v", c.Losers)
		}
	}
	if !found {
		t.Fatal("a key claimed by two actions in one scope must be reported")
	}
}

// The direction of the cross-section rule, stated on its own so a change to
// buildMappings' section order cannot pass quietly.
func TestLaterSectionWinsAcrossSections(t *testing.T) {
	r := registryFor(t, func(c *UserConfig) {
		// window_management is visited before system.
		c.Keybindings.WindowManagement["zzz_early_section"] = []string{"ctrl+alt+k"}
		c.Keybindings.System["aaa_late_section"] = []string{"ctrl+alt+k"}
	})
	if got := r.GetAction("ctrl+alt+k"); got != "aaa_late_section" {
		t.Fatalf("registry runs %q; this test's premise about section order is stale", got)
	}
	for _, c := range r.Collisions() {
		if c.Key != "ctrl+alt+k" {
			continue
		}
		if c.Winner != "aaa_late_section" {
			t.Errorf("winner = %q, want the later section's action even though its name sorts first", c.Winner)
		}
		return
	}
	t.Fatal("no collision reported for a key claimed by two sections")
}

// A key bound twice in one scope is the collision the whole overlay exists to
// show, and the winner must be the one the registry's own lookup picks: first
// in action-name order.
func TestCollisionNamesTheActionThatActuallyRuns(t *testing.T) {
	r := registryFor(t, func(c *UserConfig) {
		c.Keybindings.WindowManagement["zzz_last_action"] = []string{"ctrl+alt+q"}
		c.Keybindings.WindowManagement["aaa_first_action"] = []string{"ctrl+alt+q"}
	})

	var got *Collision
	for i, c := range r.Collisions() {
		if c.Key == "ctrl+alt+q" {
			got = &r.Collisions()[i]
			break
		}
	}
	if got == nil {
		t.Fatal("a key bound to two actions in one section produced no collision")
	}
	if got.Winner != "aaa_first_action" {
		t.Errorf("winner = %q, want aaa_first_action (first in action-name order)", got.Winner)
	}
	if len(got.Losers) != 1 || got.Losers[0].Action != "zzz_last_action" {
		t.Errorf("losers = %v, want just zzz_last_action", got.Losers)
	}
	if got.CrossSection {
		t.Error("both actions were in window_management, so this is not a cross-section collision")
	}
	// The registry itself must agree, or the report is describing a different
	// program than the one running.
	if action := r.GetAction("ctrl+alt+q"); action != got.Winner {
		t.Errorf("registry resolves the key to %q but the report names %q", action, got.Winner)
	}
}

// The same key in two different scopes is not a conflict: they are never looked
// up together. Reporting it would bury the real ones.
func TestSameKeyInDifferentScopesIsNotACollision(t *testing.T) {
	r := registryFor(t, func(c *UserConfig) {
		c.Keybindings.WindowManagement["custom_wm"] = []string{"ctrl+alt+y"}
		c.Keybindings.PrefixMode["custom_prefix"] = []string{"ctrl+alt+y"}
	})
	for _, c := range r.Collisions() {
		if c.Key == "ctrl+alt+y" {
			t.Fatalf("window mode and prefix mode are separate scopes; %s should not collide", c.Press)
		}
	}
}

// A window-mode binding on a plain letter must not be reported as stolen from
// the shell: terminal mode never consults that section for it.
func TestPlainLetterWindowModeBindIsNotSwallowed(t *testing.T) {
	r := registryFor(t, nil)
	for _, s := range r.TerminalModeSwallowed() {
		if s.Key == "n" || s.Key == "x" {
			t.Errorf("%q is a window-mode key; terminal mode forwards it to the shell", s.Key)
		}
	}
}

// Workspace switching does reach terminal mode, but only on a reserved chord.
func TestWorkspaceSwitchIsSwallowedOnlyOnAReservedChord(t *testing.T) {
	r := registryFor(t, func(c *UserConfig) {
		c.Keybindings.Workspaces["switch_workspace_1"] = []string{"alt+1", "F1"}
	})
	var sawChord, sawBare bool
	for _, s := range r.TerminalModeSwallowed() {
		switch s.Key {
		case "alt+1":
			sawChord = true
		case "F1":
			sawBare = true
		}
	}
	if !sawChord {
		t.Error("alt+1 has a real Alt modifier, so terminal mode takes it")
	}
	if sawBare {
		t.Error("F1 has no Alt or Ctrl, so isReservedTerminalChord rejects it and the shell gets it")
	}
}

// TestTerminalKeysAreReportedFromTheirSection pins that every key terminal mode
// takes from the pane is named with the config table it came from. These were
// literals in the input path and the report had to hand-list them as
// "built-in", which is the report's way of saying the user cannot change it.
// alt+space is in the list because the hand-list never had it at all: the
// launcher was taking a key from the pane and nothing said so.
func TestTerminalKeysAreReportedFromTheirSection(t *testing.T) {
	r := registryFor(t, nil)
	want := map[string]string{
		"ctrl+p":       "global",
		"alt+space":    "global",
		"shift+up":     "terminal_mode",
		"shift+down":   "terminal_mode",
		"ctrl+shift+v": "terminal_mode",
	}
	seen := map[string]bool{}
	for _, s := range r.TerminalModeSwallowed() {
		origin, ok := want[s.Key]
		if !ok {
			continue
		}
		seen[s.Key] = true
		if s.Origin != origin {
			t.Errorf("%s should be reported from %q, got %q", s.Key, origin, s.Origin)
		}
	}
	for key := range want {
		if !seen[key] {
			t.Errorf("%s is taken by the input path but the report does not say so", key)
		}
	}
}

// A clash is only worth reporting for a key tuios actually withholds. vim binds
// ctrl+r, but terminal mode forwards it, so there is nothing to warn about.
func TestForwardedKeysProduceNoGuestClash(t *testing.T) {
	r := registryFor(t, nil)
	swallowed := map[string]bool{}
	for _, s := range r.TerminalModeSwallowed() {
		swallowed[lookupForm(s.Key)] = true
	}
	for _, c := range r.GuestClashes("") {
		if !swallowed[lookupForm(c.Key)] {
			t.Errorf("%s is forwarded to the pane, so %s still gets it; no clash to report", c.Key, c.Program)
		}
	}
}
