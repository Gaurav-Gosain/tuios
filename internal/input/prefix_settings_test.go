//go:build !slim

package input

import (
	"sort"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// pressLeaderThen runs the leader chord through the same entry point the
// bubbletea Update loop uses. The bug this pins is in routing, so the keys have
// to travel the whole path rather than reach a handler directly.
func pressLeaderThen(t *testing.T, cfg *config.UserConfig, mode app.Mode, key string) (*app.OS, *capturePty) {
	t.Helper()
	o, pty := osWithFocusedPane(t, cfg, mode)
	o, _ = HandleKeyPress(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl}, o)
	if !o.PrefixActive {
		t.Fatalf("leader did not arm the prefix in mode %v", mode)
	}
	o, _ = HandleKeyPress(press(key), o)
	return o, pty
}

// legacyRenameConfig is a config from before "," moved from prefix_rename_window
// to prefix_settings, which is what a long-lived config still holds.
const legacyRenameConfig = "[keybindings]\nleader_key = \"ctrl+b\"\n\n" +
	"[keybindings.prefix_mode]\nprefix_rename_window = [\",\", \"r\"]\nprefix_settings = [\",\"]\n"

// TestLeaderSettingsOpensInBothModes is the reported bug: leader "," did not
// open settings from terminal mode. It was never about the mode. Two actions
// claimed "," in one section, and the section was resolved through a map built
// in Go's randomized iteration order, so the chord opened settings on some
// presses and renamed the pane on others. With window titles hidden the rename
// does nothing visible, so the losing presses looked like a dead key.
//
// The repeat is the point: one press proves nothing about a coin flip.
func TestLeaderSettingsOpensInBothModes(t *testing.T) {
	for _, mode := range []app.Mode{app.TerminalMode, app.WindowManagementMode} {
		cfg := legacyConfig(t, legacyRenameConfig)
		for i := range 30 {
			o, pty := pressLeaderThen(t, cfg, mode, ",")
			if !o.ShowSettings {
				t.Fatalf("mode %v, press %d: leader \",\" did not open settings", mode, i)
			}
			if got := string(pty.got); got != "" {
				t.Fatalf("mode %v: the chord leaked %q to the guest", mode, got)
			}
		}
	}
}

// TestPrefixActionsDoNotLeakToGuest is the same bug wearing the other face: a
// chord that fires must not also be typed into the shell. Every action in the
// main prefix section is driven from terminal mode against a pane that records
// what it receives.
func TestPrefixActionsDoNotLeakToGuest(t *testing.T) {
	cfg := config.DefaultConfig()
	actions := make([]string, 0, len(cfg.Keybindings.PrefixMode))
	for action := range cfg.Keybindings.PrefixMode {
		actions = append(actions, action)
	}
	sort.Strings(actions)

	for _, action := range actions {
		keys := cfg.Keybindings.PrefixMode[action]
		if len(keys) == 0 {
			continue
		}
		// A pending action does nothing yet, so its chord is typed into the
		// pane on purpose; TestPendingActionsStillDoNothing checks that.
		if _, pending := pendingActions[action]; pending {
			continue
		}
		_, pty := pressLeaderThen(t, cfg, app.TerminalMode, keys[0])
		if got := string(pty.got); got != "" {
			t.Errorf("%s (leader %q) leaked %q to the guest", action, keys[0], got)
		}
	}
}
