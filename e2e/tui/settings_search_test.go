package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// selectedSettingsRow is the settings row the cursor is on: the one line of
// the panel carrying the selection sigil ahead of a label.
func selectedSettingsRow(s tuitest.Screen, labels ...string) string {
	_, rows := s.Size()
	for row := range rows {
		line := s.Line(row)
		if !strings.Contains(line, "›") {
			continue
		}
		for _, l := range labels {
			if strings.Contains(line, "› "+l) {
				return line
			}
		}
	}
	return ""
}

// TestSettingsSearchChangesARowAndListsWrap drives the settings page the way a
// person does: up on the first row lands on the last, a search finds a row on
// another tab, enter changes that row where it stands, and esc backs out of
// the search and then out of the page.
func TestSettingsSearchChangesARowAndListsWrap(t *testing.T) {
	term, _ := start(t, startOpts{})
	waitBoot(t, term)
	newWindow(t, term)
	openSettings(t, term)

	// The Appearance tab opens on its first row, Theme. Up wraps to its last.
	if err := term.SendKeys(tuitest.Up); err != nil {
		t.Fatalf("up: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return selectedSettingsRow(s, "Session border") != ""
	}, uiTimeout); err != nil {
		t.Fatalf("up on the first row did not go to the last row, Session border: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Down); err != nil {
		t.Fatalf("down: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return selectedSettingsRow(s, "Theme") != ""
	}, uiTimeout); err != nil {
		t.Fatalf("down on the last row did not go back to the first, Theme: %v\n%s", err, term.Snapshot())
	}

	// Confirm quit lives on the Behavior tab; the search finds it from here.
	if err := term.SendKeys("/", "confirm quit"); err != nil {
		t.Fatalf("search: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		row := selectedSettingsRow(s, "Confirm quit")
		return row != "" && strings.Contains(row, "Behavior") && strings.Contains(row, "off")
	}, uiTimeout); err != nil {
		t.Fatalf("the search did not put Confirm quit, on the Behavior tab and off, under the cursor: %v\n%s", err, term.Snapshot())
	}

	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("enter: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		row := selectedSettingsRow(s, "Confirm quit")
		return row != "" && strings.Contains(row, "on ]") && strings.Contains(s.Text(), "confirm quit")
	}, uiTimeout); err != nil {
		t.Fatalf("enter on the result did not turn Confirm quit on in place: %v\n%s", err, term.Snapshot())
	}

	// esc clears the search and leaves the page up; the next esc closes it.
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("esc: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "confirm quit") && strings.Contains(s.Text(), "Focused border color")
	}, uiTimeout); err != nil {
		t.Fatalf("the first esc did not clear the search back to the Appearance tab: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("esc: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "Focused border color")
	}, uiTimeout); err != nil {
		t.Fatalf("the second esc did not close the page: %v\n%s", err, term.Snapshot())
	}

	// The change was made to the real setting: its own tab shows it. Tab in
	// the search goes to the row where it lives.
	if err := term.SendKeys(",", "/", "confirm quit", tuitest.Tab); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		row := findRow(s, "Confirm quit")
		return row >= 0 && strings.Contains(s.Line(row), "on ]") &&
			!strings.Contains(s.Text(), "confirm quit") && strings.Contains(s.Text(), "Which-key")
	}, uiTimeout); err != nil {
		t.Fatalf("the Behavior tab does not show Confirm quit on: %v\n%s", err, term.Snapshot())
	}
}
