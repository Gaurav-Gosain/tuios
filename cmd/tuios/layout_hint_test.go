package main

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestLayoutSaveHint checks the empty 'layout list' message names a way to
// save a layout that exists. It once said "tuios layout save <name>", a
// command that was never there.
func TestLayoutSaveHint(t *testing.T) {
	root := newRootCommand()
	if cmd, _, err := root.Find([]string{"layout", "save"}); err == nil && cmd.Name() == "save" {
		t.Fatal("layout save exists now; the hint should name it")
	}

	cfg := config.DefaultConfig()
	got := layoutSaveHint(cfg)
	if strings.Contains(got, "layout save") {
		t.Errorf("hint names a command that does not exist: %q", got)
	}
	if !strings.Contains(got, "ctrl+b L s") {
		t.Errorf("hint does not name the default keys ctrl+b L s: %q", got)
	}

	cfg.Keybindings.LeaderKey = "ctrl+a"
	cfg.Keybindings.LayoutPrefix["layout_prefix_save"] = []string{"w"}
	if got := layoutSaveHint(cfg); !strings.Contains(got, "ctrl+a L w") {
		t.Errorf("hint does not follow the configured keys: %q", got)
	}

	cfg.Keybindings.LayoutPrefix["layout_prefix_save"] = nil
	if got := layoutSaveHint(cfg); !strings.Contains(got, "Save layout") {
		t.Errorf("hint with no save key does not fall back to the palette: %q", got)
	}
}
