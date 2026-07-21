package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/adrg/xdg"
)

// findSetting locates a setting row by category and label, returning its
// category/item indices and the item itself.
func findSetting(m *OS, category, label string) (catIdx, itemIdx int, item settingItem, ok bool) {
	for ci, cat := range m.settingsCategories() {
		if cat.Name != category {
			continue
		}
		for ii, it := range cat.Items {
			if it.Label == label {
				return ci, ii, it, true
			}
		}
	}
	return 0, 0, settingItem{}, false
}

// focusSetting selects a setting row so the Settings* methods act on it.
func focusSetting(t *testing.T, m *OS, category, label string) settingItem {
	t.Helper()
	ci, ii, item, ok := findSetting(m, category, label)
	if !ok {
		t.Fatalf("setting %q not found in category %q", label, category)
	}
	m.SettingsCategory = ci
	m.SettingsSelected = ii
	return item
}

// editSetting drives the inline text-edit flow (begin, type, commit) on the
// currently focusable text setting.
func editSetting(t *testing.T, m *OS, category, label, value string) {
	t.Helper()
	focusSetting(t, m, category, label)
	m.SettingsBeginEdit()
	if !m.SettingsEditActive() {
		t.Fatalf("editing %q did not activate", label)
	}
	m.SettingsEditBuffer = value
	m.SettingsEditCommit()
	if m.SettingsEditActive() {
		t.Fatalf("commit of %q left the editor active", label)
	}
}

// useTempConfig points the XDG config dir at a temp location so persistSettings
// never touches the developer's real config, and returns the resolved path.
func useTempConfig(t *testing.T) string {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	xdg.Reload()
	path, err := xdg.ConfigFile("tuios/config.toml")
	if err != nil {
		t.Fatalf("resolve temp config path: %v", err)
	}
	return path
}

// TestSettingsCoverage asserts every setting this audit added is present in the
// menu, so a future refactor cannot silently drop one.
func TestSettingsCoverage(t *testing.T) {
	m := NewOS(OSOptions{UserConfig: config.DefaultConfig()})
	want := map[string][]string{
		"Appearance": {"Focused border color", "Unfocused border color", "Window title format"},
		"Behavior":   {"Preferred shell"},
		"Daemon":     {"Log level"},
	}
	for category, labels := range want {
		for _, label := range labels {
			if _, _, _, ok := findSetting(m, category, label); !ok {
				t.Errorf("expected setting %q in category %q, not found", label, category)
			}
		}
	}
}

// TestStringSettingsApplyAndPersist toggles the new text settings through the
// menu and verifies both the live effect and that the value survives a reload
// from disk.
func TestStringSettingsApplyAndPersist(t *testing.T) {
	useTempConfig(t)
	m := NewOS(OSOptions{UserConfig: config.DefaultConfig()})

	editSetting(t, m, "Appearance", "Focused border color", "#ff0000")
	if got := m.UserConfig.Appearance.BorderFocusedColor; got != "#ff0000" {
		t.Errorf("focused border color = %q, want #ff0000", got)
	}
	// The override must reach the theme package so borders recolor live.
	if theme.BorderFocusedWindow() == nil {
		t.Error("focused border override was not applied to the theme package")
	}

	editSetting(t, m, "Appearance", "Unfocused border color", "#00ff00")
	if got := m.UserConfig.Appearance.BorderUnfocusedColor; got != "#00ff00" {
		t.Errorf("unfocused border color = %q, want #00ff00", got)
	}

	editSetting(t, m, "Appearance", "Window title format", "{index}: {title}")
	if config.WindowTitleFormat != "{index}: {title}" {
		t.Errorf("window title format global = %q, want {index}: {title}", config.WindowTitleFormat)
	}
	if m.UserConfig.Appearance.WindowTitleFormat != "{index}: {title}" {
		t.Errorf("window title format config = %q", m.UserConfig.Appearance.WindowTitleFormat)
	}

	editSetting(t, m, "Behavior", "Preferred shell", "/bin/zsh")
	if m.UserConfig.Appearance.PreferredShell != "/bin/zsh" {
		t.Errorf("preferred shell = %q, want /bin/zsh", m.UserConfig.Appearance.PreferredShell)
	}

	// Reload from disk: the persisted file must carry every edited value.
	reloaded, err := config.LoadUserConfig()
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if reloaded.Appearance.BorderFocusedColor != "#ff0000" ||
		reloaded.Appearance.BorderUnfocusedColor != "#00ff00" ||
		reloaded.Appearance.WindowTitleFormat != "{index}: {title}" ||
		reloaded.Appearance.PreferredShell != "/bin/zsh" {
		t.Errorf("persisted appearance did not round-trip: %+v", reloaded.Appearance)
	}
}

