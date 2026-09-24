package app

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/harness"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// The Inbox's peek: space on an approval or a question reads the prompt the
// agent is blocked on (the daemon's peek-prompt verb) and shows it over the
// list, with its options and how long it has waited. From there the person
// answers without attaching: a digit chooses an option, a approves, A approves
// for good, d denies, tab opens a line to type an answer into, enter goes to
// the pane instead, and esc goes back to the list.
//
// An answer is the daemon's respond verb, with this client's attach nonce and
// the prompt_id the peek read. The daemon reads the prompt again before it
// presses anything, so an answer to a prompt that changed, or that another
// client answered first, presses nothing and comes back prompt_changed. The
// peek then reads the prompt again and says so, and the person answers what
// is there now.
//
// A key that did not come from the keyboard, one that tuios send-keys pushed
// into this client, never answers: the nonce stands for the person, and an
// agent that can drive the client with send-keys is not the person. The same
// rule keeps such keys from signing a mail reply (agent_mail.go).

// inboxPeekTimeout bounds the peek-prompt call.
const inboxPeekTimeout = 5 * time.Second

// inboxRespondWait is how long respond waits for the pane to move on, and
// inboxRespondTimeout bounds the call around that wait.
const (
	inboxRespondWait    = 5000
	inboxRespondTimeout = 15 * time.Second
)

// inboxPeek is the open peek.
type inboxPeek struct {
	// Item is the Inbox item the peek is about.
	Item session.AttentionItem
	// Loading is true while a read is out.
	Loading bool
	// Peek is the last read, nil until one arrives.
	Peek *session.PromptPeek
	// Sending is true while an answer is out.
	Sending bool
	// Err is the last read or answer that failed, in words.
	Err string
	// Note is what the peek says above the prompt: that the prompt changed
	// before an answer landed, for one.
	Note string
	// Composing is true while the text line is open, and Draft is its text.
	Composing bool
	Draft     string
	// DraftAutomated is true when a key that did not come from the keyboard
	// touched the draft. Such a draft is never sent.
	DraftAutomated bool
	// gen tells a reply to this peek from one to a peek since closed.
	gen uint64
}

// InboxPeekMsg is a peek-prompt reply.
type InboxPeekMsg struct {
	Gen  uint64
	Peek *session.PromptPeek
	Err  error
}

// InboxRespondedMsg is a respond reply.
type InboxRespondedMsg struct {
	Gen    uint64
	Action string
	Res    *session.PromptResponse
	Err    error
}

// inboxVerbCall makes one verb call to this machine's daemon.
type inboxVerbCall func(verb string, params map[string]any, timeout time.Duration) (json.RawMessage, error)

// inboxCaller is how the peek reaches the daemon. Tests set InboxState.call.
func (m *OS) inboxCaller() inboxVerbCall {
	if m.Inbox.call != nil {
		return m.Inbox.call
	}
	build := ""
	if m.DaemonClient != nil {
		build = m.DaemonClient.ClientVersion()
	}
	return func(verb string, params map[string]any, timeout time.Duration) (json.RawMessage, error) {
		client, err := session.DialVerbClientAs(build)
		if err != nil {
			return nil, err
		}
		defer func() { _ = client.Close() }()
		return client.CallWithTimeout(verb, params, timeout)
	}
}

// SetInboxVerbCaller replaces how the peek reaches the daemon and where it
// reads the attach nonce from. Tests use it; nil for either restores the
// default.
func (m *OS) SetInboxVerbCaller(call func(verb string, params map[string]any, timeout time.Duration) (json.RawMessage, error), nonce func() string) {
	m.Inbox.call, m.Inbox.nonce = call, nonce
}

// inboxNonce is this client's attach nonce.
func (m *OS) inboxNonce() string {
	if m.Inbox.nonce != nil {
		return m.Inbox.nonce()
	}
	if m.DaemonClient == nil {
		return ""
	}
	return m.DaemonClient.HumanNonce()
}

// inboxPeekable reports whether an item has a prompt to peek at.
func inboxPeekable(it session.AttentionItem) bool {
	return (it.Kind == session.AttentionApproval || it.Kind == session.AttentionQuestion) && it.Window != ""
}

// InboxPeeking reports whether the peek is open over the Inbox.
func (m *OS) InboxPeeking() bool {
	return m.Inbox.Peek != nil
}

// InboxPeekComposing reports whether the peek's text line is open, where every
// printable key is text.
func (m *OS) InboxPeekComposing() bool {
	return m.Inbox.Peek != nil && m.Inbox.Peek.Composing
}

