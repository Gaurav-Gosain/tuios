package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestSidebarTabCoverage asserts every knob in [appearance.sidebar] has a row,
// so a knob added to the config cannot quietly stay TOML-only.
func TestSidebarTabCoverage(t *testing.T) {
	m := NewOS(OSOptions{UserConfig: config.DefaultConfig()})
	want := map[string][]string{
		"Sidebar": {
			"Sidebar", "Position", "Width", "Show windows", "Show glyphs",
			"Show counts", "Agents section", "Marquee",
		},
		"Dock": {"Workspace tabs"},
	}
	for category, labels := range want {
		for _, label := range labels {
			if _, _, _, ok := findSetting(m, category, label); !ok {
				t.Errorf("expected setting %q in category %q, not found", label, category)
			}
		}
	}
}

// TestSidebarSettingsApplyLiveAndPersist drives each new row through the panel
// and checks both halves of the contract: the global changes at once (live) and
// the value comes back off disk (reload).
func TestSidebarSettingsApplyLiveAndPersist(t *testing.T) {
	cases := []struct {
		category string
		label    string
		global   *bool
		read     func(*config.UserConfig) *bool
	}{
		{"Sidebar", "Agents section", &config.SidebarShowAgents, func(c *config.UserConfig) *bool { return c.Appearance.Sidebar.ShowAgents }},
		{"Sidebar", "Marquee", &config.SidebarMarquee, func(c *config.UserConfig) *bool { return c.Appearance.Sidebar.Marquee }},
		{"Dock", "Workspace tabs", &config.DockWorkspaceTabs, func(c *config.UserConfig) *bool { return c.Appearance.DockWorkspaceTabs }},
	}

	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			useTempConfig(t)
			swapBool(t, tc.global, true)
			m := NewOS(OSOptions{UserConfig: config.DefaultConfig()})

			focusSetting(t, m, tc.category, tc.label)
			m.SettingsAdjust(1)

			if *tc.global {
				t.Errorf("%s: the global is still true, so the change never applied live", tc.label)
			}
			reloaded, err := config.LoadUserConfig()
			if err != nil {
				t.Fatalf("reload config: %v", err)
			}
			got := tc.read(reloaded)
			if got == nil {
				t.Fatalf("%s: nothing persisted; the row wrote no config field", tc.label)
			}
			if *got {
				t.Errorf("%s: persisted as true, want false", tc.label)
			}

			// The reload must put the global back where the panel left it, which
			// is what makes an explicit "off" stick across a restart.
			*tc.global = true
			config.ApplyAppearanceConfig(reloaded)
			if *tc.global {
				t.Errorf("%s: reload did not restore the off state", tc.label)
			}
		})
	}
}

// TestOpenSettingsAtSidebar checks the deep link resolves by name, so a tab
// moving in the list cannot send the entry point to the wrong page.
func TestOpenSettingsAtSidebar(t *testing.T) {
	m := NewOS(OSOptions{UserConfig: config.DefaultConfig()})
	m.OpenSettingsAt("Sidebar")

	if !m.ShowSettings {
		t.Fatal("deep link did not open the settings overlay")
	}
	cats := m.settingsCategories()
	if got := cats[m.SettingsCategory].Name; got != "Sidebar" {
		t.Errorf("landed on the %q tab, want Sidebar", got)
	}

	// An unknown name still opens the panel rather than doing nothing.
	m.CloseSettings()
	m.OpenSettingsAt("Nonexistent")
	if !m.ShowSettings {
		t.Error("an unknown category left the overlay closed")
	}
}

// TestRailRightClickOffersSidebarSettings is the deep-link entry point: a
// right-click on blank rail, which routes to no row, offers the settings tab
// the rail is configured from.
func TestRailRightClickOffersSidebarSettings(t *testing.T) {
	m := sidebarTestOS(t, 120, 40, "left")
	sidebarText(t, m)

	// The last usable rail line is below every drawn row, so it hits no row.
	y := m.GetTopMargin() + m.GetUsableHeight() - 1
	if _, ok := m.sidebarRowAt(1, y); ok {
		t.Skip("the rail filled its column; there is no blank area to right-click")
	}
	if !m.SidebarClick(1, y, true) {
		t.Fatal("right-click on blank rail was not consumed")
	}
	cm := m.ContextMenu
	if cm == nil {
		t.Fatal("right-click on blank rail opened no menu")
	}
	found := false
	for _, it := range cm.Items {
		if it.Action == "settings_sidebar" {
			found = true
		}
	}
	if !found {
		t.Error("the blank-rail menu has no row that opens the sidebar settings")
	}
}
