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
	// Isolate config lookup so the bootstrap reads defaults, not the developer's
	// real config, and any default write lands in the temp dir.
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
