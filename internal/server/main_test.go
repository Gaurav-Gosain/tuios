package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// TestMain isolates the whole test binary from the developer's own XDG
// directories, so the SSH session tests never read the developer's real
// ~/.config/tuios (whose keybinds and startup options would change what the
// driven sessions do) and never write state into the real home.
func TestMain(m *testing.M) { os.Exit(testutil.RunIsolated(m, pinStartupConfig)) }

func pinStartupConfig(dir string) {
	cfgDir := filepath.Join(dir, "tuios")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		panic(err)
	}
	cfg := "[startup]\nopen_default_window = true\nstart_in_terminal_mode = true\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(cfg), 0o644); err != nil {
		panic(err)
	}
}
