package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// The Hosts section of the settings page.
//
// Each test drives the page the way a person does: focus a row, edit it, commit
// it, and read the file that came out. A test that only asked whether the row
// exists would pass on a section that is drawn and wired to nothing, which is
// the failure this repository has shipped before.

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
