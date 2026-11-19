package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestProfileFromSettingsAndThePalette: the Settings row turns the Simple
// controls on, and the palette row turns them off. Each change writes the
// config file and applies at once.
//
// Negative control: make the row's activate return nil. The config never
// gets profile = "simple".
func TestProfileFromSettingsAndThePalette(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[appearance]\n")
	term := startIn(t, base, startOpts{})
	waitBoot(t, term)

	if err := term.SendKeys(","); err != nil {
		t.Fatalf("open settings: %v", err)
	}
	if err := term.WaitForText("Focused border color", uiTimeout); err != nil {
		t.Fatalf("the settings page never opened: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("/", "simple controls"); err != nil {
		t.Fatalf("search: %v", err)
	}
	if err := term.WaitForText("simple controls", uiTimeout); err != nil {
		t.Fatalf("the search never showed the whole query: %v\n%s", err, term.Snapshot())
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		row := selectedSettingsRow(s, "Simple controls")
		return row != "" && strings.Contains(row, "Behavior") && strings.Contains(row, "off")
	}, uiTimeout); err != nil {
		t.Fatalf("the search did not find Simple controls, off, on the Behavior tab: %v\n%s", err, term.Snapshot())
	}
	welcomeShot(t, term, "profile-settings-row")
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("enter: %v", err)
	}
	waitConfig(t, term, base, "profile = \"simple\"")
	if err := term.SendKeys(tuitest.Esc, tuitest.Esc); err != nil {
		t.Fatalf("close settings: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		_, rows := s.Size()
		return strings.Contains(s.Line(rows-1), "Menu") && !strings.Contains(s.Text(), "Focused border color")
	}, uiTimeout); err != nil {
		t.Fatalf("the Simple dock never came up: %v\n%s", err, term.Snapshot())
	}

	if err := term.SendKeys(tuitest.Ctrl('p')); err != nil {
		t.Fatalf("open the palette: %v", err)
	}
	if err := term.SendKeys("keyboard controls"); err != nil {
		t.Fatalf("type: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "› keyboard controls", "› Use keyboard controls")
	}, uiTimeout); err != nil {
		t.Fatalf("the palette did not narrow to the keyboard controls row: %v\n%s", err, term.Snapshot())
	}
	welcomeShot(t, term, "profile-palette-row")
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("enter: %v", err)
	}
	if err := term.WaitFor(func(tuitest.Screen) bool {
		return !strings.Contains(configText(t, base), "profile")
	}, uiTimeout); err != nil {
		t.Fatalf("the palette row did not take the profile out:\n%s", configText(t, base))
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		_, rows := s.Size()
		return !strings.Contains(s.Line(rows-1), "Menu")
	}, uiTimeout); err != nil {
		t.Fatalf("the Simple dock stayed after keyboard controls: %v\n%s", err, term.Snapshot())
	}
}
