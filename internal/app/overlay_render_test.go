package app

import (
	"strconv"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// assertSolidRect fails if any line has a different display width than the
// first, which would mean a ragged fill or a transparent hole in the panel.
func assertSolidRect(t *testing.T, name, out string) {
	t.Helper()
	lines := strings.Split(out, "\n")
	if len(lines) == 0 {
		t.Fatalf("%s: empty output", name)
	}
	width := lipgloss.Width(lines[0])
	for i, ln := range lines {
		if w := lipgloss.Width(ln); w != width {
			t.Errorf("%s: line %d width %d != panel width %d (ragged fill): %q", name, i, w, width, ln)
		}
	}
}

// TestSettingsPanelRendersSolid renders the real settings overlay for every
// category and checks each is a solid rectangle with sane hit geometry.
func TestSettingsPanelRendersSolid(t *testing.T) {
	m := &OS{Settings: config.Global}
	m.SettingsSelected = 1
	out, geo, rows := m.renderSettings()
	assertSolidRect(t, "settings", out)
	if geo.BodyY == 0 || len(rows) == 0 {
		t.Errorf("expected settings hit geometry, got bodyY=%d rows=%d", geo.BodyY, len(rows))
	}

	for i := range 4 {
		m.SettingsCategory = i
		m.SettingsSelected = 0
		s, _, _ := m.renderSettings()
		assertSolidRect(t, "settings cat "+strconv.Itoa(i), s)
	}
}

// TestHelpPanelRenders renders the real help overlay (category and search
// modes) and checks it is a solid rectangle.
func TestHelpPanelRenders(t *testing.T) {
	m := &OS{Settings: config.Global, KeybindRegistry: config.NewKeybindRegistry(config.DefaultConfig())}
	m.HelpCategory = -1
	out, geo := m.RenderHelpMenu()
	assertSolidRect(t, "help", out)
	if len(geo.Tabs) == 0 {
		t.Errorf("expected help tab geometry")
	}

	m.HelpSearchMode = true
	m.HelpSearchQuery = "window"
	s, _ := m.RenderHelpMenu()
	assertSolidRect(t, "help search", s)
}
