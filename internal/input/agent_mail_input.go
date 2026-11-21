package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

func handleAgentMailInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.AgentMail.Composing {
		return o, o.AgentMailKey(msg)
	}

	action := tableAction(o, msg, overlayKeys(o), config.TableMail)
	if action == "" {
		return o, nil
	}
	o.NoteAction(action)
	switch action {
	case config.ActionMailBack:
		o.AgentMailBack()
	case config.ActionMailOpen:
		if o.AgentMail.Thread == 0 {
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
