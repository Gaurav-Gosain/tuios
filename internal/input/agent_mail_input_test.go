package input

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

func mailThreadOS(t *testing.T) *app.OS {
	t.Helper()
	o := twoPaneWM(t)
	o.IsDaemonSession = true
	o.DaemonClient = &session.TUIClient{}
	o.SessionName = "mail"
	o.AgentMail.Messages = []session.AgentMessage{{
		ID: 3, Kind: "message", From: "a", FromLabel: "a", To: session.AgentInboxHuman, ToLabel: "human",
		Subject: "which retry policy?", Text: "exponential or fixed?", ThreadID: 3, SentAt: 1,
	}}
	return o
}
