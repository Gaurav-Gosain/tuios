package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/adrg/xdg"
)

// TestMain points every XDG directory at a throwaway tree for the whole test
// binary, so the SSH session tests never read the developer's real
// ~/.config/tuios (whose keybinds and startup options would change what the
// driven sessions do) and never write state into the real home.
//
// It also pins a config that opens one window and starts in terminal mode on
// session start. The kitty graphics crash test depends on this: it types a
// command straight into the startup pane's shell, which only exists when
// these options are on. With the developer's config this was true by
// accident; in a bare environment (CI) the defaults are false and the test
// would drive nothing.
func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "tuios-server-test-xdg")
	if err != nil {
		panic(err)
	}

	for _, name := range []string{
		"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME",
		"XDG_CACHE_HOME", "XDG_RUNTIME_DIR",
	} {
		if err := os.Setenv(name, tmp); err != nil {
			panic(err)
		}
	}
	xdg.Reload()

	cfgDir := filepath.Join(tmp, "tuios")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		panic(err)
	}
	cfg := "[startup]\nopen_default_window = true\nstart_in_terminal_mode = true\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(cfg), 0o644); err != nil {
		panic(err)
	}

	code := m.Run()

	_ = os.RemoveAll(tmp)
	os.Exit(code)
}