// InboxPeek opens the peek on the selected item and reads its prompt.
func (m *OS) InboxPeek() tea.Cmd {
	it, ok := m.inboxSelected()
	if !ok {
		return nil
	}
	if !inboxPeekable(it) {
		m.ShowNotification(capitalize(m.inboxKeyOr(config.ActionInboxPeek, "space"))+" reads the prompt of an approval or a question. Enter goes to the pane.", "info", m.Settings.NotificationDuration)
		return nil
	}
	if it.RequestID != "" {
		// A hook holds this approval off the screen until the Inbox answers
		// it, so the pane shows no prompt for the peek to read or press
		// keys into. The Inbox answers it with reply-approval instead.
		m.ShowNotification("The Inbox is holding this approval: answer it with "+inboxAnswerKeys(it)+", or enter to answer in the pane", "info", m.Settings.NotificationDuration)
		return nil
	}
	if it.Host != "" || m.AttachedHost != "" {
		m.ShowNotification("That prompt is on another machine: tuios peek-prompt -w HOST:SESSION:WINDOW reads it", "info", m.Settings.NotificationDuration)
		return nil
	}
	if m.DaemonClient == nil && m.Inbox.call == nil {
		m.ShowNotification("Reading a prompt needs a client attached to this machine's daemon", "info", m.Settings.NotificationDuration)
		return nil
	}
	m.Inbox.peekGen++
	m.Inbox.Peek = &inboxPeek{Item: it, gen: m.Inbox.peekGen}
	return m.inboxPeekRead()
}

// inboxPeekRead reads the open peek's prompt again.
func (m *OS) inboxPeekRead() tea.Cmd {
	p := m.Inbox.Peek
	if p == nil {
		return nil
	}
	p.Loading = true
	p.Err = ""
	call, gen := m.inboxCaller(), p.gen
	params := map[string]any{"session": p.Item.Session, "window": p.Item.Window}
	return func() tea.Msg {
		raw, err := call("peek-prompt", params, inboxPeekTimeout)
		if err != nil {
			return InboxPeekMsg{Gen: gen, Err: err}
		}
		var pk session.PromptPeek
		if err := json.Unmarshal(raw, &pk); err != nil {
			return InboxPeekMsg{Gen: gen, Err: err}
		}
		return InboxPeekMsg{Gen: gen, Peek: &pk}
	}
}

// InboxPeekRefresh reads the prompt again, keeping the note.
func (m *OS) InboxPeekRefresh() tea.Cmd {
	if p := m.Inbox.Peek; p != nil && !p.Sending {
		return m.inboxPeekRead()
	}
	return nil
}

// InboxPeekBack closes the peek and shows the list again.
func (m *OS) InboxPeekBack() {
	m.Inbox.Peek = nil
}

// InboxPeekGo closes the Inbox and goes to the peeked pane, for a prompt
// better answered there.
func (m *OS) InboxPeekGo() {
	p := m.Inbox.Peek
	if p == nil {
		return
	}
	it := p.Item
	m.Inbox.Peek = nil
	m.CloseInbox()
	m.inboxJump(it)
}

// applyInboxPeek takes a peek-prompt reply for the open peek.
func (m *OS) applyInboxPeek(msg InboxPeekMsg) {
	p := m.Inbox.Peek
	if p == nil || p.gen != msg.Gen {
		return
	}
	p.Loading = false
	if msg.Err != nil {
		p.Err = "Could not read the prompt: " + msg.Err.Error()
		return
	}
	p.Peek = msg.Peek
}

// InboxAnswer answers the peeked prompt with action, and value for choose and
// text. It refuses, without calling the daemon, what the peek does not offer
// and any key that did not come from the keyboard.
func (m *OS) InboxAnswer(action, value string) tea.Cmd {
	p := m.Inbox.Peek
	if p == nil || p.Sending || p.Loading {
		return nil
	}
	if m.ProcessingRemoteKeys || (action == harness.ActionText && p.DraftAutomated) {
		p.Err = "An answer that send-keys typed is not sent: only a key from your keyboard answers a prompt."
		return nil
	}
	pk := p.Peek
	if pk == nil || !pk.Found {
		p.Err = "There is no prompt to answer. r reads the pane again."
		return nil
	}
	if !pk.Offers(action) {
		p.Err = inboxNotOffered(pk, action)
		return nil
	}
	if action == harness.ActionChoose && !inboxHasOption(pk, value) {
		p.Err = "No option " + value + " is on the screen."
		return nil
	}
	nonce := m.inboxNonce()
	if nonce == "" {
		p.Err = "This daemon issued no attach nonce, so it cannot tell you from an agent. Update the daemon."
		return nil
	}
	// An allow of a risky call takes a second press of the same key, and
	// carries the rules it matched. See inbox_approvals_ext.go.
	ack, send := m.inboxPeekRiskGate(p, action, value)
	if !send {
		return nil
	}
	p.Sending = true
	p.Err, p.Note = "", ""
	params := map[string]any{
		"session":     p.Item.Session,
		"window":      p.Item.Window,
		"action":      action,
		"prompt_id":   pk.PromptID,
		"human_nonce": nonce,
		"timeout":     inboxRespondWait,
	}
	if value != "" {
		params["value"] = value
	}
	if len(ack) > 0 {
		params["risk_ack"] = ack
	}
	call, gen := m.inboxCaller(), p.gen
	return func() tea.Msg {
		raw, err := call("respond", params, inboxRespondTimeout)
		if err != nil {
			return InboxRespondedMsg{Gen: gen, Action: action, Err: err}
		}
		var res session.PromptResponse
		if err := json.Unmarshal(raw, &res); err != nil {
			return InboxRespondedMsg{Gen: gen, Action: action, Err: err}
		}
		return InboxRespondedMsg{Gen: gen, Action: action, Res: &res}
	}
}

