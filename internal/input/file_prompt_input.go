package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

func handleFilePromptInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	key := msg.String()
	if key == "esc" {
		o.FilePromptCancel()
		return o, nil
	}
	if o.FileConfirmOpen() {
		if listKey(o, key, true, listPage, o.FileConfirmMove) {
			return o, nil
		}
		switch key {
		case "tab":
			o.FileConfirmMove(1)
		case "enter":
			return o, o.FilePromptSubmit()
		}
		return o, nil
	}

	if key == "enter" {
		return o, o.FilePromptSubmit()
	}
	o.FilePromptKey(key, msg.Text)
	return o, nil
}
