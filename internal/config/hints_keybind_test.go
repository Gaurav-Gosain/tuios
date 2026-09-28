package config

import (
	"slices"
	"testing"
)

// A new default must not take a key the user already gave to something else.
// Filled in as usual, hints would claim F, and the duplicate cleanup hands a
// contested key to the action that owns it by default, so the user's own
// binding would lose it on the next load.
//
// The user's key is compared as a key press, not as text: shift+f and shift+F
// are the press F is, and the registry matches them to it.
func TestHintsDefaultYieldsToAUserBinding(t *testing.T) {
	for _, spelling := range []string{"F", "shift+f", "shift+F"} {
		t.Run(spelling, func(t *testing.T) {
			cfg, err := ParseUserConfig([]byte("[keybindings.prefix_mode]\nprefix_fullscreen = [\"" + spelling + "\"]\n"))
			if err != nil {
				t.Fatal(err)
			}
			pm := cfg.Keybindings.PrefixMode
			if got := pm["prefix_fullscreen"]; !slices.Equal(got, []string{spelling}) {
				t.Errorf("the user's binding changed: prefix_fullscreen = %q, want [%s]", got, spelling)
			}
			if keys, ok := pm["hints"]; ok {
				t.Errorf("hints was filled in with %q over the user's %s", keys, spelling)
			}
			if got := NewKeybindRegistry(cfg).GetPrefixAction("F"); got != "prefix_fullscreen" {
				t.Errorf("leader F runs %q, want prefix_fullscreen", got)
			}
			want := []YieldedDefault{{Section: "prefix_mode", Action: "hints", Key: "F", TakenBy: "prefix_fullscreen"}}
			if !slices.Equal(cfg.YieldedDefaults, want) {
				t.Errorf("YieldedDefaults = %+v, want %+v", cfg.YieldedDefaults, want)
			}
			if rep := NewKeybindRegistry(cfg).Report(PaneFacts{}); !slices.Equal(rep.Yielded, want) {
				t.Errorf("the doctor report does not name the action left without a key: %+v", rep.Yielded)
			}
		})
	}
}

// f without Shift is a different press, so it does not stop hints taking F.
func TestHintsDefaultIgnoresADifferentPress(t *testing.T) {
	cfg, err := ParseUserConfig([]byte("[keybindings.prefix_mode]\nprefix_fullscreen = [\"f\"]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Keybindings.PrefixMode["hints"]; !slices.Equal(got, []string{"F"}) {
		t.Errorf("hints = %q, want [F]", got)
	}
}

// An alphabet with digits or symbols in it works, minus those characters, and
// says so. A silent drop would read as labels that never use the keys the
// person chose.
func TestHintsAlphabetWarnsAboutWhatItDrops(t *testing.T) {
	for alphabet, wantWarn := range map[string]bool{
		"asdf":      false,
		"ASDF":      false,
		"asdf;1":    true,
		"asdfghjk,": true,
	} {
		cfg := DefaultConfig()
		cfg.Hints.Alphabet = alphabet
		warned := false
		for _, w := range ValidateConfig(cfg).Warnings {
			if w.Field == "hints" && w.Key == "alphabet" {
				warned = true
			}
		}
		if warned != wantWarn {
			t.Errorf("alphabet %q: warned = %v, want %v", alphabet, warned, wantWarn)
		}
	}
}

// The positive half: with F free, hints gets it, and nothing is reported.
func TestHintsDefaultFillsAFreeKey(t *testing.T) {
	cfg, err := ParseUserConfig([]byte("[keybindings.prefix_mode]\nprefix_fullscreen = [\"z\"]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Keybindings.PrefixMode["hints"]; !slices.Equal(got, []string{"F"}) {
		t.Errorf("hints = %q, want [F]", got)
	}
	if got := NewKeybindRegistry(cfg).GetPrefixAction("F"); got != "hints" {
		t.Errorf("leader F runs %q, want hints", got)
	}
	if len(cfg.YieldedDefaults) != 0 {
		t.Errorf("reported %+v with the key free", cfg.YieldedDefaults)
	}
}
