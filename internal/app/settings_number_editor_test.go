package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// Enter on a numeric row used to step it by one, which is what the arrow keys
// already did and no way at all to reach a number at the other end of a wide
// range: getting a scrollback from 300 to 20000 meant holding a key. The row
// opens into an editor instead, where the number can be typed or slid to.

// numberRowOS is the settings page open on a bounded numeric row.
func numberRowOS(t *testing.T, path string) *OS {
	t.Helper()
	m := &OS{Settings: config.Global, Width: 120, Height: 40}
	m.ShowSettings = true
	for ci, cat := range m.settingsCategories() {
		for ii, item := range cat.Items {
			if item.Path == path {
				m.SettingsCategory = ci
				m.SettingsSelected = ii
				return m
			}
		}
	}
	t.Fatalf("no row for %s", path)
	return nil
}

// TestATypedNumberIsClampedNotRefused pins what happens past the ends: the
// value is held to the range rather than written out of it.
func TestATypedNumberIsClampedNotRefused(t *testing.T) {
	m := numberRowOS(t, "appearance.zoom_size")
	item, _ := m.settingsSelectedItem()

	m.SettingsActivate()
	m.SettingsEditClear()
	for _, r := range "999" {
		m.SettingsEditAppend(string(r))
	}
	m.SettingsEditCommit()

	if got, want := settingsNumberOf(item.value(m)), item.numMax; got != want {
		t.Errorf("999 landed as %d, want the ceiling %d", got, want)
	}
}

// TestRubbishLeavesTheSettingAlone. Writing a zero for a typo is a keystroke
// that throws away a scrollback of ten thousand lines.
func TestRubbishLeavesTheSettingAlone(t *testing.T) {
	m := numberRowOS(t, "appearance.scrollback_lines")
	item, _ := m.settingsSelectedItem()
	before := item.value(m)

	m.SettingsActivate()
	m.SettingsEditClear()
	m.SettingsEditCommit()

	if got := item.value(m); got != before {
		t.Errorf("an empty field wrote %s over %s", got, before)
	}
}

// TestEveryNumericRowOpensAnEditor pins that no bounded numeric row was left
// behind with only a stepper.
func TestEveryNumericRowOpensAnEditor(t *testing.T) {
	m := &OS{Settings: config.Global, Width: 120, Height: 40}
	for _, cat := range m.settingsCategories() {
		for _, item := range cat.Items {
			if item.Control != controlInt {
				continue
			}
			if item.setNum == nil {
				t.Errorf("%s/%s cannot be written to, so Enter still only steps it", cat.Name, item.Label)
				continue
			}
			if item.numMax <= item.numMin {
				t.Errorf("%s/%s has no range (%d to %d), so its editor has nothing to validate against",
					cat.Name, item.Label, item.numMin, item.numMax)
			}
		}
	}
}
