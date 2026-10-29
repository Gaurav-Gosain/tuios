package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// handleGotoPicker is the goto_picker action.
func handleGotoPicker(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.OpenGotoPicker()
	return o, nil
}

// handleGotoPickerInput takes a key while the go-to picker is up, in either
// mode. It is driven like the session and workspace switchers: the shared
// list keys move, enter goes, esc closes, and every other key is query text.
func handleGotoPickerInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	keyStr := msg.String()
	if listKey(o, keyStr, false, listPage, o.GotoPickerMove) {
		return o, nil
	}
	switch keyStr {
	case "esc":
		o.CloseGotoPicker()
	case "enter":
		o.GotoPickerActivate()
	default:
		q := o.GotoPickerQuery()
		if changed, _ := editFilterQuery(msg, &q, true); changed {
			o.SetGotoPickerQuery(q)
		}
	}
	return o, nil
}
