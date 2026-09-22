package main

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestKeybindsListShowsTheWholePress. 'tuios keybinds list' listed launcher as
// "a", which is its key after the leader. Pressed on its own, a does nothing
// of the kind. The default is alt+space.
func TestKeybindsListShowsTheWholePress(t *testing.T) {
	cfg := config.DefaultConfig()
	leader := cfg.Keybindings.LeaderKey
	presses := config.PressesByAction(config.NewKeybindRegistry(cfg))

	for action, want := range map[string][]string{
		"launcher":        {"alt+space", leader + " a"},
		"command_palette": {"ctrl+p"},
		"next_session":    {"alt+shift+n", leader + " )"},
		"new_window":      {"n"},
	} {
		rows := keybindListRows(presses, []string{action})
		if len(rows) != 1 {
			t.Fatalf("%s: got %d rows, want 1", action, len(rows))
		}
		keys := strings.Split(rows[0][0], ", ")
		for _, press := range want {
			found := false
			for _, k := range keys {
				if k == press {
					found = true
				}
			}
			if !found {
				t.Errorf("%s: listed as %q, which does not include %q", action, rows[0][0], press)
			}
		}
		if len(keys) != len(want) {
			t.Errorf("%s: listed as %q, want exactly %v", action, rows[0][0], want)
		}
	}
}
