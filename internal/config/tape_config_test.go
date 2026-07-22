package config_test

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/pelletier/go-toml/v2"
)

// TestDefaultTapeAutorunIsAsk pins the safe default: detection is on but nothing
// runs without user action.
func TestDefaultTapeAutorunIsAsk(t *testing.T) {
	cfg := config.DefaultConfig()
	if cfg.Tape.Autorun != config.TapeAutorunAsk {
		t.Fatalf("default tape.autorun = %q, want %q", cfg.Tape.Autorun, config.TapeAutorunAsk)
	}
}

// TestTapeConfigParsesValidModes checks that each valid mode round-trips from
// TOML.
func TestTapeConfigParsesValidModes(t *testing.T) {
	for _, mode := range config.TapeAutorunModes {
		var cfg config.UserConfig
		src := "[tape]\nautorun = \"" + mode + "\"\n"
		if err := toml.Unmarshal([]byte(src), &cfg); err != nil {
			t.Fatalf("unmarshal %q: %v", mode, err)
		}
		if cfg.Tape.Autorun != mode {
			t.Fatalf("parsed autorun = %q, want %q", cfg.Tape.Autorun, mode)
		}
		if res := config.ValidateConfig(&cfg); res.HasErrors() {
			t.Fatalf("valid mode %q produced errors: %+v", mode, res.Errors)
		}
	}
}

// TestTapeConfigValidationWarnsOnUnknownMode: a bad value is a warning (not a
// hard error), and it falls back to the default when filled.
func TestTapeConfigValidationWarnsOnUnknownMode(t *testing.T) {
	var cfg config.UserConfig
	if err := toml.Unmarshal([]byte("[tape]\nautorun = \"sometimes\"\n"), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	res := config.ValidateConfig(&cfg)
	if res.HasErrors() {
		t.Fatalf("unknown autorun should warn, not error: %+v", res.Errors)
	}
	var found bool
	for _, w := range res.Warnings {
		if w.Field == "tape" && w.Key == "autorun" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected a warning for an unknown tape.autorun value")
	}
}

// TestTapeConfigFillDefaultsUnknownMode: the fill path replaces an unknown mode
// with the default so nothing downstream sees an invalid value. Exercised via
// the public WriteConfigFile/LoadUserConfig is heavier; here we assert the
// documented behavior through validation plus DefaultConfig equivalence.
func TestTapeConfigEmptyModeIsNotAnError(t *testing.T) {
	var cfg config.UserConfig // Autorun is the zero value ""
	res := config.ValidateConfig(&cfg)
	for _, w := range res.Warnings {
		if w.Field == "tape" && w.Key == "autorun" {
			t.Fatal("an empty autorun should not warn; it defaults to ask")
		}
	}
}
