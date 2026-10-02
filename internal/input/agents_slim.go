//go:build slim

package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// tuios-slim has no Inbox and no agent mail, so their overlays never open.
// These stand in for inbox_input.go and agent_mail_input.go.

func handleAgentMailInput(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.CloseAgentMail()
	return o, nil
}

func handleInboxInput(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.CloseInbox()
	return o, nil
}
