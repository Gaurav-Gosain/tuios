package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// handleAgentMailInput handles keyboard input while the mailbox is open. Two
// views: the list of threads, where enter opens one, and the open thread,
// where r opens the reply line, o goes to the pane that last spoke, and esc
// steps back to the list. n in the list opens a picker of the session's
// agents, and enter there opens the line for a new message to that agent.
// While a reply or message is open the composer takes every key: enter
// sends, ctrl+j makes a new line, and esc drops the draft.
func handleAgentMailInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.AgentMail.Composing {
		return o, o.AgentMailKey(msg)
	}

	action := lookupAction(o, msg, overlayKeys(o).GetMailAction)
	if action == "" {
		return o, nil
	}
	o.NoteAction(action)
	switch action {
	case config.ActionMailBack:
		o.AgentMailBack()
	case config.ActionMailOpen:
		if o.AgentMail.Thread == 0 {
			// While the agent picker is open, this chooses the agent.
			return o, o.AgentMailOpenSelected()
		}
		o.AgentMailStartReply()
	case config.ActionMailReply:
		o.AgentMailStartReply()
	case config.ActionMailNew:
		o.AgentMailStartNew()
	case config.ActionMailFocusPane:
		o.AgentMailFocusPane()
	case config.ActionMailUp:
		o.AgentMailMove(-1)
	case config.ActionMailDown:
		o.AgentMailMove(1)
	case config.ActionMailPageUp:
		o.AgentMailMove(-10)
	case config.ActionMailPageDown:
		o.AgentMailMove(10)
	}
	return o, nil
}
