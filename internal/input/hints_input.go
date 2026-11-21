package input

import (
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

func handleOpenHints(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.OpenHints()
	return o, nil
}

func handleOpenHintsAllPanes(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.OpenHintsAllPanes()
	return o, nil
}

func handleHintsKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if isLeaderKey(msg, &o.Settings, o.HostBaseCode(msg)) {
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

	r, size := utf8.DecodeRuneInString(key.Text)
	switch {
	case key.Text == "":
		r = key.Code
	case size != len(key.Text):
		return o, nil
	}
	if r == '?' {
		o.HintsShowKeys()
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
	action := app.HintCopy
	if key.Mod.Contains(tea.ModShift) || (unicode.IsUpper(r) && !key.Mod.Contains(tea.ModCapsLock)) {
		action = app.HintType
	}
	return o, o.HintsPress(lower, action)
}
