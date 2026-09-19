package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// A served session starts from the seed its server hands in, not from the
// process globals, so a session that connects after a config edit follows the
// file the server just read.
func TestNewOSSeedsAppearanceFromTheOption(t *testing.T) {
	seed := config.DefaultSettings()
	seed.BorderStyle = "double"
	seed.SharedBorders = true

	os := NewOS(OSOptions{Settings: &seed})

	if os.Settings.BorderStyle != "double" {
		t.Errorf("Settings.BorderStyle = %q, want double", os.Settings.BorderStyle)
	}
	if !os.SharedBorders {
		t.Error("SharedBorders did not come from the seed")
	}
	if !os.lastConfigSharedBorders {
		t.Error("lastConfigSharedBorders did not come from the seed")
	}
}

// Without the option the process globals are the seed, which is what the local
// client and the tests pass: it applies the file and the flags before any
// session is built.
func TestNewOSSeedsAppearanceFromTheGlobalsByDefault(t *testing.T) {
	os := NewOS(OSOptions{})

	if os.Settings.BorderStyle != config.Global.BorderStyle {
		t.Errorf("Settings.BorderStyle = %q, want the global %q", os.Settings.BorderStyle, config.Global.BorderStyle)
	}
	if os.SharedBorders != config.Global.SharedBorders {
		t.Error("SharedBorders did not come from the globals")
	}
}
