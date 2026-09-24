package app

import (
	"strings"
	"testing"
)

func paletteSettingsOS(t *testing.T) *OS {
	t.Helper()
	m := searchOS(t)
	m.CloseSettings()
	m.PaletteSessionItems = nil
	m.PaletteKeybindItems = nil
	m.PaletteSettingItems = getSettingPaletteItems(m)
	m.rebuildPaletteItems()
	return m
}

// TestPaletteReachesASettingByName: "settings: pane background" puts the
// row first, and running it opens the page on that row.
func TestPaletteReachesASettingByName(t *testing.T) {
	m := paletteSettingsOS(t)
	for _, q := range []string{"settings: pane background", "pane background"} {
		got := FilterCommandPalette(m.allPaletteItems(), q)
		if len(got) == 0 || got[0].Name != "Settings: Pane background" {
			names := []string{}
			for _, it := range got[:min(len(got), 5)] {
				names = append(names, it.Name)
			}
			t.Fatalf("%q ranked %v first, want Settings: Pane background", q, names)
		}
		if got[0].Shortcut != "Backgrounds" {
			t.Errorf("the row names tab %q, want Backgrounds", got[0].Shortcut)
		}
	}

	got := FilterCommandPalette(m.allPaletteItems(), "pane background")
	m2, _ := got[0].Action(m)
	cats := m2.settingsCategories()
	if !m2.ShowSettings || cats[m2.SettingsCategory].Items[m2.SettingsSelected].Label != "Pane background" {
		t.Errorf("running the row opened settings=%v on %q", m2.ShowSettings,
			cats[m2.SettingsCategory].Items[m2.SettingsSelected].Label)
	}
}

// TestPaletteSettingRowsStayOutOfTheWay: none in the empty palette, and none
// ahead of a command that matches.
func TestPaletteSettingRowsStayOutOfTheWay(t *testing.T) {
	m := paletteSettingsOS(t)
	for _, it := range FilterCommandPalette(m.allPaletteItems(), "") {
		if it.Setting {
			t.Fatalf("the empty palette lists %q", it.Name)
		}
	}
	got := FilterCommandPalette(m.allPaletteItems(), "theme")
	seen := false
	for _, it := range got {
		if it.Setting {
			seen = true
		} else if seen {
			t.Fatalf("command %q ranks below a setting row", it.Name)
		}
	}
	if !seen {
		t.Error("theme reached no setting row")
	}
	if n := paletteReachable(m.allPaletteItems(), ""); n != len(FilterCommandPalette(m.allPaletteItems(), "")) {
		t.Errorf("the empty palette counts %d reachable rows, but lists %d", n, len(FilterCommandPalette(m.allPaletteItems(), "")))
	}
	for _, it := range FilterCommandPalette(m.allPaletteItems(), "#theme") {
		if strings.HasPrefix(it.Name, "Settings: ") {
			t.Errorf("the keybind search lists setting row %q", it.Name)
		}
	}
}
