package main

import (
	"testing"

	"github.com/adrg/xdg"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestLoadAndApplyConfigHonorsConfirmQuit proves the one shared bootstrap applies
// the ConfirmQuit override, so every run path (standalone, daemon, ssh) honors it
// identically. The daemon path used to omit ConfirmQuit from its Overrides; this
// pins that it no longer can, since all paths now flow through this function.
func TestLoadAndApplyConfigHonorsConfirmQuit(t *testing.T) {
	// Give the bootstrap a config directory of its own, so it reads defaults
	// rather than anything an earlier test in this binary saved. Registered
	// before the Setenvs so it runs after them: cleanups are LIFO, and a reload
	// that ran first would leave the xdg globals on these temp dirs for every
	// test after this one, moments before they are deleted.
	t.Cleanup(xdg.Reload)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	xdg.Reload()

	prevFlag, prevApplied := confirmQuit, config.AlwaysConfirmQuit
	t.Cleanup(func() {
		confirmQuit = prevFlag
		config.AlwaysConfirmQuit = prevApplied
	})

	confirmQuit = true
	config.AlwaysConfirmQuit = false

	loadAndApplyConfig()

	if !config.AlwaysConfirmQuit {
		t.Fatal("loadAndApplyConfig ignored the ConfirmQuit override")
	}
}
