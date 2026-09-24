package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// handleAgentMailInput handles keyboard input while the mailbox is open. Two
// views: the list of threads, where enter opens one, and the open thread,
// where r opens the reply line, o goes to the pane that last spoke, and esc
// steps back to the list. While the reply line is open every printable key is
// text, enter sends, and esc drops the draft.
func handleAgentMailInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	key := msg.String()

	if o.AgentMail.Composing {
		switch key {
		case "esc":
			o.AgentMailCancelReply()
		case "enter":
			return o, o.AgentMailSendReply()
		case "backspace":
			o.AgentMailBackspace()
		case "ctrl+u":
			o.AgentMailClearDraft()
		default:
			// The draft takes a space and any typed rune, the way the palette
			// query does.
			text := msg.Text
			if key == "space" {
				text = " "
			}
			o.AgentMailType(text)
		}
		return o, nil
	}

	action := lookupAction(msg, overlayKeys(o).GetMailAction)
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
