package app

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/harness"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/charmbracelet/x/ansi"
)

// fakeDaemon answers the peek's verb calls from canned replies and records
// every call it gets.
type fakeDaemon struct {
	peeks   []session.PromptPeek
	respond func(params map[string]any) (json.RawMessage, error)
	calls   []fakeCall
}

type fakeCall struct {
	verb   string
	params map[string]any
}

func (f *fakeDaemon) call(verb string, params map[string]any, _ time.Duration) (json.RawMessage, error) {
	f.calls = append(f.calls, fakeCall{verb, params})
	switch verb {
	case "peek-prompt":
		pk := f.peeks[0]
		if len(f.peeks) > 1 {
			f.peeks = f.peeks[1:]
		}
		return json.Marshal(pk)
	case "respond":
		return f.respond(params)
	}
	return nil, &session.VerbCallError{Code: session.ErrVerbUnknownVerb, Message: verb}
}

func (f *fakeDaemon) count(verb string) int {
	n := 0
	for _, c := range f.calls {
		if c.verb == verb {
			n++
		}
	}
	return n
}

// claudePeek is what peek-prompt says about Claude Code's permission menu.
func claudePeek(id string) session.PromptPeek {
	return session.PromptPeek{
		Session: "here", Window: "w-2", Harness: "claude-code", State: "needs_input",
		StateAt: time.Now().Add(-2 * time.Minute).UnixNano(), Blocked: true, Found: true, Answerable: true,
		Kind: harness.PromptKindApproval, PromptID: id,
		Lines:   []string{" Bash command", "   rm -rf build", " Do you want to proceed?", " > 1. Yes", "   2. Yes, and don't ask again", "   3. No (esc)"},
		Options: []harness.Option{{N: 1, Label: "Yes"}, {N: 2, Label: "Yes, and don't ask again"}, {N: 3, Label: "No (esc)"}},
		Actions: []string{"approve", "approve_always", "deny", "choose"},
	}
}

// peekOS is a client with one approval in its Inbox, selected, and a fake
// daemon behind the peek.
func peekOS(t *testing.T, f *fakeDaemon) *OS {
	t.Helper()
	m := inboxOS(t, zeroSettle())
	m.applyInboxSnapshot(InboxSnapshotMsg{Items: []session.AttentionItem{
		item("7", session.AttentionApproval, "here", "w-2", "Bash: rm -rf build", time.Now().Add(-2*time.Minute).UnixNano()),
	}})
	m.OpenInbox("")
	m.SetInboxVerbCaller(f.call, func() string { return "nonce-1" })
	return m
}

// run runs a command and feeds its message back, the way Update would.
func run(t *testing.T, m *OS, cmd tea.Cmd) {
	t.Helper()
	for cmd != nil {
		switch msg := cmd().(type) {
		case InboxPeekMsg:
			m.applyInboxPeek(msg)
			cmd = nil
		case InboxRespondedMsg:
			cmd = m.applyInboxResponded(msg)
		default:
			t.Fatalf("unexpected message %T", msg)
		}
	}
}

// TestInboxPeekShowsThePrompt: space on an approval reads the prompt of that
// pane and draws it with its options, the wait, and only the keys that answer
// it, in words.
func TestInboxPeekShowsThePrompt(t *testing.T) {
	f := &fakeDaemon{peeks: []session.PromptPeek{claudePeek("p1")}}
	m := peekOS(t, f)
	run(t, m, m.InboxPeek())
	if !m.InboxPeeking() {
		t.Fatal("space did not open the peek")
	}
	if c := f.calls[0]; c.verb != "peek-prompt" || c.params["session"] != "here" || c.params["window"] != "w-2" {
		t.Fatalf("the peek read %v", c)
	}
	out, _, _ := m.renderInbox()
	plain := ansi.Strip(out)
	for _, want := range []string{"Approval: agent-7", "rm -rf build", "Do you want to proceed?", "2m", "1  Yes", "choose", "approve", "always", "deny", "go to pane"} {
		if !strings.Contains(plain, want) {
			t.Errorf("the peek does not show %q:\n%s", want, plain)
		}
	}
	if strings.Contains(plain, "type") {
		t.Errorf("the peek offers a text answer the prompt does not take:\n%s", plain)
	}

	m.InboxPeekBack()
	if m.InboxPeeking() || !m.ShowInbox {
		t.Error("back did not return to the list")
	}
}

