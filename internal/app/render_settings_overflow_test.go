package app

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
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

// TestSettingsDescriptionWrapsWithinThePanel confirms the longest description in
// the set is laid across the box's two lines rather than cut at the first, and
// that both lines stay inside the panel.
func TestSettingsDescriptionWrapsWithinThePanel(t *testing.T) {
	m := NewOS(OSOptions{UserConfig: config.DefaultConfig()})
	m.Width, m.Height = 120, 40
	m.EffectiveWidth, m.EffectiveHeight = 120, 40
	m.ShowSettings = true
	ci, ii, item, ok := findSetting(m, "Advanced", "Word characters")
	if !ok {
		t.Fatal("Word characters setting not found")
	}
	m.SettingsCategory = ci
	m.SettingsSelected = ii

	if lipgloss.Width(item.Desc) <= settingsMaxInnerWidth-2 {
		t.Fatalf("fixture description %q fits on one line; the test would not exercise wrapping", item.Desc)
	}

	content, geo, _ := m.renderSettings()
	lines := strings.Split(content, "\n")
	var box []string
	for i, ln := range lines {
		if strings.Contains(ln, "Punctuation that counts") {
			box = lines[i:min(i+settingsDescLines, len(lines))]
			break
		}
	}
	if len(box) < settingsDescLines {
		t.Fatalf("could not find the description box in rendered content:\n%s", content)
	}
	for i, ln := range box {
		if w := lipgloss.Width(ln); w != geo.Width {
			t.Errorf("description line %d width = %d, want %d\nline: %q", i, w, geo.Width, ln)
		}
	}
	// The tail of the sentence made it onto the second line instead of being
	// dropped behind an ellipsis on the first.
	if !strings.Contains(box[1], "@-./") {
		t.Errorf("the description did not wrap onto its second line: %q", box[1])
	}
	if strings.Contains(box[0], "…") {
		t.Errorf("the first line was truncated even though a second was free: %q", box[0])
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

// TestSettingsDescriptionBoxHoldsItsHeight: the box is padded to a fixed number
// of lines, so moving the selection from a one-line description to a two-line
// one must not change the panel's height. A panel that grows re-centres itself,
// which moves every row out from under the pointer.
func TestSettingsDescriptionBoxHoldsItsHeight(t *testing.T) {
	m := newNarrowOS(t, 120, 40)
	ci, ii, _, ok := findSetting(m, "Advanced", "Word characters")
	if !ok {
		t.Fatal("Word characters setting not found")
	}
	m.SettingsCategory = ci

	heights := map[int]int{}
	cat := m.settingsCategories()[ci]
	for i := range cat.Items {
		m.SettingsSelected = i
		content, _, _ := m.renderSettings()
		heights[lipgloss.Height(content)]++
	}
	if len(heights) != 1 {
		t.Errorf("panel height varies with the selected row: %v (the long one is row %d)", heights, ii)
	}
}

// TestSettingsDescriptionTruncatesBeyondTheBox: a description too long even for
// the box is cut with a mark rather than silently dropped, so the user can tell
// there is more.
func TestSettingsDescriptionTruncatesBeyondTheBox(t *testing.T) {
	pal := overlay.Palette{}
	long := strings.Repeat("word ", 100)
	out := settingsDescription(long, 40, settingsDescLines, pal)
	if len(out) != settingsDescLines {
		t.Fatalf("box is %d lines, want %d", len(out), settingsDescLines)
	}
	last := ansiEscape.ReplaceAllString(out[len(out)-1], "")
	if !strings.Contains(last, "…") && !strings.Contains(last, "...") {
		t.Errorf("an over-long description lost its truncation mark: %q", last)
	}
	for i, ln := range out {
		if w := lipgloss.Width(ln); w > 40 {
			t.Errorf("line %d is %d cells, inner width is 40: %q", i, w, ln)
		}
	}
}
