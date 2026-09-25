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
