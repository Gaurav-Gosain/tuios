package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestSidebarSettingsApplyLiveAndPersist drives each new row through the panel
// and checks both halves of the contract: this session's setting changes at
// once (live) and the value comes back off disk (reload).
func TestSidebarSettingsApplyLiveAndPersist(t *testing.T) {
	cases := []struct {
		category string
		label    string
		live     func(*OS) *bool
		read     func(*config.UserConfig) *bool
	}{
		{"Sidebar", "File icons", func(m *OS) *bool { return &m.Settings.SidebarFileIcons }, func(c *config.UserConfig) *bool { return c.Appearance.Sidebar.FileIcons }},
		{"Sidebar", "Marquee", func(m *OS) *bool { return &m.Settings.SidebarMarquee }, func(c *config.UserConfig) *bool { return c.Appearance.Sidebar.Marquee }},
		{"Dock", "Workspace tabs", func(m *OS) *bool { return &m.Settings.DockWorkspaceTabs }, func(c *config.UserConfig) *bool { return c.Appearance.DockWorkspaceTabs }},
	}

	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			useTempConfig(t)
			m := NewOS(OSOptions{UserConfig: config.DefaultConfig()})
			*tc.live(m) = true

			focusSetting(t, m, tc.category, tc.label)
			runSave(t, m.SettingsAdjust(1))

			if *tc.live(m) {
				t.Errorf("%s: the session still reads true, so the change never applied live", tc.label)
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

			// The reload must put the session back where the panel left it,
			// which is what makes an explicit "off" stick across a restart.
			*tc.live(m) = true
			config.ApplyAppearanceConfig(reloaded, &m.Settings)
			if *tc.live(m) {
				t.Errorf("%s: reload did not restore the off state", tc.label)
			}
		})
	}
}