// inboxNotOffered says why an action is not offered on a prompt.
func inboxNotOffered(pk *session.PromptPeek, action string) string {
	if len(pk.Actions) == 0 {
		why := strings.TrimSpace(pk.Reason)
		if why == "" {
			why = "the harness declares no answers for this prompt"
		}
		return capitalize(why) + ". Enter goes to the pane."
	}
	return "This prompt does not take " + strings.ReplaceAll(action, "_", " ") + ". It takes " + strings.Join(pk.Actions, ", ") + "."
}

// inboxHasOption reports whether the peek shows option value.
func inboxHasOption(pk *session.PromptPeek, value string) bool {
	n, err := strconv.Atoi(value)
	if err != nil {
		return false
	}
	for _, o := range pk.Options {
		if o.N == n {
			return true
		}
	}
	return false
}

// capitalize upper-cases the first letter of a sentence.
func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// applyInboxResponded takes a respond reply. An answer that landed closes the
// peek and says where the pane went. A prompt that changed under the answer
// is read again, so the person sees what is there now.
func (m *OS) applyInboxResponded(msg InboxRespondedMsg) tea.Cmd {
	p := m.Inbox.Peek
	if p == nil || p.gen != msg.Gen {
		return nil
	}
	p.Sending = false
	if msg.Err != nil {
		var callErr *session.VerbCallError
		if errors.As(msg.Err, &callErr) && callErr.Code == session.ErrVerbPromptChanged {
			p.Note = "The prompt changed before your answer, or another client answered it, so nothing was pressed. This is the prompt now."
			p.Composing, p.Draft = false, ""
			return m.inboxPeekRead()
		}
		p.Err = "The answer did not go through: " + msg.Err.Error()
		return nil
	}
	who := inboxWho(p.Item)
	text := "Sent " + inboxSentWords(msg.Res.Sent) + " to " + who
	switch msg.Res.SettledBy {
	case "timeout":
		text += "; the prompt is still on the screen"
	case "gone":
		text += "; the pane has closed"
	case "prompt":
		text += "; the prompt is gone"
	default:
		text += "; it is " + sidebarStateWords(msg.Res.State) + " now"
	}
	m.Inbox.Peek = nil
	m.ShowNotification(text, "info", m.Settings.NotificationDuration)
	return nil
}

// inboxSentWords is what respond pressed, as a toast says it.
func inboxSentWords(sent string) string {
	if sent == "text" {
		return "your answer"
	}
	return strconv.Quote(sent)
}

// InboxPeekStartText opens the text line, when the prompt takes one.
func (m *OS) InboxPeekStartText() {
	p := m.Inbox.Peek
	if p == nil || p.Sending {
		return
	}
	if p.Peek == nil || !p.Peek.Offers(harness.ActionText) {
		if p.Peek != nil {
			p.Err = inboxNotOffered(p.Peek, harness.ActionText)
		}
		return
	}
	p.Composing, p.Draft, p.Err = true, "", ""
	p.DraftAutomated = m.ProcessingRemoteKeys
}

// InboxPeekCancelText closes the text line and drops the draft.
func (m *OS) InboxPeekCancelText() {
	if p := m.Inbox.Peek; p != nil {
		p.Composing, p.Draft, p.DraftAutomated = false, "", false
	}
}

// InboxPeekType appends typed text to the draft.
func (m *OS) InboxPeekType(text string) {
	p := m.Inbox.Peek
	if p == nil || !p.Composing || text == "" {
		return
	}
	if m.ProcessingRemoteKeys {
		p.DraftAutomated = true
	}
	p.Draft += text
}

// InboxPeekBackspace removes the last rune of the draft.
func (m *OS) InboxPeekBackspace() {
	p := m.Inbox.Peek
	if p == nil || !p.Composing || p.Draft == "" {
		return
	}
	if m.ProcessingRemoteKeys {
		p.DraftAutomated = true
	}
	r := []rune(p.Draft)
	p.Draft = string(r[:len(r)-1])
}

// InboxPeekSendText sends the draft as the answer.
func (m *OS) InboxPeekSendText() tea.Cmd {
	p := m.Inbox.Peek
	if p == nil || !p.Composing || strings.TrimSpace(p.Draft) == "" {
		return nil
	}
	cmd := m.InboxAnswer(harness.ActionText, p.Draft)
	if cmd != nil {
		p.Composing = false
	}
	return cmd
}
