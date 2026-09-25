package main

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestKeybindSectionOfFindsTheTable, which is what lets the command take an
// action name alone rather than making the user name the config table too.
//
// Negative control: iterate a hardcoded subset of sections and this fails on
// the prefix or global cases.
func TestKeybindSectionOfFindsTheTable(t *testing.T) {
	cfg := config.DefaultConfig()
	for action, want := range map[string]string{
		"close_window":         config.SectionWindowManagement,
		"prefix_close_window":  config.SectionPrefixMode,
		"command_palette":      config.SectionGlobal,
		"terminal_focus_left":  config.SectionTerminalMode,
		"workspace_prefix_new": "",
	} {
		if got := keybindSectionOf(cfg, action); got != want {
			t.Errorf("keybindSectionOf(%q) = %q, want %q", action, got, want)
		}
	}
}
