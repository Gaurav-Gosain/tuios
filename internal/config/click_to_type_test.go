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
