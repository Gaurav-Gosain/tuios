package input

import (
	"encoding/json"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/harness"
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

// TestInboxSpacePeeksAndKeysAnswer: space on the approval opens the peek, a
// digit chooses that option, and keys that are the list's (j, f) do nothing
// to the list while the peek owns the keyboard. esc returns to the list.
func TestInboxSpacePeeksAndKeysAnswer(t *testing.T) {
	o := inboxInputOS(t)
	var calls []string
	var params map[string]any
	o.SetInboxVerbCaller(func(verb string, p map[string]any, _ time.Duration) (json.RawMessage, error) {
		calls = append(calls, verb)
		if verb == "peek-prompt" {
			return json.Marshal(session.PromptPeek{
				Session: "local", Window: "b", State: "needs_input", Blocked: true, Found: true, Answerable: true,
				PromptID: "p1", Options: []harness.Option{{N: 1, Label: "Yes"}, {N: 2, Label: "No"}},
				Actions: []string{"approve", "deny", "choose"},
			})
		}
		params = p
		return nil, &session.VerbCallError{Code: session.ErrVerbPromptChanged}
	}, func() string { return "n" })
	o = leader(o, press("i"))
	o, cmd := HandleKeyPress(press("space"), o)
	if !o.InboxPeeking() || cmd == nil {
		t.Fatal("space did not open the peek")
	}
	o.Update(cmd())
	o, _ = HandleKeyPress(press("f"), o)
	if o.Inbox.Filter != "" {
		t.Error("f reached the list under the peek")
	}
	o, cmd = HandleKeyPress(press("2"), o)
	if cmd == nil {
		t.Fatal("2 did not answer")
	}
	cmd()
	if calls[len(calls)-1] != "respond" || params["action"] != "choose" || params["value"] != "2" {
		t.Errorf("2 sent %v %v", calls, params)
	}
	o, _ = HandleKeyPress(press("esc"), o)
	if o.InboxPeeking() || !o.ShowInbox {
		t.Error("esc did not return to the list")
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

// TestInboxSlashTypesASelector: / opens the selector line, where the list's
// keys are text (j does not move, d does not dismiss), enter applies it, and
// the list then holds only what it matches.
func TestInboxSlashTypesASelector(t *testing.T) {
	o := inboxInputOS(t)
	o.Inbox.Items[0].Harness = "claude-code"
	o.Inbox.Items[1].Harness = "codex"
	var calls []string
	o.SetInboxVerbCaller(func(verb string, _ map[string]any, _ time.Duration) (json.RawMessage, error) {
		calls = append(calls, verb)
		return json.RawMessage(`{}`), nil
	}, func() string { return "nonce" })
	o = leader(o, press("i"))
	o, _ = HandleKeyPress(press("/"), o)
	if !o.InboxSelecting() {
		t.Fatal("/ did not open the selector line")
	}
	for _, r := range "harness:codex dj" {
		key := string(r)
		if key == " " {
			key = "space"
		}
		o, _ = HandleKeyPress(press(key), o)
	}
	o, _ = HandleKeyPress(press("backspace"), o)
	o, _ = HandleKeyPress(press("backspace"), o)
	o, _ = HandleKeyPress(press("backspace"), o)
	if len(calls) != 0 {
		t.Fatalf("typing on the selector line called %v", calls)
	}
	o, _ = HandleKeyPress(press("enter"), o)
	if o.InboxSelecting() || o.Inbox.Select != "harness:codex" {
		t.Fatalf("enter did not apply the selector (open=%v select=%q)", o.InboxSelecting(), o.Inbox.Select)
	}
	// The only row left is the question for pane a, so enter goes there.
	o, _ = HandleKeyPress(press("enter"), o)
	if w := o.GetFocusedWindow(); w == nil || w.ID != "a" {
		t.Errorf("enter on the one selected item did not focus pane a")
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