// TestInboxPeekApproveSendsTheNonceAndPromptID: a approves with the prompt_id
// the peek read and this client's attach nonce, and a landed answer closes the
// peek and says where the pane went.
func TestInboxPeekApproveSendsTheNonceAndPromptID(t *testing.T) {
	f := &fakeDaemon{peeks: []session.PromptPeek{claudePeek("p1")}}
	f.respond = func(map[string]any) (json.RawMessage, error) {
		return json.Marshal(session.PromptResponse{Sent: "1", SettledBy: "state", State: "working"})
	}
	m := peekOS(t, f)
	run(t, m, m.InboxPeek())
	run(t, m, m.InboxAnswer(harness.ActionApprove, ""))
	if f.count("respond") != 1 {
		t.Fatalf("respond was called %d times", f.count("respond"))
	}
	p := f.calls[len(f.calls)-1].params
	if p["action"] != "approve" || p["prompt_id"] != "p1" || p["human_nonce"] != "nonce-1" || p["window"] != "w-2" {
		t.Errorf("respond params %v", p)
	}
	if m.InboxPeeking() {
		t.Error("the peek stayed open after the answer landed")
	}
	if n := len(m.Notifications); n == 0 || !strings.Contains(m.Notifications[n-1].Message, "working") {
		t.Errorf("no toast says where the pane went: %+v", m.Notifications)
	}
}

// TestInboxPeekRereadsAChangedPrompt: an answer refused with prompt_changed
// pressed nothing, so the peek says so and shows the prompt as it is now.
func TestInboxPeekRereadsAChangedPrompt(t *testing.T) {
	f := &fakeDaemon{peeks: []session.PromptPeek{claudePeek("p1"), claudePeek("p2")}}
	f.respond = func(map[string]any) (json.RawMessage, error) {
		return nil, &session.VerbCallError{Code: session.ErrVerbPromptChanged, Message: "nothing was pressed"}
	}
	m := peekOS(t, f)
	run(t, m, m.InboxPeek())
	run(t, m, m.InboxAnswer(harness.ActionDeny, ""))
	if !m.InboxPeeking() {
		t.Fatal("a refused answer closed the peek")
	}
	if f.count("peek-prompt") != 2 || m.Inbox.Peek.Peek.PromptID != "p2" {
		t.Errorf("the peek did not read the prompt again: %d reads, id %s", f.count("peek-prompt"), m.Inbox.Peek.Peek.PromptID)
	}
	out, _, _ := m.renderInbox()
	if !strings.Contains(ansi.Strip(out), "nothing was pressed") {
		t.Errorf("the peek does not say the answer pressed nothing:\n%s", ansi.Strip(out))
	}
}

// TestInboxPeekRefusesKeysFromSendKeys: a key that send-keys pushed into the
// client never answers with the person's nonce, and a text draft such a key
// touched is never sent.
//
// Negative control: without the ProcessingRemoteKeys check in InboxAnswer,
// respond is called and the first assertion fails.
func TestInboxPeekRefusesKeysFromSendKeys(t *testing.T) {
	f := &fakeDaemon{peeks: []session.PromptPeek{claudePeek("p1")}}
	f.respond = func(map[string]any) (json.RawMessage, error) {
		return json.Marshal(session.PromptResponse{Sent: "1", SettledBy: "state", State: "working"})
	}
	m := peekOS(t, f)
	run(t, m, m.InboxPeek())
	m.ProcessingRemoteKeys = true
	run(t, m, m.InboxAnswer(harness.ActionApprove, ""))
	if f.count("respond") != 0 {
		t.Fatal("ASSERTION: a key from send-keys answered the prompt")
	}
	if !strings.Contains(m.Inbox.Peek.Err, "send-keys") {
		t.Errorf("the refusal does not say why: %q", m.Inbox.Peek.Err)
	}

	text := claudePeek("p3")
	text.Actions = []string{"text"}
	f.peeks = []session.PromptPeek{text}
	m.ProcessingRemoteKeys = false
	run(t, m, m.InboxPeekRefresh())
	m.InboxPeekStartText()
	m.ProcessingRemoteKeys = true
	m.InboxPeekType("yes")
	m.ProcessingRemoteKeys = false
	run(t, m, m.InboxPeekSendText())
	if f.count("respond") != 0 {
		t.Fatal("ASSERTION: a draft send-keys typed was sent")
	}
}

