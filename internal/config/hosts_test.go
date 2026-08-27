package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

// TestHostsTableParses covers the shape the design document specifies in
// section 3, field for field, so a config file written from the documentation
// loads.
func TestHostsTableParses(t *testing.T) {
	const src = `
[hosts.build]
addr = "gaurav@buildbox"

[hosts.work]
addr = "workstation.local"
connect_timeout = 5
command = "/home/gaurav/.local/bin/tuios"
ssh_options = ["-J", "bastion"]
`
	var cfg UserConfig
	if err := toml.Unmarshal([]byte(src), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(cfg.Hosts) != 2 {
		t.Fatalf("got %d hosts, want 2", len(cfg.Hosts))
	}
	if got := cfg.Hosts["build"].Addr; got != "gaurav@buildbox" {
		t.Errorf("build addr is %q, want gaurav@buildbox", got)
	}
	work := cfg.Hosts["work"]
	if work.ConnectTimeout != 5 {
		t.Errorf("work connect_timeout is %d, want 5", work.ConnectTimeout)
	}
	if work.Command != "/home/gaurav/.local/bin/tuios" {
		t.Errorf("work command is %q", work.Command)
	}
	if len(work.SSHOptions) != 2 || work.SSHOptions[0] != "-J" {
		t.Errorf("work ssh_options is %v, want [-J bastion]", work.SSHOptions)
	}
}

// TestSavingConfigKeepsHosts is the guard that matters more than the parse. The
// settings panel rewrites the whole file from the struct, so a host table the
// struct did not carry would be deleted the first time a user changed a colour.
func TestSavingConfigKeepsHosts(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Hosts = map[string]HostConfig{
		"build": {Addr: "gaurav@buildbox", ConnectTimeout: 7},
	}

	path := filepath.Join(t.TempDir(), "config.toml")
	if err := WriteConfigFile(cfg, path); err != nil {
		t.Fatalf("write: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(data), "gaurav@buildbox") {
		t.Fatalf("the saved file dropped the host address:\n%s", data)
	}

	var back UserConfig
	if err := toml.Unmarshal(data, &back); err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if back.Hosts["build"].Addr != "gaurav@buildbox" {
		t.Errorf("host addr came back as %q", back.Hosts["build"].Addr)
	}
	if back.Hosts["build"].ConnectTimeout != 7 {
		t.Errorf("host connect_timeout came back as %d, want 7", back.Hosts["build"].ConnectTimeout)
	}
}

// TestDefaultConfigShipsNoHosts keeps a machine out of a fresh install. A
// default host would be a peer the user never named, which is the one thing the
// design refuses: hosts exist because somebody wrote them down.
func TestDefaultConfigShipsNoHosts(t *testing.T) {
	cfg := DefaultConfig()
	if len(cfg.Hosts) != 0 {
		t.Fatalf("the default config ships %d host(s): %+v", len(cfg.Hosts), cfg.Hosts)
	}

	path := filepath.Join(t.TempDir(), "config.toml")
	if err := WriteConfigFile(cfg, path); err != nil {
		t.Fatalf("write: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(data), "[hosts") {
		t.Errorf("a config with no hosts wrote a [hosts] table:\n%s", data)
	}
}
