package config_test

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// withZenMode restores the global after a test that moves it, since it is
// package state shared with every other test in the run.
func withZenMode(t *testing.T) {
	t.Helper()
	prev := config.Global.ZenMode
	t.Cleanup(func() { config.Global.ZenMode = prev })
}

// The default keeps today's behaviour: borders are always visible, and zen mode
// is opt-in.
func TestZenModeDefaultsToDisabled(t *testing.T) {
	if got := config.DefaultConfig().Appearance.ZenMode; got != config.ZenModeDisabled {
		t.Errorf("default zen_mode = %q, want %q", got, config.ZenModeDisabled)
	}
}

// Each value reaches the global the render path reads, and a config written
// before the key existed loads as the default rather than as no policy at all.
func TestZenModeReachesTheGlobal(t *testing.T) {
	withZenMode(t)

	for _, mode := range config.ZenModeModes {
		cfg := config.DefaultConfig()
		cfg.Appearance.ZenMode = mode
		config.ApplyAppearanceConfig(cfg, &config.Global)
		if config.Global.ZenMode != mode {
			t.Errorf("ZenMode = %q after applying %q", config.Global.ZenMode, mode)
		}
	}

	// An older config: the key is absent, and the load path backfills it.
	cfg := writeConfig(t, "[appearance]\nborder_style = \"rounded\"\n")
	if got := cfg.Appearance.ZenMode; got != config.ZenModeDisabled {
		t.Errorf("zen_mode = %q for a config written before the key existed, want %q", got, config.ZenModeDisabled)
	}
}

// A typo warns and lands on the default, so a misspelled policy cannot leave
// the renderer doing something unrecognisable.
func TestZenModeRejectsAnUnknownValue(t *testing.T) {
	withZenMode(t)
	config.Global.ZenMode = config.ZenModeAlways

	cfg := config.DefaultConfig()
	cfg.Appearance.ZenMode = "sometimes"

	var warned bool
	for _, w := range config.ValidateConfig(cfg).Warnings {
		warned = warned || w.Key == "zen_mode"
	}
	if !warned {
		t.Error("an unknown zen_mode value was accepted without a warning")
	}

	config.ApplyAppearanceConfig(cfg, &config.Global)
	if config.Global.ZenMode != config.ZenModeDisabled {
		t.Errorf("ZenMode = %q after an unknown value, want the default %q", config.Global.ZenMode, config.ZenModeDisabled)
	}
}
