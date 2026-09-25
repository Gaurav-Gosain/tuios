package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

func typeKeys(o *app.OS, s string) *app.OS {
	for _, r := range s {
		o, _ = handleSettingsInput(press(string(r)), o)
	}
	return o
}

// TestSettingsBackspaceInTheSearchNeverResets: backspace edits the query, and
// running the query out keeps the search open rather than falling through to
// the tab view, where backspace resets a row.
func TestSettingsBackspaceInTheSearchNeverResets(t *testing.T) {
	o := listKeysOS(t)
	o.OpenSettings()
	o = typeKeys(o, "x")
	for range 3 {
		o, _ = handleSettingsInput(tea.KeyPressMsg{Code: tea.KeyBackspace}, o)
	}
	if !o.SettingsSearchOpen() || o.SettingsSearchQuery() != "" {
		t.Errorf("after backspaces search=%v query=%q, want an open, empty search", o.SettingsSearchOpen(), o.SettingsSearchQuery())
	}
}

// TestSettingsNumberKeysPickATab: 3 goes to the third tab, and in the search
// a digit is text.
func TestSettingsNumberKeysPickATab(t *testing.T) {
	o := listKeysOS(t)
	o.OpenSettings()
	o, _ = handleSettingsInput(press("3"), o)
	if o.SettingsCategory != 2 {
		t.Errorf("3 went to tab %d, want the third (2)", o.SettingsCategory)
	}
	o, _ = handleSettingsInput(press("/"), o)
	o, _ = handleSettingsInput(press("5"), o)
	if o.SettingsSearchQuery() != "5" {
		t.Errorf("5 in the search left the query %q", o.SettingsSearchQuery())
	}
}

// TestSettingsTypingALetterSearches: a letter the page does not use starts a
// search with it, and a letter it does use keeps its meaning.
func TestSettingsTypingALetterSearches(t *testing.T) {
	o := listKeysOS(t)
	o.OpenSettings()
	o, _ = handleSettingsInput(press("j"), o)
	if o.SettingsSearchOpen() || o.SettingsSelected != 1 {
		t.Fatalf("j searched=%v selected=%d; j moves down on the tab view", o.SettingsSearchOpen(), o.SettingsSelected)
	}
	o = typeKeys(o, "pane")
	if !o.SettingsSearchOpen() || o.SettingsSearchQuery() != "pane" {
		t.Errorf("typing pane gave search=%v query=%q, want a search for pane", o.SettingsSearchOpen(), o.SettingsSearchQuery())
	}
	// Down moves through the results, and a ctrl chord does not type.
	o, _ = handleSettingsInput(tea.KeyPressMsg{Code: tea.KeyDown}, o)
	if o.SettingsSelected != 1 {
		t.Errorf("down in the results left row %d", o.SettingsSelected)
	}
	o, _ = handleSettingsInput(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl}, o)
	if o.SettingsSearchQuery() != "pane" {
		t.Errorf("ctrl+a changed the query to %q", o.SettingsSearchQuery())
	}
}
