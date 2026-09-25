package config_test

import (
	"path/filepath"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestBackgroundSurvivesSave writes a config holding every background and
// reads it back, so an unset surface stays unset rather than being pinned to
// off by the save, which would stop it following appearance.background.
func TestBackgroundSurvivesSave(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Appearance.Background = "#101010"
	cfg.Appearance.DockBackground = "off"
	cfg.Appearance.Sidebar.Background = "theme"
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := config.WriteConfigFile(cfg, path); err != nil {
		t.Fatalf("save: %v", err)
	}
	back, err := config.ReloadConfig(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	s := config.DefaultSettings()
	config.ApplyAppearanceConfig(back, &s)
	for _, c := range []struct {
		name, got, want string
	}{
		{"pane", s.PaneBackgroundResolved(), "#101010"},
		{"desktop", s.DesktopBackgroundResolved(), "#101010"},
		{"chrome", s.WindowChromeBackgroundResolved(), "#101010"},
		{"dock", s.DockBackgroundResolved(), config.BackgroundOff},
		{"sidebar", s.SidebarBackgroundResolved(), config.BackgroundTheme},
	} {
		if c.got != c.want {
			t.Errorf("%s resolved to %q after a save, want %q", c.name, c.got, c.want)
		}
	}
}
