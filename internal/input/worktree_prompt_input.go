package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// handleWorktreePromptInput drives the rail's worktree dialog.
//
// It is the file name prompt's keyboard with none of the confirmations: two
// text fields tab cycles, enter creates, esc cancels. There is no yes or no
// to pick, so the keys are the fields' keys and nothing else.
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
	promptTextKeys{
		Backspace: o.WorktreePromptBackspace,
		Clear:     o.WorktreePromptClearInput,
		Type:      o.WorktreePromptType,
	}.handle(msg)
	return o, nil
}
