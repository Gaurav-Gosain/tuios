package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"
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

func leader(o *app.OS, key tea.KeyPressMsg) *app.OS {
	o, _ = HandleKeyPress(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl}, o)
	o, _ = HandleKeyPress(key, o)
	return o
}

// TestLeaderIOpensTheInbox: the chord reaches the Inbox from both modes, and
// the Inbox owns the keyboard until esc.
func TestLeaderIOpensTheInbox(t *testing.T) {
	for _, mode := range []app.Mode{app.WindowManagementMode, app.TerminalMode} {
		o := inboxInputOS(t)
		o.Mode = mode
		o = leader(o, press("i"))
		if !o.ShowInbox || o.Inbox.Filter != "" {
			t.Fatalf("mode %v: leader, i did not open the Inbox (open=%v filter=%q)", mode, o.ShowInbox, o.Inbox.Filter)
		}
		// f is the Inbox's, not the shell's and not a window command.
		o, _ = HandleKeyPress(press("f"), o)
		if o.Inbox.Filter != session.AttentionApproval {
			t.Errorf("mode %v: f set the filter to %q", mode, o.Inbox.Filter)
		}
		o, _ = HandleKeyPress(press("f"), o)
		o, _ = HandleKeyPress(press("esc"), o)
		if o.ShowInbox {
			t.Errorf("mode %v: esc did not close the Inbox", mode)
		}
	}
}

// TestInboxEnterGoesToThePane: j moves to the second item and enter focuses
// its pane and closes the Inbox.
func TestInboxEnterGoesToThePane(t *testing.T) {
	o := inboxInputOS(t)
	o = leader(o, press("i"))
	o, _ = HandleKeyPress(press("j"), o)
	o, _ = HandleKeyPress(press("enter"), o)
	if o.ShowInbox {
		t.Fatal("enter left the Inbox open")
	}
	if w := o.GetFocusedWindow(); w == nil || w.ID != "a" {
		t.Errorf("enter on the question did not focus pane a")
	}
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
