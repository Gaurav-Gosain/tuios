package app

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestSettingsTellsTheTruthAboutUnsetValues is the falsehood the audit caught:
// a text row drew its own placeholder example in the value's style, so the panel
// stated a value the user had never set.
//
// An unset field now says it is unset, in the row's own terms, and the example
// moves to the description line where an example belongs.
//
// It is checked on the preferred shell because the two border colours it used to
// be checked on are no longer text fields; the colour rows have their own
// version of this below. The shell row also reads its value off the held config
// rather than a package global, so the assertion does not depend on what any
// other test in the binary left behind.
func TestSettingsTellsTheTruthAboutUnsetValues(t *testing.T) {
	m := &OS{Width: 120, Height: 40, UserConfig: config.DefaultConfig()}
	m.ShowSettings = true

	row := settingsRowNamed(t, m, "Preferred shell")

	line := settingsRowLine(t, m, row.Label)
	if strings.Contains(line, "[ "+row.Placeholder+" ]") {
		t.Errorf("the panel drew the placeholder %q as the value in force: %q", row.Placeholder, line)
	}
	if !strings.Contains(line, "[ "+row.Unset+" ]") {
		t.Errorf("an unset value did not read as unset (%q): %q", row.Unset, line)
	}

	// A value that is set renders as itself.
	m.UserConfig.Appearance.PreferredShell = "/bin/zsh"
	line = settingsRowLine(t, m, row.Label)
	if !strings.Contains(line, "[ /bin/zsh ]") {
		t.Errorf("a set value did not render itself: %q", line)
	}
	if strings.Contains(line, "[ "+row.Unset+" ]") {
		t.Errorf("a set value still read as unset: %q", line)
	}
}

// TestColourRowFramesItsValueBesideItsSwatch is the colour rows' version of the
// same truth. The value sits inside the same bracketed field every other row
// uses, with the swatch of the colour in force in front of it, so an unset row
// says where its colour comes from and still shows what that colour is.
func TestColourRowFramesItsValueBesideItsSwatch(t *testing.T) {
	m := &OS{Width: 120, Height: 40, UserConfig: config.DefaultConfig()}
	m.ShowSettings = true
	row := settingsRowNamed(t, m, "Focused border color")

	line := settingsRowLine(t, m, row.Label)
	if !strings.Contains(line, row.Unset+" ]") {
		t.Errorf("an unset colour did not read as unset (%q): %q", row.Unset, line)
	}

	m.UserConfig.Appearance.BorderFocusedColor = "#ff0000"
	line = settingsRowLine(t, m, row.Label)
	if !strings.Contains(line, "#ff0000 ]") {
		t.Errorf("a set colour did not render its value: %q", line)
	}
	if strings.Contains(line, row.Unset) {
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
	row := settingsRowNamed(t, m, "Preferred shell")
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
