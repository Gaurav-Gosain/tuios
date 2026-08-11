package config

import (
	"slices"
	"strings"
	"testing"

	toml "github.com/pelletier/go-toml/v2"
)

// withSidebarGlobals restores every sidebar/dock global an apply pass writes, so
// a test may drive ApplyAppearanceConfig without leaking into the next one.
func withSidebarGlobals(t *testing.T) {
	t.Helper()
	e, p, w := SidebarEnabled, SidebarPosition, SidebarWidth
	sw, sg, sc := SidebarShowWindows, SidebarShowGlyphs, SidebarShowCounts
	sa, ws, mq, dt := SidebarShowAgents, SidebarWorkspaces, SidebarMarquee, DockWorkspaceTabs
	t.Cleanup(func() {
		SidebarEnabled, SidebarPosition, SidebarWidth = e, p, w
		SidebarShowWindows, SidebarShowGlyphs, SidebarShowCounts = sw, sg, sc
		SidebarShowAgents, SidebarWorkspaces, SidebarMarquee, DockWorkspaceTabs = sa, ws, mq, dt
	})
}

// loadTOML parses src the way LoadUserConfig does, defaults and all, so a test
// exercises the same migration and fill order a real config file gets.
func loadTOML(t *testing.T, src string) *UserConfig {
	t.Helper()
	var cfg UserConfig
	if err := toml.Unmarshal([]byte(src), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	fillMissingAppearance(&cfg, DefaultConfig())
	return &cfg
}

// TestLegacyFlatSidebarKeysStillApply is the backward-compatibility contract: a
// config written before [appearance.sidebar] existed must reach the globals
// unchanged.
func TestLegacyFlatSidebarKeysStillApply(t *testing.T) {
	withSidebarGlobals(t)

	cfg := loadTOML(t, `
[appearance]
sidebar_enabled = true
sidebar_position = "right"
sidebar_width = 34
sidebar_show_windows = false
sidebar_show_glyphs = false
sidebar_show_counts = false
`)
	ApplyAppearanceConfig(cfg)

	if !SidebarEnabled || SidebarPosition != "right" || SidebarWidth != 34 {
		t.Errorf("enabled/position/width = %v/%q/%d, want true/right/34", SidebarEnabled, SidebarPosition, SidebarWidth)
	}
	if SidebarShowWindows || SidebarShowGlyphs || SidebarShowCounts {
		t.Errorf("show windows/glyphs/counts = %v/%v/%v, want all false",
			SidebarShowWindows, SidebarShowGlyphs, SidebarShowCounts)
	}
	// Knobs the old file never mentioned keep their defaults.
	if !SidebarShowAgents || SidebarWorkspaces != SidebarWorkspacesBand || !SidebarMarquee || !DockWorkspaceTabs {
		t.Errorf("unmentioned knobs drifted: agents=%v workspaces=%q marquee=%v docktabs=%v",
			SidebarShowAgents, SidebarWorkspaces, SidebarMarquee, DockWorkspaceTabs)
	}
}

// TestSidebarTableWinsOverLegacyKeys covers a config that carries both
// spellings, which is what a hand-edited file mid-upgrade looks like.
func TestSidebarTableWinsOverLegacyKeys(t *testing.T) {
	withSidebarGlobals(t)

	cfg := loadTOML(t, `
[appearance]
sidebar_enabled = false
sidebar_width = 34

[appearance.sidebar]
enabled = true
width = 20
`)
	ApplyAppearanceConfig(cfg)

	if !SidebarEnabled || SidebarWidth != 20 {
		t.Errorf("enabled/width = %v/%d, want true/20 (the table wins)", SidebarEnabled, SidebarWidth)
	}
}

// TestSidebarNilTogglesKeepDefaults pins the tri-state: an absent toggle leaves
// the built-in default alone rather than reading as false.
func TestSidebarNilTogglesKeepDefaults(t *testing.T) {
	withSidebarGlobals(t)
	SidebarShowWindows, SidebarShowAgents, SidebarMarquee, DockWorkspaceTabs = true, true, true, true

	cfg := loadTOML(t, `
[appearance.sidebar]
enabled = true
`)
	if s := cfg.Appearance.Sidebar; s.ShowWindows != nil || s.ShowAgents != nil || s.Marquee != nil {
		t.Fatalf("absent toggles parsed as non-nil: %+v", s)
	}
	ApplyAppearanceConfig(cfg)

	if !SidebarShowWindows || !SidebarShowAgents || !SidebarMarquee || !DockWorkspaceTabs {
		t.Errorf("nil toggles overwrote the defaults: windows=%v agents=%v marquee=%v docktabs=%v",
			SidebarShowWindows, SidebarShowAgents, SidebarMarquee, DockWorkspaceTabs)
	}
}

// TestSidebarExplicitFalseSurvivesApply is the other half of the tri-state: an
// explicit false must reach the global, which is what makes a toggle turned off
// in the settings page survive a reload.
func TestSidebarExplicitFalseSurvivesApply(t *testing.T) {
	withSidebarGlobals(t)
	SidebarShowAgents, SidebarMarquee, DockWorkspaceTabs = true, true, true

	cfg := loadTOML(t, `
[appearance]
dock_workspace_tabs = false

[appearance.sidebar]
show_agents = false
marquee = false
workspaces = "off"
`)
	ApplyAppearanceConfig(cfg)

	if SidebarShowAgents || SidebarMarquee || DockWorkspaceTabs {
		t.Errorf("explicit false dropped: agents=%v marquee=%v docktabs=%v",
			SidebarShowAgents, SidebarMarquee, DockWorkspaceTabs)
	}
	if SidebarWorkspaces != SidebarWorkspacesOff {
		t.Errorf("workspaces = %q, want off", SidebarWorkspaces)
	}
}

// TestSidebarSaveDropsLegacyKeys checks the migration is one-way: what a saved
// config writes is the table, so the flat keys do not linger and start
// disagreeing with it.
func TestSidebarSaveDropsLegacyKeys(t *testing.T) {
	cfg := loadTOML(t, `
[appearance]
sidebar_enabled = true
sidebar_position = "right"
`)
	data, err := toml.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out := string(data)
	for _, key := range []string{"sidebar_enabled", "sidebar_position", "sidebar_width", "sidebar_show_"} {
		if strings.Contains(out, key) {
			t.Errorf("saved config still carries the legacy key %q", key)
		}
	}

	var round UserConfig
	if err := toml.Unmarshal(data, &round); err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if s := round.Appearance.Sidebar; s.Enabled == nil || !*s.Enabled || s.Position != "right" {
		t.Errorf("round trip lost the migrated values: %+v", s)
	}
	// The table is written last, so no [appearance] scalar falls inside it.
	if round.Appearance.BorderStyle != cfg.Appearance.BorderStyle {
		t.Errorf("border_style = %q after round trip, want %q", round.Appearance.BorderStyle, cfg.Appearance.BorderStyle)
	}
}

// TestLegacyEscapeBindingMovesOffDetach covers the one binding whose meaning
// changed when the prefix handlers started honouring the config. Older configs
// list esc under prefix_detach, but the old hard-coded handler made esc leave
// terminal mode and never detach. Loading such a config must not turn esc into a
// detach key.
func TestLegacyEscapeBindingMovesOffDetach(t *testing.T) {
	cfg := &UserConfig{}
	cfg.Keybindings.PrefixMode = map[string][]string{
		"prefix_detach": {"d", "esc"},
	}

	fillMissingKeybinds(cfg, DefaultConfig())

	detach := cfg.Keybindings.PrefixMode["prefix_detach"]
	if slices.Contains(detach, "esc") {
		t.Errorf("prefix_detach = %v, want esc removed", detach)
	}
	if !slices.Contains(detach, "d") {
		t.Errorf("prefix_detach = %v, want d kept", detach)
	}
	if exit := cfg.Keybindings.PrefixMode["prefix_exit_mode"]; !slices.Contains(exit, "esc") {
		t.Errorf("prefix_exit_mode = %v, want esc", exit)
	}
}

// TestMigrationLeavesADeliberateEscapeDetachAlone checks the migration does not
// fight a user who has since said what they want: once prefix_exit_mode is
// present, the config was written by a version that knew about the split, and
// esc on prefix_detach is a deliberate choice.
func TestMigrationLeavesADeliberateEscapeDetachAlone(t *testing.T) {
	cfg := &UserConfig{}
	cfg.Keybindings.PrefixMode = map[string][]string{
		"prefix_detach":    {"esc"},
		"prefix_exit_mode": {"q"},
	}

	fillMissingKeybinds(cfg, DefaultConfig())

	if exit := cfg.Keybindings.PrefixMode["prefix_exit_mode"]; !slices.Contains(exit, "q") {
		t.Errorf("prefix_exit_mode = %v, want the user's own q binding kept", exit)
	}
}
