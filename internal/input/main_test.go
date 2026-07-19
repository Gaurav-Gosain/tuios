package input

import (
	"os"
	"testing"

	"github.com/adrg/xdg"
)

// TestMain points every XDG directory at a throwaway tree for the whole test
// binary.
//
// Constructing an app.OS loads (and, when absent, writes) the user config, and
// several overlays read and write tape and layout files. The XDG paths are
// resolved at package init, so a per-test t.Setenv cannot redirect them; without
// this, running the tests writes into the developer's real ~/.config/tuios and
// ~/.local/share/tuios.
func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "tuios-input-test-xdg")
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

	code := m.Run()

	_ = os.RemoveAll(tmp)
	os.Exit(code)
}
