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

// TestSettingsDescriptionTruncates confirms a description longer than the
// panel's inner width is shortened with an ellipsis rather than left to
// overflow past the panel edge.
func TestSettingsDescriptionTruncates(t *testing.T) {
	m := NewOS(OSOptions{UserConfig: config.DefaultConfig()})
	m.ShowSettings = true
	ci, ii, item, ok := findSetting(m, "Behavior", "Preferred shell")
	if !ok {
		t.Fatal("Preferred shell setting not found")
	}
	m.SettingsCategory = ci
	m.SettingsSelected = ii

	if lipgloss.Width(item.Desc) <= settingsInnerWidth-2 {
		t.Fatalf("fixture description %q is not longer than the truncation budget; test would not exercise truncation", item.Desc)
	}

	content, geo, _ := m.renderSettings()
	lines := strings.Split(content, "\n")
	var descLine string
	for _, ln := range lines {
		if strings.Contains(ln, "Shell for new windows") {
			descLine = ln
			break
		}
	}
	if descLine == "" {
		t.Fatalf("could not find the description line in rendered content:\n%s", content)
	}
	if w := lipgloss.Width(descLine); w != geo.Width {
		t.Errorf("description line width = %d, want %d\nline: %q", w, geo.Width, descLine)
	}
	if !strings.Contains(descLine, "…") && !strings.Contains(descLine, "...") {
		t.Errorf("expected truncated description to carry an ellipsis, got %q", descLine)
	}
}
