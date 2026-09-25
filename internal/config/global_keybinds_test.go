package config_test

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestSettingsCommaMigratesOffTheDeadResize covers the config an existing user
// already has on disk. "," has been open_settings in practice since the literal
// landed; the stale layout claim would now be reported as a conflict for a key
// the user never chose.
func TestSettingsCommaMigratesOffTheDeadResize(t *testing.T) {
	cfg := writeConfig(t, strings.Join([]string{
		"[keybindings.layout]",
		`resize_master_shrink_left = [","]`,
		"",
	}, "\n"))
	if keys, ok := cfg.Keybindings.Layout["resize_master_shrink_left"]; ok {
		t.Errorf("the stale claim survived: %q", keys)
	}
	for _, w := range config.ValidateConfig(cfg).Warnings {
		if strings.Contains(w.Message, "','") {
			t.Errorf("an existing config warns about \",\": %s", w.Message)
		}
	}
}

// TestOwnResizeCommaIsLeftAlone guards the other half: a user who deliberately
// put "," on the resize action keeps it, warning and all. Quietly deleting
// someone's binding to tidy a report would be the worse bug.
func TestOwnResizeCommaIsLeftAlone(t *testing.T) {
	cfg := writeConfig(t, strings.Join([]string{
		"[keybindings.layout]",
		`resize_master_shrink_left = [",", "ctrl+alt+left"]`,
		"",
	}, "\n"))
	keys := cfg.Keybindings.Layout["resize_master_shrink_left"]
	if len(keys) != 2 || keys[0] != "," {
		t.Errorf("a deliberate binding was rewritten: %q", keys)
	}
}
