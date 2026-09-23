package config_test

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// withClickToType restores the global after a test that moves it, since it is
// package state shared with every other test in the run.
func withClickToType(t *testing.T) {
	t.Helper()
	prev := config.Global.ClickToType
	t.Cleanup(func() { config.Global.ClickToType = prev })
}

// The default has been double since v0.8.0: one click focuses, and a second
// one starts typing. It was single before.
func TestClickToTypeDefaultsToDouble(t *testing.T) {
	if got := config.DefaultConfig().Appearance.ClickToType; got != config.ClickToTypeDouble {
		t.Errorf("default click_to_type = %q, want %q", got, config.ClickToTypeDouble)
	}
	if got := config.DefaultSettings().ClickToType; got != config.ClickToTypeDouble {
		t.Errorf("default settings click_to_type = %q, want %q", got, config.ClickToTypeDouble)
	}
}

// Each value reaches the global the mouse path reads, and a config written
// before the key existed loads as the default rather than as no policy at all.
func TestClickToTypeReachesTheGlobal(t *testing.T) {
	withClickToType(t)

	for _, mode := range config.ClickToTypeModes {
		cfg := config.DefaultConfig()
		cfg.Appearance.ClickToType = mode
		config.ApplyAppearanceConfig(cfg, &config.Global)
		if config.Global.ClickToType != mode {
			t.Errorf("ClickToType = %q after applying %q", config.Global.ClickToType, mode)
		}
	}

	// An older config: the key is absent, and the load path backfills it.
	cfg := writeConfig(t, "[appearance]\nborder_style = \"rounded\"\n")
	if got := cfg.Appearance.ClickToType; got != config.ClickToTypeDouble {
		t.Errorf("click_to_type = %q for a config written before the key existed, want %q", got, config.ClickToTypeDouble)
	}
}

// A typo warns and lands on the default, so a misspelled policy cannot leave
// the mouse doing nothing recognisable.
func TestClickToTypeRejectsAnUnknownValue(t *testing.T) {
	withClickToType(t)
	config.Global.ClickToType = config.ClickToTypeOff

	cfg := config.DefaultConfig()
	cfg.Appearance.ClickToType = "sometimes"

	var warned bool
	for _, w := range config.ValidateConfig(cfg).Warnings {
		warned = warned || w.Key == "click_to_type"
	}
	if !warned {
		t.Error("an unknown click_to_type value was accepted without a warning")
	}

	config.ApplyAppearanceConfig(cfg, &config.Global)
	if config.Global.ClickToType != config.ClickToTypeDouble {
		t.Errorf("ClickToType = %q after an unknown value, want the default %q", config.Global.ClickToType, config.ClickToTypeDouble)
	}
}
