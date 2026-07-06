package app

import (
	"strconv"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
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

// TestOverlayPanelRendersSolid renders a sample panel through the overlay
// package and asserts it is a gap-free solid rectangle.
func TestOverlayPanelRendersSolid(t *testing.T) {
	pal := theme.UI()
	bg := pal.Surface

	rows := []string{
		overlay.KeyBadges([]string{"n"}, bg, pal) + overlay.Style(bg).Render("   ") + overlay.Style(bg).Foreground(pal.Fg).Render("New window"),
		overlay.Style(bg).Foreground(pal.FgDim).Render("Theme") + overlay.Style(bg).Render("   ") + overlay.Cycler("tokyonight", true, bg, pal),
		overlay.Style(bg).Foreground(pal.FgDim).Render("Shared borders") + overlay.Style(bg).Render("   ") + overlay.Toggle(true, false, bg, pal),
	}

	p := overlay.Panel{
		Title:     "Sample Panel",
		Width:     60,
		Tabs:      []string{"Windows", "Layout", "Modes"},
		ActiveTab: 0,
		Body:      strings.Join(rows, "\n"),
		Hints:     []overlay.Hint{{Key: "↑↓", Label: "move"}, {Key: "esc", Label: "close"}},
	}
	out, geo := p.Render(pal)
	if geo.Width == 0 || len(geo.Tabs) != 3 {
		t.Errorf("unexpected geometry: width=%d tabs=%d", geo.Width, len(geo.Tabs))
	}
	assertSolidRect(t, "sample", out)
}

// TestSettingsPanelRendersSolid renders the real settings overlay for every
// category and checks each is a solid rectangle with sane hit geometry.
func TestSettingsPanelRendersSolid(t *testing.T) {
	m := &OS{}
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

// TestThemePickerRenders renders the theme picker (with swatches) and checks it
// is a solid rectangle with per-row hit geometry.
func TestThemePickerRenders(t *testing.T) {
	m := &OS{}
	m.OpenThemePicker()
	out, geo, rows := m.renderThemePicker()
	t.Logf("\n%s", out)
	assertSolidRect(t, "themepicker", out)
	if geo.Width == 0 || len(rows) == 0 {
		t.Errorf("expected theme picker geometry+rows, got width=%d rows=%d", geo.Width, len(rows))
	}
}

// TestHelpPanelRenders renders the real help overlay (category and search
// modes) and checks it is a solid rectangle.
func TestHelpPanelRenders(t *testing.T) {
	m := &OS{KeybindRegistry: config.NewKeybindRegistry(config.DefaultConfig())}
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
