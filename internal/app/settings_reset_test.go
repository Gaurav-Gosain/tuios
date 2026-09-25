package app

import (
	"testing"
)

// TestADefaultConfigHasNoChangedRows: with nothing changed, no row may carry
// the changed mark, or the mark means nothing.
func TestADefaultConfigHasNoChangedRows(t *testing.T) {
	m := searchOS(t)
	for _, cat := range m.settingsCategories() {
		for _, item := range cat.Items {
			if m.settingDiffers(item) {
				t.Errorf("%s / %s (%s) reads as changed on a default config: value %q", cat.Name, item.Label, item.Path, m.optionEffective(item.Path))
			}
		}
	}
}

// TestResetAnEnumWhoseDefaultIsEmpty: a few enums default to the empty string
// without accepting it; resetting one writes the built-in.
func TestResetAnEnumWhoseDefaultIsEmpty(t *testing.T) {
	m := searchOS(t)
	item := focusSetting(t, m, "Behavior", "Which-key position")
	runSave(t, m.SettingsAdjust(1))
	if !m.settingDiffers(item) {
		t.Skip("stepping the which-key position did not change it; nothing to reset")
	}
	runSave(t, m.SettingsResetSelected())
	if m.settingDiffers(item) {
		t.Errorf("reset left Which-key position at %q", m.optionEffective(item.Path))
	}
}
