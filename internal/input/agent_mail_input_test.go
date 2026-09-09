package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// The routes into the mailbox, at the level of the real key dispatch: the
// leader chord, the rail's key, and the keys inside the overlay. A table
// holding an entry proves nothing here; the key has to reach the action.

// mailThreadOS is a two-pane window-mode model attached to a daemon session
// holding one message from pane a to the person.
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

// TestLeaderMOpensTheMailbox: the default chord reaches the overlay from
// window mode and from terminal mode.
func TestLeaderMOpensTheMailbox(t *testing.T) {
	for _, mode := range []app.Mode{app.WindowManagementMode, app.TerminalMode} {
		o := twoPaneWM(t)
		o.Mode = mode
		o, _ = HandleKeyPress(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl}, o)
		if !o.PrefixActive {
			t.Fatalf("mode %v: the leader did not arm the prefix", mode)
		}
		o, _ = HandleKeyPress(tea.KeyPressMsg{Code: 'M', Text: "M", Mod: tea.ModShift}, o)
		if !o.ShowAgentMail {
			t.Errorf("mode %v: leader, M did not open the mailbox", mode)
		}
	}
}

// TestRailIOpensTheMailbox: i while the rail owns the keyboard opens the
// mailbox and keeps the rail's focus, so closing it comes back to the rail.
func TestRailIOpensTheMailbox(t *testing.T) {
	o := twoPaneWM(t)
	o.SidebarFocused = true
	o, _ = HandleKeyPress(press("i"), o)
	if !o.ShowAgentMail {
		t.Fatal("i on the rail did not open the mailbox")
	}
	if !o.SidebarFocused {
		t.Error("opening the mailbox from the rail dropped the rail's focus")
	}
	// And the mailbox, not the rail, gets the next key.
	o, _ = HandleKeyPress(press("esc"), o)
	if o.ShowAgentMail {
		t.Error("esc did not close the mailbox opened from the rail")
	}
	if !o.SidebarFocused {
		t.Error("closing the mailbox took the rail's focus with it")
	}
}

// TestMailboxKeysReadReplyAndLeave walks the overlay: enter opens the thread,
// r opens the reply line, typed keys are the draft, enter sends, esc steps
// back to the list and esc again closes. In terminal mode too, where a typed
// reply must not reach the shell.
func TestMailboxKeysReadReplyAndLeave(t *testing.T) {
	for _, mode := range []app.Mode{app.WindowManagementMode, app.TerminalMode} {
		o := mailThreadOS(t)
		o.Mode = mode
		o.OpenAgentMail()
		o.AgentMail.Loading = false

		o, cmd := HandleKeyPress(press("enter"), o)
		if o.AgentMail.Thread != 3 {
			t.Fatalf("mode %v: enter on the list opened thread %d, want 3", mode, o.AgentMail.Thread)
		}
		if cmd == nil {
			t.Errorf("mode %v: opening a thread with unread mail for the person returned no marking command", mode)
		}
		o, _ = HandleKeyPress(press("r"), o)
		if !o.AgentMail.Composing {
			t.Fatalf("mode %v: r did not open the reply line", mode)
		}
		for _, k := range []string{"o", "k", "space", "g", "o"} {
			o, _ = HandleKeyPress(press(k), o)
		}
		if o.AgentMail.Draft != "ok go" {
			t.Errorf("mode %v: the draft reads %q, want %q", mode, o.AgentMail.Draft, "ok go")
		}
		o, cmd = HandleKeyPress(press("enter"), o)
		if cmd == nil || !o.AgentMail.Sending {
			t.Errorf("mode %v: enter on the draft did not send (cmd=%v sending=%v)", mode, cmd != nil, o.AgentMail.Sending)
		}
		o.AgentMail.Sending, o.AgentMail.Composing = false, false

		o, _ = HandleKeyPress(press("esc"), o)
		if o.AgentMail.Thread != 0 || !o.ShowAgentMail {
			t.Errorf("mode %v: esc in the thread did not step back to the list", mode)
		}
		o, _ = HandleKeyPress(press("esc"), o)
		if o.ShowAgentMail {
			t.Errorf("mode %v: esc on the list did not close the mailbox", mode)
		}
	}
}
