package app

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
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

func TestAChangedRowIsMarkedAndSaysItsDefault(t *testing.T) {
	m := searchOS(t)
	item := focusSetting(t, m, "Behavior", "Confirm quit")
	runSave(t, m.SettingsAdjust(1))
	if !m.settingDiffers(item) {
		t.Fatal("toggling Confirm quit did not mark it changed")
	}
	content, _, _ := m.renderSettings()
	plain := ansi.Strip(content)
	if !strings.Contains(plain, "Confirm quit •") && !strings.Contains(plain, "Confirm quit *") {
		t.Errorf("the changed row carries no mark:\n%s", plain)
	}
	// The description wraps, so its words are compared without the breaks.
	if !strings.Contains(strings.Join(strings.Fields(plain), " "), "Default off, backspace resets.") {
		t.Errorf("the description does not give the default and the key:\n%s", plain)
	}
}

func TestResetAndUndo(t *testing.T) {
	m := searchOS(t)
	const path = "appearance.niri_scroll_cells"
	focusSetting(t, m, "Behavior", "Strip scroll step")
	before := m.optionEffective(path)
	runSave(t, m.SettingsAdjust(1))
	runSave(t, m.SettingsAdjust(1))
	changed := m.optionEffective(path)
	if changed == before {
		t.Fatal("the stepper did not move the step")
	}

	runSave(t, m.SettingsResetSelected())
	if got := m.optionEffective(path); got != before {
		t.Errorf("reset left the step at %s, want the default %s", got, before)
	}
	runSave(t, m.SettingsUndo())
	if got := m.optionEffective(path); got != changed {
		t.Errorf("undoing the reset left the step at %s, want %s", got, changed)
	}
	runSave(t, m.SettingsUndo())
	runSave(t, m.SettingsUndo())
	if got := m.optionEffective(path); got != before {
		t.Errorf("undoing both steps left the step at %s, want %s", got, before)
	}
	n := len(m.Notifications)
	runSave(t, m.SettingsUndo())
	if len(m.Notifications) == n || !strings.Contains(m.Notifications[len(m.Notifications)-1].Message, "Nothing to undo") {
		t.Error("undo with nothing left said nothing")
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

func TestAHandWrittenRowIsNotReset(t *testing.T) {
	m := searchOS(t)
	focusSetting(t, m, "Advanced", "Show keys")
	runSave(t, m.SettingsAdjust(1))
	on := m.ShowKeys
	runSave(t, m.SettingsResetSelected())
	if m.ShowKeys != on {
		t.Error("reset changed a hand-written row it cannot restore whole")
	}
}
