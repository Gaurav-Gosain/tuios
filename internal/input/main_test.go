package input

import (
	"os"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// TestMain isolates the whole test binary from the developer's own XDG
// directories.
//
// Constructing an app.OS loads (and, when absent, writes) the user config, and
// several overlays read and write tape and layout files. See
// testutil.RunIsolated for why this cannot be a per-test helper.
//
// It also seeds config.Global with the looks from before v0.8.0, which the
// fixtures here were measured against: a click that starts typing, the dock
// at the bottom, no rail. internal/app's TestMain says the same.
func TestMain(m *testing.M) {
	cfg := config.DefaultConfig()
	config.PinPreV080Appearance(&cfg.Appearance)
	config.ApplyAppearanceConfig(cfg, &config.Global)
	os.Exit(testutil.RunIsolated(m))
}
