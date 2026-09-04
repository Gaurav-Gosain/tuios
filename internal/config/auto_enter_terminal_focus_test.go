package config_test

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// withAutoEnterTerminalOnFocus restores the setting after a test that moves
// it, since config.Global is shared with every other test in the run.
func withAutoEnterTerminalOnFocus(t *testing.T) {
	t.Helper()
	prev := config.Global.AutoEnterTerminalOnFocus
	t.Cleanup(func() { config.Global.AutoEnterTerminalOnFocus = prev })
}

func TestAutoEnterTerminalOnFocusDefaultsToOff(t *testing.T) {
	if got := config.DefaultSettings().AutoEnterTerminalOnFocus; got != config.AutoEnterTerminalOff {
		t.Errorf("default AutoEnterTerminalOnFocus = %q, want %q", got, config.AutoEnterTerminalOff)
	}
	if got := config.DefaultConfig().Appearance.AutoEnterTerminalOnFocus; got != config.AutoEnterTerminalOff {
		t.Errorf("DefaultConfig auto_enter_terminal_on_focus = %q, want %q", got, config.AutoEnterTerminalOff)
	}
}

func TestAutoEnterTerminalOnFocusReachesTheSettings(t *testing.T) {
	withAutoEnterTerminalOnFocus(t)

	for _, mode := range config.AutoEnterTerminalModes {
		cfg := config.DefaultConfig()
		cfg.Appearance.AutoEnterTerminalOnFocus = config.AutoEnterTerminalPolicy(mode)
		config.ApplyAppearanceConfig(cfg, &config.Global)
		if got := config.Global.AutoEnterTerminalOnFocus; got != config.AutoEnterTerminalPolicy(mode) {
			t.Errorf("AutoEnterTerminalOnFocus = %q after applying %q", got, mode)
		}
	}

	// An older config: the key is absent, and the load path backfills the default.
	cfg := writeConfig(t, "[appearance]\nborder_style = \"rounded\"\n")
	if got := cfg.Appearance.AutoEnterTerminalOnFocus; got != config.AutoEnterTerminalOff {
		t.Errorf("auto_enter_terminal_on_focus = %q for a config written before the key existed, want %q", got, config.AutoEnterTerminalOff)
	}
}

func TestAutoEnterTerminalOnFocusRejectsAnUnknownValue(t *testing.T) {
	withAutoEnterTerminalOnFocus(t)
	config.Global.AutoEnterTerminalOnFocus = config.AutoEnterTerminalAll

	cfg := config.DefaultConfig()
	cfg.Appearance.AutoEnterTerminalOnFocus = "sometimes"

	var warned bool
	for _, w := range config.ValidateConfig(cfg).Warnings {
		warned = warned || w.Key == "auto_enter_terminal_on_focus"
	}
	if !warned {
		t.Error("an unknown auto_enter_terminal_on_focus value was accepted without a warning")
	}

	config.ApplyAppearanceConfig(cfg, &config.Global)
	if config.Global.AutoEnterTerminalOnFocus != config.AutoEnterTerminalOff {
		t.Errorf("AutoEnterTerminalOnFocus = %q after an unknown value, want the default %q", config.Global.AutoEnterTerminalOnFocus, config.AutoEnterTerminalOff)
	}
}

func TestAutoEnterTerminalOnFocusAcceptsALeftoverBool(t *testing.T) {
	withAutoEnterTerminalOnFocus(t)

	trueCfg := writeConfig(t, "[appearance]\nauto_enter_terminal_on_focus = true\n")
	if got := trueCfg.Appearance.AutoEnterTerminalOnFocus; got != config.AutoEnterTerminalAll {
		t.Errorf("true decoded as %q, want %q so a leftover bool does not discard the file", got, config.AutoEnterTerminalAll)
	}

	falseCfg := writeConfig(t, "[appearance]\nauto_enter_terminal_on_focus = false\n")
	if got := falseCfg.Appearance.AutoEnterTerminalOnFocus; got != config.AutoEnterTerminalOff {
		t.Errorf("false decoded as %q, want %q", got, config.AutoEnterTerminalOff)
	}
}
