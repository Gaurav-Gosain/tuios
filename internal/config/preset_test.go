package config

import (
	"slices"
	"strings"
	"testing"
)

// The ways a preset can go wrong, and the test that holds each:
//
//   - Two actions share a key in one scope, so the key runs whichever comes
//     first by name: TestEveryPresetLoadsCleanly, on a Linux and a macOS host.
//   - A preset binds alt+[ or alt+], which a terminal sends as the start of an
//     escape sequence: the same test.
//   - A preset names an action nothing runs: TestEveryPresetActionIsRegistered
//     in internal/input.
//   - The preset's key takes a key the user bound, or a user's own binding of
//     an action is lost: TestTheUserWinsOverThePreset.
//   - A tuios feature vanishes because the preset took its key and put it
//     nowhere: TestEveryDisplacedFeatureMoves.

// presetConfig parses a config that names preset and adds body.
func presetConfig(t *testing.T, preset, body string) *UserConfig {
	t.Helper()
	cfg, err := ParseUserConfig([]byte("[keybindings]\npreset = \"" + preset + "\"\n" + body))
	if err != nil {
		t.Fatalf("preset %s: %v", preset, err)
	}
	return cfg
}

// TestEveryPresetLoadsCleanly loads each preset on a Linux and a macOS host
// and requires no clash in any scope and no key that is ambiguous on the
// wire. The tuios row is the positive half: the defaults pass the same check.
//
// Negative control: binding prefix_new_window to "c" in the tmux preset (the
// key new_workspace has) fails it with a collision.
func TestEveryPresetLoadsCleanly(t *testing.T) {
	for _, mac := range []bool{false, true} {
		restore := ForceMacOSHost(mac)
		for _, name := range Presets() {
			cfg := presetConfig(t, name, "")
			r := NewKeybindRegistry(cfg)
			for _, c := range r.Collisions() {
				t.Errorf("preset %s (mac %v): %s runs %s and hides %v", name, mac, c.Press, c.Winner, c.Losers)
			}
			for _, b := range r.Bindings() {
				// alt+[ and alt+] reach tuios as ESC [ and ESC ], the starts
				// of a CSI and an OSC, without the Kitty protocol.
				if b.Key == "alt+[" || b.Key == "alt+]" {
					t.Errorf("preset %s (mac %v): %s [%s] is on %s, which a terminal sends as the start of an escape sequence",
						name, mac, b.Action, b.Section, b.Key)
				}
			}
		}
		restore()
	}
}

// TestTheUserWinsOverThePreset: under the tmux preset, an action the user
// binds keeps the user's keys, and a key the user gives to another action
// keeps that action. The preset's own action for the key goes without it.
//
// Negative control: filling prefix_mode with fillMapDefaults (no yield) under
// a preset gives p to both actions, and the registry runs prefix_prev_window
// for p only by name order.
func TestTheUserWinsOverThePreset(t *testing.T) {
	cfg := presetConfig(t, "tmux", "[keybindings.prefix_mode]\nprefix_prev_window = [\"p\"]\nnew_workspace = [\"N\"]\n")
	r := NewKeybindRegistry(cfg)
	if got := r.GetPrefixAction("p"); got != "prefix_prev_window" {
		t.Errorf("p runs %q, want the user's prefix_prev_window", got)
	}
	if keys := cfg.Keybindings.PrefixMode["prev_workspace"]; slices.Contains(keys, "p") {
		t.Errorf("prev_workspace still holds p: %v", keys)
	}
	if got := r.GetPrefixAction("N"); got != "new_workspace" {
		t.Errorf("N runs %q, want the user's new_workspace", got)
	}
	if got := r.GetPrefixAction("c"); got == "new_workspace" {
		t.Error("new_workspace kept the preset's c beside the user's N")
	}
	if got := r.GetPrefixAction("n"); got != "next_workspace" {
		t.Errorf("n runs %q, want the preset's next_workspace", got)
	}
	if cfg.Keybindings.LeaderKey != "ctrl+b" {
		t.Errorf("leader is %q, want the preset's ctrl+b", cfg.Keybindings.LeaderKey)
	}
}

// TestEveryDisplacedFeatureMoves: a tuios action a preset takes the key of is
// still reachable, in the tuios menu or on another key after the leader.
//
// Negative control: dropping prefix_minimize from the tmux preset's
// [tuios_prefix] fails it for prefix_minimize.
func TestEveryDisplacedFeatureMoves(t *testing.T) {
	tuios := NewKeybindRegistry(DefaultConfig())
	for _, name := range Presets()[1:] {
		cfg := presetConfig(t, name, "")
		for action, keys := range DefaultConfig().Keybindings.PrefixMode {
			if len(keys) == 0 || tuios.GetPrefixAction(keys[0]) != action {
				continue
			}
			reachable := len(cfg.Keybindings.PrefixMode[action]) > 0 || len(cfg.Keybindings.TuiosPrefix[action]) > 0
			if !reachable {
				t.Errorf("preset %s: %s (tuios key %s) has no key after the leader and none in the tuios menu", name, action, strings.Join(keys, ", "))
			}
		}
	}
}

// TestAnUnknownPresetKeepsTheTuiosKeys: a misspelt preset is reported, and the
// keys stay the tuios ones rather than half of a preset.
func TestAnUnknownPresetKeepsTheTuiosKeys(t *testing.T) {
	cfg := presetConfig(t, "tmx", "")
	if got := NewKeybindRegistry(cfg).GetPrefixAction("c"); got != "prefix_new_window" {
		t.Errorf("c runs %q under an unknown preset, want the tuios prefix_new_window", got)
	}
	warned := false
	for _, w := range ValidateConfig(cfg).Warnings {
		warned = warned || w.Key == "preset"
	}
	if !warned {
		t.Error("an unknown preset raised no warning")
	}
}
