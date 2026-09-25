package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
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

func leader(o *app.OS, key tea.KeyPressMsg) *app.OS {
	o, _ = HandleKeyPress(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl}, o)
	o, _ = HandleKeyPress(key, o)
	return o
}

// TestLeaderOWalksWhatIsWaiting: o goes to the oldest item needing you, and
// because the prefix stays armed, o again goes to the next.
func TestLeaderOWalksWhatIsWaiting(t *testing.T) {
	o := inboxInputOS(t)
	o.FocusWindow(0)
	o = leader(o, press("o"))
	if w := o.GetFocusedWindow(); w == nil || w.ID != "b" {
		t.Fatalf("leader, o focused %v, want b (the approval groups first)", w)
	}
	if !o.PrefixRepeatLive() {
		t.Fatal("the prefix did not stay armed after o")
	}
	o, _ = HandleKeyPress(press("o"), o)
	if w := o.GetFocusedWindow(); w == nil || w.ID != "a" {
		t.Errorf("a second o focused %v, want a", w)
	}
}

// TestInboxKeysAreRebindable: the Inbox's keys come from [keybindings.inbox]
// and the mailbox's from [keybindings.mail], like every other key. They were
// literals in this file, so KEYBINDINGS.md's "every binding is rebindable"
// was not true of them.
func TestInboxKeysAreRebindable(t *testing.T) {
	o := osWithBindings(t, func(k *config.KeybindingsConfig) {
		k.Inbox[config.ActionInboxFilter] = []string{"x"}
		k.Mail[config.ActionMailFocusPane] = []string{"p"}
	})
	o.Width, o.Height = 120, 40
	o.Windows = []*terminal.Window{{ID: "a", Width: 60, Height: 40, Workspace: o.CurrentWorkspace}}
	o.Inbox.Live = true
	o.Inbox.Items = []session.AttentionItem{{ID: "1", Kind: session.AttentionApproval, Session: "local", Window: "a", Since: 1}}
	o.OpenInbox("")

	o, _ = HandleKeyPress(press("f"), o)
	if o.Inbox.Filter != "" {
		t.Errorf("f still steps the filter after it was rebound: %q", o.Inbox.Filter)
	}
	o, _ = HandleKeyPress(press("x"), o)
	if o.Inbox.Filter == "" {
		t.Error("x, the rebound filter key, did nothing")
	}
	if got := o.KeybindRegistry.GetInboxKeys(config.ActionMailFocusPane); len(got) != 1 || got[0] != "p" {
		t.Errorf("the mailbox's focus key reads back as %v", got)
	}
}
