package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestNewOS_DoesNotClobberAppearanceGlobals is a regression test for the config
// reload bug: NewOS used to call LoadUserConfig a second time, re-applying the
// file's appearance over CLI flags already reconciled at startup (e.g.
// `tuios --no-animations` starting with animations on). NewOS must now use the
// passed-in config without mutating any appearance package global.
func TestNewOS_DoesNotClobberAppearanceGlobals(t *testing.T) {
	original := config.AnimationsEnabled
	defer func() { config.AnimationsEnabled = original }()

	// Simulate a CLI flag having forced animations off at startup.
	config.AnimationsEnabled = false

	// A user config that enables animations, as it would be on disk.
	enabled := true
	cfg := config.DefaultConfig()
	cfg.Appearance.AnimationsEnabled = &enabled

	os := NewOS(OSOptions{UserConfig: cfg})

	if config.AnimationsEnabled {
		t.Error("NewOS must not re-apply config appearance and re-enable animations")
	}
	if os.UserConfig != cfg {
		t.Error("NewOS should hold the passed-in config for the settings page to persist")
	}
}
