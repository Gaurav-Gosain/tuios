package config_test

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

func withAutoEnterTerminalOnFocus(t *testing.T) {
	t.Helper()
	prev := config.AutoEnterTerminalOnFocus
	t.Cleanup(func() { config.AutoEnterTerminalOnFocus = prev })
}

func TestAutoEnterTerminalOnFocusDefaultsToTrue(t *testing.T) {
	if !config.AutoEnterTerminalOnFocus {
		t.Error("package default AutoEnterTerminalOnFocus is false, want true")
	}
	if cfg := config.DefaultConfig(); cfg.Appearance.AutoEnterTerminalOnFocus != nil {
		t.Errorf("DefaultConfig set AutoEnterTerminalOnFocus = %v, want nil so an absent key keeps the default", *cfg.Appearance.AutoEnterTerminalOnFocus)
	}
}

func TestAutoEnterTerminalOnFocusReachesTheGlobal(t *testing.T) {
	withAutoEnterTerminalOnFocus(t)

	cfg := config.DefaultConfig()
	off := false
	cfg.Appearance.AutoEnterTerminalOnFocus = &off
	config.ApplyAppearanceConfig(cfg)
	if config.AutoEnterTerminalOnFocus {
		t.Error("AutoEnterTerminalOnFocus stayed true after applying false")
	}

	on := true
	cfg.Appearance.AutoEnterTerminalOnFocus = &on
	config.ApplyAppearanceConfig(cfg)
	if !config.AutoEnterTerminalOnFocus {
		t.Error("AutoEnterTerminalOnFocus stayed false after applying true")
	}

	// An older config: the key is absent, and the load path leaves the default.
	loaded := writeConfig(t, "[appearance]\nborder_style = \"rounded\"\n")
	if loaded.Appearance.AutoEnterTerminalOnFocus != nil {
		t.Errorf("absent auto_enter_terminal_on_focus decoded as %v, want nil", *loaded.Appearance.AutoEnterTerminalOnFocus)
	}
}
