package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

func handleWorktreePromptInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	switch msg.String() {
	case "esc":
		o.WorktreePromptCancel()
		return o, nil
	case "enter":
		return o, o.WorktreePromptSubmit()
	case "tab":
		o.WorktreePromptNextField()
		return o, nil
	}
	o.WorktreePromptKey(msg.String(), msg.Text)
	return o, nil
}
