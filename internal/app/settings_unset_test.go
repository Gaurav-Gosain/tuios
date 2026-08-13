package app

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestSettingsTellsTheTruthAboutUnsetValues is the falsehood the audit caught:
// "Focused border color [ #89b4fa ]" drew the row's own placeholder example in
// the value's style, so the panel stated a colour the user had never set and
// which was not the one on screen.
//
// An unset field now says it is unset, in the row's own terms, and the example
// moves to the description line where an example belongs.
func TestSettingsTellsTheTruthAboutUnsetValues(t *testing.T) {
	m := &OS{Width: 120, Height: 40, UserConfig: config.DefaultConfig()}
	m.ShowSettings = true

	// Find the colour row's category and put the selection on it.
	row := settingsRowNamed(t, m, "Focused border color")

	line := settingsRowLine(t, m, row.Label)
	if strings.Contains(line, "[ "+row.Placeholder+" ]") {
		t.Errorf("the panel drew the placeholder %q as the value in force: %q", row.Placeholder, line)
	}
	if !strings.Contains(line, "[ "+row.Unset+" ]") {
		t.Errorf("an unset colour did not read as unset (%q): %q", row.Unset, line)
	}

	// A value that is set renders as itself.
	m.UserConfig.Appearance.BorderFocusedColor = "#ff0000"
	line = settingsRowLine(t, m, row.Label)
	if !strings.Contains(line, "[ #ff0000 ]") {
		t.Errorf("a set colour did not render its value: %q", line)
	}
	if strings.Contains(line, "[ "+row.Unset+" ]") {
		t.Errorf("a set colour still read as unset: %q", line)
	}
}

// settingsRowLine is the drawn row carrying the given label.
func settingsRowLine(t *testing.T, m *OS, label string) string {
	t.Helper()
	content, _, _ := m.renderSettings()
	for line := range strings.SplitSeq(ansi.Strip(content), "\n") {
		if strings.Contains(line, label) {
			return line
		}
	}
	t.Fatalf("the settings panel drew no row for %q", label)
	return ""
}

// TestSettingsExamplesLiveOnTheDescriptionLine checks the example did not just
// disappear: it is still there to copy, one line down.
func TestSettingsExamplesLiveOnTheDescriptionLine(t *testing.T) {
	m := &OS{Width: 120, Height: 40, UserConfig: config.DefaultConfig()}
	m.ShowSettings = true
	row := settingsRowNamed(t, m, "Focused border color")
	if !strings.Contains(row.Desc, row.Placeholder) {
		t.Errorf("the description %q does not carry the example %q", row.Desc, row.Placeholder)
	}
}

// settingsRowNamed selects the named row and returns it, so a test can assert
// against the row the panel is actually drawing.
func settingsRowNamed(t *testing.T, m *OS, label string) settingItem {
	t.Helper()
	for ci, cat := range m.settingsCategories() {
		for ii, item := range cat.Items {
			if item.Label == label {
				m.SettingsCategory, m.SettingsSelected = ci, ii
				return item
			}
		}
	}
	t.Fatalf("no settings row named %q", label)
	return settingItem{}
}
