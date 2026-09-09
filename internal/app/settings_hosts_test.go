package app

import (
	"os"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
)

// The Hosts section of the settings page.
//
// Each test drives the page the way a person does: focus a row, edit it, commit
// it, and read the file that came out. A test that only asked whether the row
// exists would pass on a section that is drawn and wired to nothing, which is
// the failure this repository has shipped before.

func hostsOS(t *testing.T, hosts map[string]config.HostConfig) *OS {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Hosts = hosts
	return NewOS(OSOptions{UserConfig: cfg})
}

func TestHostsSectionListsEachConfiguredHost(t *testing.T) {
	m := hostsOS(t, map[string]config.HostConfig{
		"build": {Addr: "gaurav@buildbox"},
		"lab":   {Addr: "lab-01"},
	})
	item := focusSetting(t, m, "Hosts", "build")
	if got := item.value(m); got != "gaurav@buildbox" {
		t.Errorf("ASSERTION: the host row does not show its address, got %q", got)
	}
	focusSetting(t, m, "Hosts", "lab")
	focusSetting(t, m, "Hosts", hostAddRowLabel)
	focusSetting(t, m, "Hosts", hostTestRowLabel)
}

// A host that is not answering keeps its row and says why. Hiding it would
// leave the user looking for a machine they know they configured.
func TestAnUnreachableHostKeepsItsRowAndItsReason(t *testing.T) {
	m := hostsOS(t, map[string]config.HostConfig{"offline": {Addr: "someone@poweredoff"}})
	m.applyFederationSnapshot(FederationHostsMsg{
		Configured: 1,
		Snapshot: FederationSnapshot{Hosts: []FederationHost{{
			Name:   "offline",
			Status: string(federation.StatusUnreachable),
			Reason: "The host did not answer.",
		}}},
	})
	item := focusSetting(t, m, "Hosts", "offline")
	if !strings.Contains(item.Desc, "does not answer") {
		t.Errorf("ASSERTION: the row does not say the host is unreachable, got %q", item.Desc)
	}
	if !strings.Contains(item.Desc, "The host did not answer.") {
		t.Errorf("ASSERTION: the row drops the reason the link gave, got %q", item.Desc)
	}
}

// The add row is the one that had to write something a config file can hold.
func TestTheAddRowWritesAHostToTheConfigFile(t *testing.T) {
	path := useTempConfig(t)
	m := hostsOS(t, nil)

	editSetting(t, m, "Hosts", hostAddRowLabel, "build gaurav@buildbox")

	if got := m.UserConfig.Hosts["build"].Addr; got != "gaurav@buildbox" {
		t.Fatalf("ASSERTION: the add row did not put the host in the config, got %q", got)
	}
	data, err := os.ReadFile(path) //nolint:gosec // the temp config this test made
	if err != nil {
		t.Fatalf("read the config file: %v", err)
	}
	if !strings.Contains(string(data), "gaurav@buildbox") {
		t.Fatalf("ASSERTION: the add row did not reach the config file:\n%s", data)
	}
	// The file the daemon will read has to hold the host as a host, not as a
	// string that happens to be in the file.
	cfg, err := config.ParseUserConfig(data)
	if err != nil {
		t.Fatalf("ASSERTION: the file the page wrote does not parse: %v", err)
	}
	if cfg.Hosts["build"].Addr != "gaurav@buildbox" {
		t.Errorf("ASSERTION: the daemon would not see the host, got %+v", cfg.Hosts)
	}
	// The row appears in the section, so the next thing the user sees is the
	// machine they just added.
	focusSetting(t, m, "Hosts", "build")
}

func TestTheAddRowRefusesAnIncompleteEntry(t *testing.T) {
	useTempConfig(t)
	m := hostsOS(t, nil)
	editSetting(t, m, "Hosts", hostAddRowLabel, "build")
	if len(m.UserConfig.Hosts) != 0 {
		t.Errorf("ASSERTION: a name with no address was added, got %+v", m.UserConfig.Hosts)
	}
	editSetting(t, m, "Hosts", hostAddRowLabel, "local somewhere")
	if _, ok := m.UserConfig.Hosts[federation.LocalHostName]; ok {
		t.Error("ASSERTION: the reserved name was added as a host")
	}
}

func TestEditingAHostRowMovesTheMachine(t *testing.T) {
	path := useTempConfig(t)
	m := hostsOS(t, map[string]config.HostConfig{"build": {Addr: "old-address"}})

	editSetting(t, m, "Hosts", "build", "new-address")

	if got := m.UserConfig.Hosts["build"].Addr; got != "new-address" {
		t.Fatalf("ASSERTION: editing the row did not change the address, got %q", got)
	}
	data, _ := os.ReadFile(path) //nolint:gosec // the temp config this test made
	if strings.Contains(string(data), "old-address") {
		t.Errorf("ASSERTION: the old address is still in the config file:\n%s", data)
	}
}

func TestClearingAHostRowRemovesTheHost(t *testing.T) {
	path := useTempConfig(t)
	m := hostsOS(t, map[string]config.HostConfig{
		"build": {Addr: "gaurav@buildbox"},
		"lab":   {Addr: "lab-01"},
	})

	editSetting(t, m, "Hosts", "build", "")

	if _, ok := m.UserConfig.Hosts["build"]; ok {
		t.Fatal("ASSERTION: clearing the row did not remove the host")
	}
	if _, ok := m.UserConfig.Hosts["lab"]; !ok {
		t.Error("ASSERTION: removing one host removed another")
	}
	data, _ := os.ReadFile(path) //nolint:gosec // the temp config this test made
	if strings.Contains(string(data), "buildbox") {
		t.Errorf("ASSERTION: the removed host is still in the config file:\n%s", data)
	}
	if !strings.Contains(string(data), "lab-01") {
		t.Errorf("ASSERTION: removing one host dropped another from the file:\n%s", data)
	}
}

// The rail stops polling once the daemon reports no hosts, which is the default
// install. Adding the first host has to start it again, or the status on this
// page would stay empty for ever.
func TestAddingTheFirstHostRestartsTheStatusPolling(t *testing.T) {
	useTempConfig(t)
	m := hostsOS(t, nil)
	m.applyFederationSnapshot(FederationHostsMsg{Configured: 0})
	if m.federationPolling {
		t.Fatal("the poll did not stop for a daemon with no hosts")
	}
	editSetting(t, m, "Hosts", hostAddRowLabel, "build gaurav@buildbox")
	if !m.federationPolling {
		t.Error("ASSERTION: adding the first host left the status polling off, so no row would ever show a state")
	}
}

// The test row's result lands on the rows, which is where a person reads it.
func TestALinkTestResultReachesTheRows(t *testing.T) {
	useTempConfig(t)
	m := hostsOS(t, map[string]config.HostConfig{"build": {Addr: "gaurav@buildbox"}})
	m.hostTestRunning = true
	m.applyHostTest(HostTestDoneMsg{Reports: []federation.HostReport{{
		Host:   "build",
		Addr:   "gaurav@buildbox",
		Status: federation.StatusUnreachable,
		Reason: "The host did not answer.",
		Detail: "someone@buildbox: Permission denied (publickey).",
	}}})
	if m.hostTestRunning {
		t.Error("ASSERTION: the test row stayed busy after its result came back")
	}
	item := focusSetting(t, m, "Hosts", "build")
	if !strings.Contains(item.Desc, "Permission denied") {
		t.Errorf("ASSERTION: the row does not show what ssh said, got %q", item.Desc)
	}
}

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
