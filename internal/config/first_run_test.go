package config_test

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/adrg/xdg"
)

// TestFirstRunIsTheFileThisProcessWrote: a load that writes the first-start
// config.toml is the first run, and so is a later load of that same file in
// the process (the attach path after the client loaded). A load of another
// config folder is not. The e2e suite's sim backend runs many clients in one
// process, each with its own folder, and the welcome question covered the
// screen of every test after the first one that wrote a first-start file.
//
// Negative control: make the flag process-wide again (FirstRun true once
// any first-start file was written). The last load then reports a first run.
func TestFirstRunIsTheFileThisProcessWrote(t *testing.T) {
	t.Cleanup(xdg.Reload)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	xdg.Reload()
	cfg, err := config.LoadUserConfig()
	if err != nil {
		t.Fatalf("LoadUserConfig in an empty folder: %v", err)
	}
	if !cfg.FirstRun {
		t.Fatal("the load that wrote the first-start config.toml is not a first run")
	}
	cfg, err = config.LoadUserConfig()
	if err != nil {
		t.Fatalf("LoadUserConfig of the file it wrote: %v", err)
	}
	if !cfg.FirstRun {
		t.Fatal("a second load of the first-start config.toml is not a first run")
	}

	if cfg := writeConfig(t, "[keybindings]\nleader_key = 'ctrl+s'\n"); cfg.FirstRun {
		t.Fatal("a load of another config folder, which holds a config.toml, is a first run")
	}
}
