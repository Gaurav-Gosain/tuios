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

// handleHintsKey takes a key while hints mode is open. Hints mode owns the
// keyboard: a key it does not use is dropped, never passed to the pane.
//
//   - a letter of a label copies the match once the label is complete
//   - the same letter with Shift copies it and types it into the pane
//   - the same letter with Ctrl opens it
//   - backspace takes back a letter, and esc, ctrl+c or q close
func handleHintsKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	key := msg.Key()
	switch msg.String() {
	case "esc", "ctrl+c", "ctrl+g":
		o.CloseHints()
		return o, nil
	case "backspace":
		o.HintsBackspace()
		return o, nil
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

	r, size := utf8.DecodeRuneInString(key.Text)
	if size == 0 || size != len(key.Text) {
		r = key.Code
	}
	if r == 'q' && !o.HintsUsesLetter('q') {
		o.CloseHints()
		return o, nil
	}
	lower := unicode.ToLower(r)
	if lower < 'a' || lower > 'z' {
		return o, nil
	}
	action := app.HintCopy
	if unicode.IsUpper(r) || key.Mod.Contains(tea.ModShift) {
		action = app.HintType
	}
	return o, o.HintsPress(lower, action)
}
