package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestRemoteDockComponentWarningNamesTheHost is the documented answer to "where
// does a component run": a component is UI, so it runs where the client runs,
// which for a browser tab or an SSH session is not the machine the user is
// looking at. The warning is how that stops being something people discover.
func TestRemoteDockComponentWarningNamesTheHost(t *testing.T) {
	left := []string{"mode", "custom/battery"}
	cfg := &config.UserConfig{Dock: config.DockConfig{
		Left:   &left,
		Custom: map[string]config.DockCustomConfig{"battery": {Command: "cat /sys/class/power_supply/BAT0/capacity"}},
	}}

	got := remoteDockComponentWarning(cfg, "tuios-web")
	if len(got) != 1 || !strings.Contains(got[0], "tuios-web") {
		t.Fatalf("a browser session was not told where its components run: %v", got)
	}
	if !strings.Contains(got[0], "machine hosting this session") {
		t.Errorf("the warning does not say which machine: %q", got[0])
	}

	// A config with no components of its own is told nothing, the way a user who
	// never turned an unavailable alert sink on is told nothing about it.
	plain := &config.UserConfig{}
	if got := remoteDockComponentWarning(plain, "tuios-web"); len(got) != 0 {
		t.Errorf("a session with no custom components was warned anyway: %v", got)
	}

	// Defined but never placed is also nothing to warn about here: the
	// "you defined it and did not use it" warning is the config validator's.
	unplaced := &config.UserConfig{Dock: config.DockConfig{
		Custom: map[string]config.DockCustomConfig{"battery": {Command: "true"}},
	}}
	if got := remoteDockComponentWarning(unplaced, "over SSH"); len(got) != 0 {
		t.Errorf("an unplaced component produced a host warning: %v", got)
	}
}
