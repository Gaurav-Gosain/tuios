package config_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/adrg/xdg"
)

// writeConfig points XDG at a throwaway tree holding src as the user's
// config.toml, so a test loads through the same path a real file takes.
//
// The XDG search paths are resolved once at package init, so the reload is what
// makes the redirect take: without it LoadUserConfig reads the developer's own
// ~/.config/tuios/config.toml and the test asserts against whatever happens to
// be there.
func writeConfig(t *testing.T, src string) *config.UserConfig {
	t.Helper()
	dir := t.TempDir()
	// Registered before t.Setenv so it runs after it: cleanups are LIFO, and a
	// reload that ran first would re-resolve against the temp dir and leave the
	// xdg globals pointing at it for the rest of the binary, after the directory
	// is gone.
	t.Cleanup(xdg.Reload)
	t.Setenv("XDG_CONFIG_HOME", dir)
	xdg.Reload()
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

// TestSettingsKeyReclaimedFromLegacyRename is the config half of the leader-","
// bug. "," used to be a second binding for prefix_rename_window; it became the
// binding for prefix_settings, and prefix_rename_window kept "r". A config
// written before that move still names "," under rename, and filling defaults
// adds prefix_settings on the same key, so both claim it. Whichever wins, the
// user has lost one of the two commands.
//
// The first case is a config from before prefix_settings existed. The second is
// the state that config is left in once it has been saved back with the filled
// default, which is what a long-lived config looks like on disk.
func TestSettingsKeyReclaimedFromLegacyRename(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{
			name: "before prefix_settings existed",
			src: "[keybindings]\nleader_key = \"ctrl+b\"\n\n" +
				"[keybindings.prefix_mode]\nprefix_rename_window = [\",\", \"r\"]\n",
		},
		{
			name: "saved back with the filled default",
			src: "[keybindings]\nleader_key = \"ctrl+b\"\n\n" +
				"[keybindings.prefix_mode]\nprefix_rename_window = [\",\", \"r\"]\nprefix_settings = [\",\"]\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := writeConfig(t, tt.src)
			r := config.NewKeybindRegistry(cfg)

			for range 50 {
				if got := r.GetPrefixAction(","); got != "prefix_settings" {
					t.Fatalf("GetPrefixAction(\",\") = %q, want prefix_settings", got)
				}
			}
			// The rename chord keeps the key it was re-homed onto.
			if got := r.GetPrefixAction("r"); got != "prefix_rename_window" {
				t.Fatalf("GetPrefixAction(\"r\") = %q, want prefix_rename_window", got)
			}
			if keys := cfg.Keybindings.PrefixMode["prefix_rename_window"]; slices.Contains(keys, ",") {
				t.Fatalf("prefix_rename_window kept the stale \",\": %v", keys)
			}
		})
	}
}

// TestUserOwnedDuplicateKeyIsLeftAlone is the other side of the repair: a key
// no default claims is the user's own arrangement, and rewriting it would be
// the config editing itself. It still has to resolve the same way every time.
func TestUserOwnedDuplicateKeyIsLeftAlone(t *testing.T) {
	cfg := writeConfig(t, "[keybindings]\nleader_key = \"ctrl+b\"\n\n"+
		"[keybindings.prefix_mode]\nprefix_help = [\"g\"]\nprefix_scrollback = [\"g\"]\n")

	for _, action := range []string{"prefix_help", "prefix_scrollback"} {
		if keys := cfg.Keybindings.PrefixMode[action]; !slices.Contains(keys, "g") {
			t.Fatalf("%s lost its user-set \"g\": %v", action, keys)
		}
	}

	r := config.NewKeybindRegistry(cfg)
	first := r.GetPrefixAction("g")
	if first == "" {
		t.Fatal("GetPrefixAction(\"g\") resolved to nothing")
	}
	for range 50 {
		if got := r.GetPrefixAction("g"); got != first {
			t.Fatalf("GetPrefixAction(\"g\") = %q then %q; a duplicate must resolve the same way every press", first, got)
		}
	}
}

// TestNoDefaultSectionBindsAKeyTwice guards the defaults themselves. Two
// actions in one section on the same key is unresolvable by construction: the
// section is one keymap, so one of them can never fire.
func TestNoDefaultSectionBindsAKeyTwice(t *testing.T) {
	cfg := config.DefaultConfig()
	sections := map[string]map[string][]string{
		"window_management": cfg.Keybindings.WindowManagement,
		"workspaces":        cfg.Keybindings.Workspaces,
		"layout":            cfg.Keybindings.Layout,
		"mode_control":      cfg.Keybindings.ModeControl,
		"system":            cfg.Keybindings.System,
		"navigation":        cfg.Keybindings.Navigation,
		"restore_minimized": cfg.Keybindings.RestoreMinimized,
		"prefix_mode":       cfg.Keybindings.PrefixMode,
		"window_prefix":     cfg.Keybindings.WindowPrefix,
		"minimize_prefix":   cfg.Keybindings.MinimizePrefix,
		"workspace_prefix":  cfg.Keybindings.WorkspacePrefix,
		"debug_prefix":      cfg.Keybindings.DebugPrefix,
		"tape_prefix":       cfg.Keybindings.TapePrefix,
		"terminal_mode":     cfg.Keybindings.TerminalMode,
		"sidebar":           cfg.Keybindings.Sidebar,
	}

	for name, section := range sections {
		owner := make(map[string]string)
		for action, keys := range section {
			for _, key := range keys {
				if prev, taken := owner[key]; taken {
					t.Errorf("[%s] key %q is bound to both %s and %s", name, key, prev, action)
					continue
				}
				owner[key] = action
			}
		}
	}
}
