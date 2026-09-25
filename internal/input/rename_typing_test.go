package input

import (
	"testing"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// typeInto plays a string into the open rename editor one keypress at a time,
// the way a keyboard delivers it: a space arrives under its key name with a
// space in Text, everything else as the rune it produced.
func typeInto(o *app.OS, s string) *app.OS {
	for _, r := range s {
		msg := tea.KeyPressMsg{Code: r, Text: string(r)}
		if r == ' ' {
			msg = tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
		}
		o, _ = HandleKeyPress(msg, o)
	}
	return o
}

// TestRenameTakesASpace is the reported bug: the space key is named "space"
// rather than delivered as a one-character string, so the old length test threw
// it away and no name could hold a space.
func TestRenameTakesASpace(t *testing.T) {
	o := twoPaneOS(t)
	w := o.Windows[0]

	o.BeginRenameWindow(w)
	o.RenameBuffer = ""
	o = typeInto(o, "build v2")

	if o.RenameBuffer != "build v2" {
		t.Fatalf("rename buffer = %q, want %q", o.RenameBuffer, "build v2")
	}

	o, _ = HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter}, o)
	if w.CustomName != "build v2" {
		t.Fatalf("committed name = %q, want the spaced name", w.CustomName)
	}
}

// TestRenameTakesNonASCII covers the other half of the same filter: it compared
// bytes, so every rune above 127 was silently dropped and a user could not spell
// their own project's name.
func TestRenameTakesNonASCII(t *testing.T) {
	for _, name := range []string{"café", "日本語", "über builds"} {
		o := twoPaneOS(t)
		w := o.Windows[0]
		o.BeginRenameWindow(w)
		o.RenameBuffer = ""
		o = typeInto(o, name)

		if o.RenameBuffer != name {
			t.Errorf("typing %q left buffer %q", name, o.RenameBuffer)
		}
		o, _ = HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter}, o)
		if w.CustomName != name {
			t.Errorf("committed name = %q, want %q", w.CustomName, name)
		}
	}
}

// TestRenameRefusesUnprintable keeps the reason the filter existed. A control
// character, a private-use icon and a lone combining mark all reach the editor
// as text; none of them may land in a name.
func TestRenameRefusesUnprintable(t *testing.T) {
	o := twoPaneOS(t)
	o.BeginRenameWindow(o.Windows[0])
	o.RenameBuffer = ""

	for _, text := range []string{"\x07", "\x1b", "\u0301", "\ue0a0", "\U0001f600"} {
		o, _ = HandleKeyPress(tea.KeyPressMsg{Code: 'x', Text: text}, o)
		if o.RenameBuffer != "" {
			t.Fatalf("%q reached the rename buffer: %q", text, o.RenameBuffer)
		}
	}

	// A control character mixed into otherwise good text is stripped, not taken.
	o, _ = HandleKeyPress(tea.KeyPressMsg{Code: 'x', Text: "a\x00b"}, o)
	if o.RenameBuffer != "ab" {
		t.Fatalf("buffer = %q, want the control character dropped", o.RenameBuffer)
	}
}

// TestSessionSearchCanSpellASpacedName closes the loop the rename opens: a
// session called "Payments API" is only findable if the switcher's search field
// takes the same characters the rename editor now does.
func TestSessionSearchCanSpellASpacedName(t *testing.T) {
	o := twoPaneOS(t)
	o.ShowSessionSwitcher = true

	for _, r := range "payments api" {
		msg := tea.KeyPressMsg{Code: r, Text: string(r)}
		if r == ' ' {
			msg = tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
		}
		o, _ = HandleKeyPress(msg, o)
	}
	if o.SessionSwitcherQuery != "payments api" {
		t.Fatalf("search query = %q, want %q", o.SessionSwitcherQuery, "payments api")
	}

	// And a multi-byte one, backspaced back off a rune at a time.
	o.SessionSwitcherQuery = ""
	for _, r := range "café" {
		o, _ = HandleKeyPress(tea.KeyPressMsg{Code: r, Text: string(r)}, o)
	}
	if o.SessionSwitcherQuery != "café" {
		t.Fatalf("search query = %q, want café", o.SessionSwitcherQuery)
	}
	o, _ = HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyBackspace}, o)
	if o.SessionSwitcherQuery != "caf" {
		t.Fatalf("backspace left %q, want caf", o.SessionSwitcherQuery)
	}
	if !utf8.ValidString(o.SessionSwitcherQuery) {
		t.Fatalf("backspace broke the encoding: %q", o.SessionSwitcherQuery)
	}
}

// TestRenameBackspaceCountsRunes: once a name can hold multi-byte text, cutting
// one byte off the end leaves the buffer holding a broken sequence.
func TestRenameBackspaceCountsRunes(t *testing.T) {
	o := twoPaneOS(t)
	o.BeginRenameWindow(o.Windows[0])
	o.RenameBuffer = ""
	o = typeInto(o, "日本語")

	o, _ = HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyBackspace}, o)
	if o.RenameBuffer != "日本" {
		t.Fatalf("backspace left %q, want 日本", o.RenameBuffer)
	}
	if !utf8.ValidString(o.RenameBuffer) {
		t.Fatalf("backspace broke the encoding: %q", o.RenameBuffer)
	}

	// Backspacing an empty buffer is a no-op, not a slice out of range.
	for range 5 {
		o, _ = HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyBackspace}, o)
	}
	if o.RenameBuffer != "" {
		t.Fatalf("buffer = %q after backspacing past the start", o.RenameBuffer)
	}
}