// TestInboxPeekOnlyOffersWhatThePromptTakes: an answer the peek does not list,
// or an option that is not on the screen, never reaches the daemon.
func TestInboxPeekOnlyOffersWhatThePromptTakes(t *testing.T) {
	pk := claudePeek("p1")
	pk.Actions = []string{"deny", "choose"}
	f := &fakeDaemon{peeks: []session.PromptPeek{pk}}
	m := peekOS(t, f)
	run(t, m, m.InboxPeek())
	run(t, m, m.InboxAnswer(harness.ActionApprove, ""))
	run(t, m, m.InboxAnswer(harness.ActionChoose, "7"))
	m.InboxPeekStartText()
	if f.count("respond") != 0 || m.InboxPeekComposing() {
		t.Fatalf("an answer the prompt does not take went through (%d calls, composing %v)", f.count("respond"), m.InboxPeekComposing())
	}
	if !strings.Contains(m.Inbox.Peek.Err, "deny, choose") {
		t.Errorf("the refusal does not list what the prompt takes: %q", m.Inbox.Peek.Err)
	}
}

// TestInboxPeekTypesAnAnswer: tab opens the line, and enter sends what was
// typed as a text answer.
func TestInboxPeekTypesAnAnswer(t *testing.T) {
	pk := claudePeek("p1")
	pk.Actions = []string{"text"}
	pk.Options = nil
	f := &fakeDaemon{peeks: []session.PromptPeek{pk}}
	var got map[string]any
	f.respond = func(p map[string]any) (json.RawMessage, error) {
		got = p
		return json.Marshal(session.PromptResponse{Sent: "text", SettledBy: "state", State: "working"})
	}
	m := peekOS(t, f)
	run(t, m, m.InboxPeek())
	m.InboxPeekStartText()
	m.InboxPeekType("main")
	m.InboxPeekType(" branch")
	m.InboxPeekBackspace()
	run(t, m, m.InboxPeekSendText())
	if got["action"] != "text" || got["value"] != "main branc" {
		t.Errorf("respond params %v", got)
	}
}

// TestInboxPeekDropsAStaleReply: a reply to a peek since closed does not land
// on the next one.
func TestInboxPeekDropsAStaleReply(t *testing.T) {
	f := &fakeDaemon{peeks: []session.PromptPeek{claudePeek("old"), claudePeek("new")}}
	m := peekOS(t, f)
	stale := m.InboxPeek()().(InboxPeekMsg)
	m.InboxPeekBack()
	run(t, m, m.InboxPeek())
	m.applyInboxPeek(stale)
	if id := m.Inbox.Peek.Peek.PromptID; id != "new" {
		t.Errorf("the peek shows prompt %s, want new", id)
	}
}

// TestInboxPeekIsForPrompts: space on an item with no prompt says what space
// is for and calls nothing.
func TestInboxPeekIsForPrompts(t *testing.T) {
	f := &fakeDaemon{}
	m := inboxOS(t, zeroSettle())
	m.applyInboxSnapshot(InboxSnapshotMsg{Items: []session.AttentionItem{
		item("1", session.AttentionFinished, "here", "w-1", "", 10),
	}})
	m.OpenInbox("")
	m.SetInboxVerbCaller(f.call, nil)
	if cmd := m.InboxPeek(); cmd != nil || m.InboxPeeking() || len(f.calls) != 0 {
		t.Error("space peeked at a finished turn")
	}
}