// TestStringSettingEditCancel keeps a cancelled edit from changing anything.
func TestStringSettingEditCancel(t *testing.T) {
	useTempConfig(t)
	m := NewOS(OSOptions{UserConfig: config.DefaultConfig()})
	m.UserConfig.Appearance.PreferredShell = "/bin/bash"

	focusSetting(t, m, "Behavior", "Preferred shell")
	m.SettingsBeginEdit()
	m.SettingsEditBuffer = "/bin/fish"
	m.SettingsEditCancel()
	if m.SettingsEditActive() {
		t.Fatal("cancel left the editor active")
	}
	if m.UserConfig.Appearance.PreferredShell != "/bin/bash" {
		t.Errorf("cancel changed the value to %q", m.UserConfig.Appearance.PreferredShell)
	}
}

// TestDaemonLogLevelPersists cycles the daemon log level enum and verifies it
// persists to disk.
func TestDaemonLogLevelPersists(t *testing.T) {
	useTempConfig(t)
	m := NewOS(OSOptions{UserConfig: config.DefaultConfig()})

	focusSetting(t, m, "Daemon", "Log level")
	if got := m.daemonLogLevel(); got != "off" {
		t.Fatalf("default log level = %q, want off", got)
	}
	m.SettingsAdjust(1) // off -> errors
	if got := m.UserConfig.Daemon.LogLevel; got != "errors" {
		t.Fatalf("after one step log level = %q, want errors", got)
	}

	reloaded, err := config.LoadUserConfig()
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if reloaded.Daemon.LogLevel != "errors" {
		t.Errorf("persisted daemon log level = %q, want errors", reloaded.Daemon.LogLevel)
	}
}

// TestSettingsStringValuesNilConfigSafe guards the getters against a bare OS
// (no held config), the shape used by several render/mouse tests.
func TestSettingsStringValuesNilConfigSafe(t *testing.T) {
	m := &OS{}
	for _, cat := range m.settingsCategories() {
		for _, item := range cat.Items {
			if item.value != nil {
				_ = item.value(m) // must not panic
			}
		}
	}
}

// TestSharedBordersPaletteToggles verifies the quick palette command flips and
// persists the shared-borders setting.
func TestSharedBordersPaletteToggles(t *testing.T) {
	useTempConfig(t)
	m := NewOS(OSOptions{UserConfig: config.DefaultConfig()})
	config.SharedBorders = false

	var toggle *CommandPaletteItem
	for _, item := range GetCommandPaletteItems() {
		if item.Name == "Toggle Shared Borders" {
			it := item
			toggle = &it
			break
		}
	}
	if toggle == nil {
		t.Fatal("Toggle Shared Borders command not found in the palette")
	}

	toggle.Action(m)
	if !config.SharedBorders {
		t.Error("palette toggle did not enable shared borders")
	}
	if m.UserConfig.Appearance.SharedBorders == nil || !*m.UserConfig.Appearance.SharedBorders {
		t.Error("palette toggle did not mirror shared borders to config")
	}

	reloaded, err := config.LoadUserConfig()
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if reloaded.Appearance.SharedBorders == nil || !*reloaded.Appearance.SharedBorders {
		t.Error("shared borders did not persist to disk")
	}
}
