package app

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestSettingsPanelLinesMatchGeometryWidth guards against any settings row
// (description, value field, or control) rendering wider than the panel's
// own declared geometry. Every line the panel emits must be exactly
// geo.Width cells, matching the invariant overlay.Panel already upholds for
// its own generic rows (see overlay.TestPanelGeometry).
func TestSettingsPanelLinesMatchGeometryWidth(t *testing.T) {
	cases := []struct {
		name    string
		desc    string
		shell   string
		editBuf string
	}{
		{
			name: "real preferred-shell description",
			desc: "Shell for new windows, empty = auto-detect (applies to new windows)",
		},
		{
			name:  "long shell path value",
			shell: "/usr/local/very/long/path/to/some/custom/shell/binary/that/keeps/going/and/going",
		},
		{
			name:    "long value while editing",
			shell:   "/bin/bash",
			editBuf: "/usr/local/very/long/path/to/some/custom/shell/binary/that/keeps/going/and/going",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.DefaultConfig()
			if tc.shell != "" {
				cfg.Appearance.PreferredShell = tc.shell
			}
			m := NewOS(OSOptions{UserConfig: cfg})
			m.ShowSettings = true
			ci, ii, _, ok := findSetting(m, "Behavior", "Preferred shell")
			if !ok {
				t.Fatal("Preferred shell setting not found")
			}
			m.SettingsCategory = ci
			m.SettingsSelected = ii
			if tc.editBuf != "" {
				m.SettingsEditing = true
				m.SettingsEditBuffer = tc.editBuf
			}

			content, geo, _ := m.renderSettings()
			for i, ln := range strings.Split(content, "\n") {
				if w := lipgloss.Width(ln); w != geo.Width {
					t.Errorf("line %d width = %d, want geo.Width = %d\nline: %q", i, w, geo.Width, ln)
				}
			}
		})
	}
}

// TestSettingsDescriptionStaysInsideThePanelAtEveryWidth renders every setting
// at every width the panel can be drawn at and requires the description box to
// end where the panel ends. A description that overruns paints over whatever is
// behind the overlay, which is the failure this pass was opened for.
func TestSettingsDescriptionStaysInsideThePanelAtEveryWidth(t *testing.T) {
	for w := 16; w <= 200; w++ {
		m := newNarrowOS(t, w, 40)
		for ci, cat := range m.settingsCategories() {
			for ii := range cat.Items {
				m.SettingsCategory, m.SettingsSelected = ci, ii
				content, geo, _ := m.renderSettings()
				for i, ln := range strings.Split(content, "\n") {
					if lw := lipgloss.Width(ln); lw != geo.Width {
						t.Fatalf("w=%d %s[%d]: line %d is %d cells, panel is %d: %q",
							w, cat.Name, ii, i, lw, geo.Width, ln)
					}
				}
			}
		}
	}
}
