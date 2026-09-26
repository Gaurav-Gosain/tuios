package app

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

func searchOS(t *testing.T) *OS {
	t.Helper()
	useTempConfig(t)
	// The session's pane geometry is seeded from the config the way the
	// factory seeds it, so the rows that show it read the default too.
	m := &OS{
		Settings: config.Global, Width: 120, Height: 44, UserConfig: config.DefaultConfig(),
		MasterRatio: config.Global.MasterRatioFraction(), ScrollColumnWidth: config.Global.ScrollColumnWidth,
		SharedBorders: config.Global.SharedBorders, PaneGap: config.Global.PaneGap,
	}
	m.OpenSettings()
	return m
}

// searchFor opens the search line with query and returns the rows found.
func searchFor(m *OS, query string) ([]settingItem, []settingsHit) {
	m.SettingsSearchStart("")
	m.SettingsSearchSetQuery(query)
	return m.settingsSearchRows(m.settingsCategories())
}

// TestSettingsSearchRowsAreClickable: the rows are drawn one line lower while
// the search line is up, and the hit rects move with them.
func TestSettingsSearchRowsAreClickable(t *testing.T) {
	m := searchOS(t)
	searchFor(m, "gap")
	content, geo, rows := m.renderSettings()
	if len(rows) == 0 {
		t.Fatal("no hit rows")
	}
	lines := strings.Split(ansi.Strip(content), "\n")
	items := m.settingsCurrentItems()
	for _, r := range rows {
		line := lines[r.Rect.Y0]
		if !strings.Contains(line, items[r.Idx].Label) {
			t.Errorf("hit row %d at y=%d is over %q, not %q", r.Idx, r.Rect.Y0, line, items[r.Idx].Label)
		}
	}
	_ = geo
}
