package app

import (
	"strings"
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

// TestEnterOpensTheNumberEditor pins the report: Enter opens an editor rather
// than nudging the value.
func TestEnterOpensTheNumberEditor(t *testing.T) {
	m := numberRowOS(t, "appearance.scrollback_lines")
	item, _ := m.settingsSelectedItem()
	before := item.value(m)

	m.SettingsActivate()

	if !m.SettingsEditing {
		t.Fatal("Enter did not open an editor")
	}
	if !m.SettingsEditingNumber() {
		t.Error("the editor does not know it is editing a number")
	}
	if got := item.value(m); got != before {
		t.Errorf("Enter changed the value from %s to %s instead of opening an editor", before, got)
	}
	// The buffer starts at the value, so Enter twice is a no-op rather than a
	// surprise.
	if m.SettingsEditBuffer != strings.TrimSuffix(before, "%") {
		t.Errorf("the editor opened with %q, want the value %q", m.SettingsEditBuffer, before)
	}
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

// TestTheSliderMovesTheNumber is the slider half, and that it edits the same
// buffer the digits do so Esc abandons both together.
func TestTheSliderMovesTheNumber(t *testing.T) {
	m := numberRowOS(t, "appearance.zoom_size")
	item, _ := m.settingsSelectedItem()
	before := settingsNumberOf(item.value(m))

	m.SettingsActivate()
	m.SettingsEditNumberSlide(-1)
	slid, ok := m.SettingsEditNumberValue()
	if !ok {
		t.Fatal("the slider left the buffer holding something that is not a number")
	}
	if slid >= before {
		t.Errorf("sliding down gave %d, want less than %d", slid, before)
	}
	// Still only in the buffer: the setting has not moved until Enter.
	if got := settingsNumberOf(item.value(m)); got != before {
		t.Errorf("the slider wrote %d to the setting before it was committed", got)
	}

	m.SettingsEditCancel()
	if got := settingsNumberOf(item.value(m)); got != before {
		t.Errorf("Esc left the setting at %d, want the %d it started at", got, before)
	}
}

// TestTheSliderCrossesAWideRangeInReasonableSteps. A range of a million stepped
// by one is a slider nobody can use.
func TestTheSliderCrossesAWideRangeInReasonableSteps(t *testing.T) {
	m := numberRowOS(t, "appearance.scrollback_lines")
	m.SettingsActivate()
	item, _ := m.settingsSelectedItem()
	start, _ := m.SettingsEditNumberValue()

	m.SettingsEditNumberSlide(1)
	after, _ := m.SettingsEditNumberValue()

	span := item.numMax - item.numMin
	if step := after - start; step <= 1 || step > span/10 {
		t.Errorf("one slide moved %d over a range of %d", step, span)
	}
}

// TestASmallRangeStillStepsByOne, because a range of eight crossed in
// hundredths would never move at all.
func TestASmallRangeStillStepsByOne(t *testing.T) {
	m := numberRowOS(t, "appearance.gap")
	m.SettingsActivate()
	start, _ := m.SettingsEditNumberValue()
	m.SettingsEditNumberSlide(1)
	after, _ := m.SettingsEditNumberValue()
	if after-start != 1 {
		t.Errorf("one slide moved %d on a range of eight, want 1", after-start)
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
