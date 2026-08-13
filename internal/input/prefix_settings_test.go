package input

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
	"github.com/adrg/xdg"
)

// legacyConfig loads src as the user's config.toml. The XDG search paths are
// resolved at package init, so the reload is what makes the redirect take.
func legacyConfig(t *testing.T, src string) *config.UserConfig {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	xdg.Reload()
	t.Cleanup(xdg.Reload)
	if err := os.MkdirAll(filepath.Join(dir, "tuios"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tuios", "config.toml"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadUserConfig()
	if err != nil {
		t.Fatalf("LoadUserConfig: %v", err)
	}
	return cfg
}

// osWithFocusedPane builds an OS with one focused pane whose PTY records what
// reaches the guest, so a test can drive keys through the real HandleKeyPress
// path and see both what the app did and what the shell was typed into.
func osWithFocusedPane(t *testing.T, cfg *config.UserConfig, mode app.Mode) (*app.OS, *capturePty) {
	t.Helper()
	em := vt.NewEmulator(80, 24)
	t.Cleanup(func() { _ = em.Close() })
	pty := &capturePty{}
	win := &terminal.Window{ID: "prefix-0001", Terminal: em, Pty: pty, X: 0, Y: 0, Width: 82, Height: 26}
	o := app.NewOS(app.OSOptions{UserConfig: cfg, KeybindRegistry: config.NewKeybindRegistry(cfg)})
	o.Windows = []*terminal.Window{win}
	o.FocusedWindow = 0
	win.Workspace = o.CurrentWorkspace
	o.Mode = mode
	return o, pty
}

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
		_, pty := pressLeaderThen(t, cfg, app.TerminalMode, keys[0])
		if got := string(pty.got); got != "" {
			t.Errorf("%s (leader %q) leaked %q to the guest", action, keys[0], got)
		}
	}
}
