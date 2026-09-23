package app

import (
	"os"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// TestMain isolates the whole test binary from the developer's own XDG
// directories. See testutil.RunIsolated for why this cannot be a per-test
// helper.
//
// It also seeds config.Global with the looks from before v0.8.0 (rail off,
// dock and titles at the bottom, full-screen zoom, single-click typing, thin
// scrollbar). Most fixtures in this package build their OS from the global
// and were measured against that screen: a dock row at the bottom, panes
// that fill the width. The v0.8.0 defaults are tested on their own, from
// config.DefaultSettings, in sidebar_defaults_test.go.
func TestMain(m *testing.M) {
	cfg := config.DefaultConfig()
	config.PinPreV080Appearance(&cfg.Appearance)
	config.ApplyAppearanceConfig(cfg, &config.Global)
	os.Exit(testutil.RunIsolated(m))
}
