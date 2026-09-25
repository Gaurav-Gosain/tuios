package config

import (
	"slices"
	"testing"
)

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
	ApplyAppearanceConfig(cfg, &Global)

	if !Global.SidebarEnabled || Global.SidebarPosition != "right" || Global.SidebarWidth != 34 {
		t.Errorf("enabled/position/width = %v/%q/%d, want true/right/34", Global.SidebarEnabled, Global.SidebarPosition, Global.SidebarWidth)
	}
	if Global.SidebarShowGlyphs || Global.SidebarShowCounts {
		t.Errorf("show glyphs/counts = %v/%v, want both false", Global.SidebarShowGlyphs, Global.SidebarShowCounts)
	}
	// sidebar_show_windows is two spellings old now: it folds into the table,
	// and the table folds it into the layout. The section is gone from the rail
	// and every other section is where it was.
	if got := layoutNames(t); slices.Contains(got, "terminals") {
		t.Errorf("layout = %v, want no terminals section", got)
	}
	if got := layoutNames(t); !slices.Contains(got, "sessions") || !slices.Contains(got, "agents") {
		t.Errorf("layout = %v, want the sections the file never mentioned left alone", got)
	}
	// Knobs the old file never mentioned keep their defaults.
	if !Global.SidebarMarquee || !Global.DockWorkspaceTabs {
		t.Errorf("unmentioned knobs drifted: marquee=%v docktabs=%v", Global.SidebarMarquee, Global.DockWorkspaceTabs)
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
	ApplyAppearanceConfig(cfg, &Global)

	if !Global.SidebarEnabled || Global.SidebarWidth != 20 {
		t.Errorf("enabled/width = %v/%d, want true/20 (the table wins)", Global.SidebarEnabled, Global.SidebarWidth)
	}
}

// layoutNames is the section names of the live layout, in order, for a test
// that cares which sections the rail was left with.
func layoutNames(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, e := range ParseSidebarSections(Global.SidebarSections) {
		out = append(out, e.Name)
	}
	return out
}

// loadTOML parses src the way LoadUserConfig does, defaults and all, so a test
// exercises the same migration and fill order a real config file gets. It
// calls the one parse function rather than repeating its fill list here, which
// is how this helper had drifted to one fill of seven.
func loadTOML(t *testing.T, src string) *UserConfig {
	t.Helper()
	cfg, err := ParseUserConfig([]byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return cfg
}

// withSidebarGlobals restores every sidebar/dock global an apply pass writes, so
// a test may drive ApplyAppearanceConfig without leaking into the next one.
func withSidebarGlobals(t *testing.T) {
	t.Helper()
	e, p, w := Global.SidebarEnabled, Global.SidebarPosition, Global.SidebarWidth
	sg, sc, sx := Global.SidebarShowGlyphs, Global.SidebarShowCounts, Global.SidebarSections
	mq, dt := Global.SidebarMarquee, Global.DockWorkspaceTabs
	t.Cleanup(func() {
		Global.SidebarEnabled, Global.SidebarPosition, Global.SidebarWidth = e, p, w
		Global.SidebarShowGlyphs, Global.SidebarShowCounts, Global.SidebarSections = sg, sc, sx
		Global.SidebarMarquee, Global.DockWorkspaceTabs = mq, dt
	})
}
