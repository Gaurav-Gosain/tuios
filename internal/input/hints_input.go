package input

import (
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// handleOpenHints is the hints action: prefix F by default.
func handleOpenHints(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.OpenHints()
	return o, nil
}

// handleOpenHintsAllPanes is the hints_all_panes action. It has no default
// key.
func handleOpenHintsAllPanes(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.OpenHintsAllPanes()
	return o, nil
}

// handleHintsKey takes a key while hints mode is open. Hints mode owns the
// keyboard: a key it does not use is dropped, never passed to the pane.
//
//   - a letter of a label copies the match once the label is complete
//   - the same letter with Shift copies it and types it into the focused
//     pane, whichever pane the match is on
//   - the same letter with Ctrl opens it
//   - backspace takes back a letter
//   - esc and the leader close. q closes when q is not a label letter, and
//     ctrl+c and ctrl+g close when c and g are not label letters; when they
//     are, they are Ctrl and a label letter like any other.
//
// The leader is read first, so a leader that is Ctrl and a label letter
// (ctrl+b with b in the alphabet) closes hints rather than opening a match.
func handleHintsKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if isLeaderKey(msg, &o.Settings) {
		o.CloseHints()
		return o, nil
	}
	key := msg.Key()
	switch msg.String() {
	case "esc":
		o.CloseHints()
		return o, nil
	case "backspace":
		o.HintsBackspace()
		return o, nil
	case "ctrl+c", "ctrl+g":
		if !o.HintsUsesLetter(key.Code) {
			o.CloseHints()
			return o, nil
		}
	}

	if key.Mod.Contains(tea.ModAlt) || key.Mod.Contains(tea.ModSuper) {
		return o, nil
	}
	if key.Mod.Contains(tea.ModCtrl) {
		if r := unicode.ToLower(key.Code); r >= 'a' && r <= 'z' {
			return o, o.HintsPress(r, app.HintOpen)
		}
		return o, nil
	}

	// A key that produced more than one character is an input method's
	// commit or a burst, never one letter of a label.
	r, size := utf8.DecodeRuneInString(key.Text)
	switch {
	case key.Text == "":
		r = key.Code
	case size != len(key.Text):
		return o, nil
	}
	if r == 'q' && !o.HintsUsesLetter('q') {
		o.CloseHints()
		return o, nil
	}
	lower := unicode.ToLower(r)
	if lower < 'a' || lower > 'z' {
		return o, nil
	}
	// Shift asks for the match to be typed. An upper case letter counts as
	// Shift unless the terminal says Caps Lock made it upper case; a terminal
	// that does not report Caps Lock cannot tell the two apart.
	action := app.HintCopy
	if key.Mod.Contains(tea.ModShift) || (unicode.IsUpper(r) && !key.Mod.Contains(tea.ModCapsLock)) {
		action = app.HintType
	}
	return o, o.HintsPress(lower, action)
}
