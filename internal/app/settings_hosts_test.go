package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// A served session does not own the config file, and it must not run ssh from
// the serving machine on behalf of whoever connected.
func TestAReadOnlySessionDoesNotTestTheLinks(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Hosts = map[string]config.HostConfig{"build": {Addr: "gaurav@buildbox"}}
	m := NewOS(OSOptions{UserConfig: cfg, ConfigReadOnly: true})
	focusSetting(t, m, "Hosts", hostTestRowLabel)
	if cmd := m.SettingsActivate(); cmd != nil {
		t.Error("ASSERTION: a read-only session started a link test")
	}
	if m.hostTestRunning {
		t.Error("ASSERTION: a read-only session marked a link test as running")
	}
	if got := m.hostAddrCandidates(); len(got) != 0 {
		t.Errorf("ASSERTION: a read-only session read the serving machine's ssh config, got %v", got)
	}
}
