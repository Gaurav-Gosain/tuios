package main

import (
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestDaemonConfigFromCarriesTheDaemonSection pins the [daemon] settings onto
// the daemon tuios-web autostarts. They used to be dropped, so whether agent
// detection ran came down to whether `tuios attach` or `tuios-web` won the race
// to start the daemon.
func TestDaemonConfigFromCarriesTheDaemonSection(t *testing.T) {
	off := false
	cfg := config.DefaultConfig()
	cfg.Daemon.AgentAutoDetect = &off
	cfg.Daemon.AgentDetectSeconds = 7
	cfg.Daemon.AgentBinaries = []string{"my-agent"}

	got := daemonConfigFrom(cfg)

	if got.AgentAutoDetect == nil || *got.AgentAutoDetect {
		t.Error("agent_autodetect did not reach the daemon")
	}
	if got.AgentDetectInterval != 7*time.Second {
		t.Errorf("agent_detect_seconds reached the daemon as %v, want 7s", got.AgentDetectInterval)
	}
	if len(got.AgentBinaries) != 1 || got.AgentBinaries[0] != "my-agent" {
		t.Errorf("agent_binaries reached the daemon as %v", got.AgentBinaries)
	}
}

// TestDaemonConfigFromLeavesUnsetFieldsZero checks an untouched config asks the
// daemon for nothing, so the daemon's own defaults and its TUIOS_* environment
// overrides still apply.
func TestDaemonConfigFromLeavesUnsetFieldsZero(t *testing.T) {
	got := daemonConfigFrom(config.DefaultConfig())

	if got.AgentDetectInterval != 0 {
		t.Errorf("an unset agent_detect_seconds became %v, want 0", got.AgentDetectInterval)
	}
	if got.Version != "" {
		t.Errorf("daemonConfigFrom set Version to %q; that is the starter's to fill", got.Version)
	}
}
