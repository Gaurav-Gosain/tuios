package app

import "testing"

// TestSettingsReopensWhereItWasLeft: closing and reopening the page keeps the
// tab, the row and an open search.
func TestSettingsReopensWhereItWasLeft(t *testing.T) {
	m := searchOS(t)
	m.SettingsSetCategory(4)
	m.SettingsMove(3)
	m.CloseSettings()
	m.OpenSettings()
	if m.SettingsCategory != 4 || m.SettingsSelected != 3 {
		t.Errorf("reopened on tab %d row %d, want tab 4 row 3", m.SettingsCategory, m.SettingsSelected)
	}

	searchFor(m, "gap")
	m.CloseSettings()
	m.OpenSettings()
	if !m.SettingsSearchOpen() || m.SettingsSearchQuery() != "gap" || len(m.settingsSearch.hits) == 0 {
		t.Errorf("reopened with search=%v query=%q hits=%d, want the gap search back",
			m.SettingsSearchOpen(), m.SettingsSearchQuery(), len(m.settingsSearch.hits))
	}

	// An entry point that names a tab wins over the remembered place.
	m.OpenSettingsAt("Dock")
	if m.SettingsSearchOpen() || m.settingsCategories()[m.SettingsCategory].Name != "Dock" || m.SettingsSelected != 0 {
		t.Errorf("OpenSettingsAt(Dock) left search=%v tab=%d row=%d", m.SettingsSearchOpen(), m.SettingsCategory, m.SettingsSelected)
	}
}
