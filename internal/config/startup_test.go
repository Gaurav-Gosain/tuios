package config_test

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	toml "github.com/pelletier/go-toml/v2"
)

// TestStartupConfigDefaults confirms both [startup] options default to false so
// a fresh install behaves exactly as before: an empty, floating session.
func TestStartupConfigDefaults(t *testing.T) {
	cfg := config.DefaultConfig()
	if cfg.Startup.OpenDefaultWindow {
		t.Error("open_default_window should default to false")
	}
	if cfg.Startup.Tiled {
		t.Error("tiled should default to false")
	}
}

// TestStartupConfigParsing confirms both options round-trip from TOML.
func TestStartupConfigParsing(t *testing.T) {
	const src = `
[startup]
open_default_window = true
tiled = true
`
	var cfg config.UserConfig
	if err := toml.Unmarshal([]byte(src), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !cfg.Startup.OpenDefaultWindow {
		t.Error("expected open_default_window = true after parsing")
	}
	if !cfg.Startup.Tiled {
		t.Error("expected tiled = true after parsing")
	}
}

// TestStartupConfigAbsentDefaultsFalse confirms that omitting the [startup]
// section leaves both options false rather than picking up some other value.
func TestStartupConfigAbsentDefaultsFalse(t *testing.T) {
	const src = `
[appearance]
border_style = "rounded"
`
	var cfg config.UserConfig
	if err := toml.Unmarshal([]byte(src), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cfg.Startup.OpenDefaultWindow || cfg.Startup.Tiled {
		t.Errorf("absent [startup] should leave both options false, got open=%v tiled=%v",
			cfg.Startup.OpenDefaultWindow, cfg.Startup.Tiled)
	}
}
