package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// handleFilePromptInput drives the rail's file dialogs.
//
// Two shapes behind one entry point. A name prompt takes text and answers on
// enter. A confirmation takes no text at all: it is a selection and then enter,
// which is what the close-session dialog does and what makes a confirmation
// worth appearing. There is no one-key yes, and the key that opened the dialog
// does nothing inside it, because a confirmation a repeated keypress satisfies
// is how the accident it exists to stop actually happens.
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
	promptTextKeys{
		Backspace: o.FilePromptBackspace,
		Clear:     o.FilePromptClearInput,
		Type:      o.FilePromptType,
	}.handle(msg)
	return o, nil
}
