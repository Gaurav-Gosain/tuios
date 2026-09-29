package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrg/xdg"
)

// TestAQuotedProxyCommandIsNotRunByTheCLI: tuios new --host --ssh reads the
// host from config.toml and runs ssh itself. A pane can write that file, and
// ssh reads a quoted keyword as ProxyCommand, so the CLI must refuse it before
// ssh runs.
//
// Negative control: with CheckSSHOptions back to a list of refused keywords,
// the host resolves and ssh would run the command.
func TestAQuotedProxyCommandIsNotRunByTheCLI(t *testing.T) {
	// Registered before the Setenv calls, so it runs after they are undone.
	t.Cleanup(xdg.Reload)
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("XDG_CONFIG_DIRS", filepath.Join(home, "none"))
	xdg.Reload()
	cfg := filepath.Join(home, "tuios", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o700); err != nil {
		t.Fatal(err)
	}
	body := "[hosts.work]\naddr = \"work.invalid\"\nssh_options = [\"-o\", \"\\\"ProxyCommand\\\" touch /tmp/x\"]\n"
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := resolveConfiguredHost("work")
	if err == nil || !strings.Contains(err.Error(), "ssh_options") {
		t.Fatalf("resolveConfiguredHost = %v, want a refusal of ssh_options", err)
	}
}
