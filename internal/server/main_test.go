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

// pinStartupConfig writes a config that opens one window and starts in
// terminal mode on session start. The kitty graphics crash test depends on
// this: it types a command straight into the startup pane's shell, which only
// exists when these options are on. With the developer's config this was true
// by accident; in a bare environment (CI) the defaults are false and the test
// would drive nothing.
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
