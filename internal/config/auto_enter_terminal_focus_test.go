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
