package config_test

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestAnExistingConfigKeepsTheStartupItWasWritten is the upgrade contract for
// tiled and daemon, and it is the reason both could be turned on at all.
//
// The [startup] booleans have no fill-missing pass, so a file that does not
// name them reads them as false. That is not an oversight here: it means the
// two defaults that change what happens the moment tuios starts reach a new
// install only. Somebody who already has a config file goes on getting the
// floating, standalone session they had, and the daemon never appears on a
// machine that was not asked for one.
//
// If a fillMissingStartup is ever added, this test fails, and the note in
// docs/SESSIONS.md stops being true on the same day.
func TestAnExistingConfigKeepsTheStartupItWasWritten(t *testing.T) {
	const src = `
[appearance]
border_style = "rounded"
`
	cfg, err := config.ParseUserConfig([]byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Startup.OpenDefaultWindow || cfg.Startup.Tiled || cfg.Startup.StartInTerminalMode || cfg.Startup.Daemon {
		t.Errorf("an existing config without [startup] must keep every option off, got open=%v tiled=%v terminal=%v daemon=%v",
			cfg.Startup.OpenDefaultWindow, cfg.Startup.Tiled, cfg.Startup.StartInTerminalMode, cfg.Startup.Daemon)
	}

	// The appearance half is the opposite, and deliberately so. Those are
	// strings, so an absent key is told apart from a set one, and the new
	// window buttons do reach a config file that never named them.
	if got := cfg.Appearance.WindowButtonStyle; got != config.WindowButtonStyleDots {
		t.Errorf("window_button_style = %q, want %q from the defaults", got, config.WindowButtonStyleDots)
	}
	if got := cfg.Appearance.WindowButtonPosition; got != config.WindowButtonPositionLeft {
		t.Errorf("window_button_position = %q, want %q from the defaults", got, config.WindowButtonPositionLeft)
	}
}
