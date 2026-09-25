package input

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// inboxInputOS is a two-pane model with an Inbox holding a question for pane a
// and an approval for pane b, in this client's own session.
func inboxInputOS(t *testing.T) *app.OS {
	t.Helper()
	o := twoPaneWM(t)
	o.Inbox.Live = true
	o.Inbox.Items = []session.AttentionItem{
		{ID: "2", Kind: session.AttentionApproval, Session: "local", Window: "b", Since: 20},
		{ID: "1", Kind: session.AttentionQuestion, Session: "local", Window: "a", Since: 10},
	}
	return o
}
