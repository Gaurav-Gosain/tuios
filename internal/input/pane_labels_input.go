package input

import (
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

func handleDisplayPanes(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.OpenPaneLabels()
	return o, nil
}

func handlePaneLabelsKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if isLeaderKey(msg, &o.Settings, o.HostBaseCode(msg)) {
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
