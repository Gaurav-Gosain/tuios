package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

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
