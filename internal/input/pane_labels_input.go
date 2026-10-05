package input

import (
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// handleDisplayPanes is the display_panes action: prefix Q by default.
func handleDisplayPanes(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.OpenPaneLabels()
	return o, nil
}

// handlePaneLabelsKey takes a key while the pane labels are up. The labels
// own the keyboard: a key they do not use is dropped, never passed to the
// pane, so a slip cannot type into a shell.
//
//   - a key of a label focuses that pane once the label is complete
//   - backspace takes back a key
//   - esc, ctrl+c, ctrl+g and the leader close. q closes too, when q is not
//     a label key.
func handlePaneLabelsKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if isLeaderKey(msg, &o.Settings) {
		o.ClosePaneLabels()
		return o, nil
	}
	key := msg.Key()
	switch msg.String() {
	case "esc":
		o.ClosePaneLabels()
		return o, nil
	case "backspace":
		o.PaneLabelsBackspace()
		return o, nil
	case "ctrl+c", "ctrl+g":
		o.ClosePaneLabels()
		return o, nil
	}
	if key.Mod.Contains(tea.ModAlt) || key.Mod.Contains(tea.ModSuper) || key.Mod.Contains(tea.ModCtrl) {
		return o, nil
	}
	// A key that produced more than one character is an input method's
	// commit or a burst, never one key of a label.
	r, size := utf8.DecodeRuneInString(key.Text)
	switch {
	case key.Text == "":
		r = key.Code
	case size != len(key.Text):
		return o, nil
	}
	if r == 'q' && !o.PaneLabelsUsesKey('q') {
		o.ClosePaneLabels()
		return o, nil
	}
	// A label key typed with Shift is the same key: the labels draw letters
	// in capitals, and pressing what is on the screen must work.
	r = unicode.ToLower(r)
	if !o.PaneLabelsUsesKey(r) {
		return o, nil
	}
	prev := o.FocusedWindow
	if o.PaneLabelsPress(r) {
		return afterFocusCommand(o, prev, focusEnterTargeted)
	}
	return o, nil
}
