package app

import (
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// Replying to an agent: a one-line editor under the Inbox list that queues a
// message with queue-prompt, typed the moment the agent comes to rest, and
// the queue's length at the right edge of the agent's rail row.
//
// r opens it on a Finished or Errored item in the Inbox, and on a rail agent
// row whose pane is not waiting on a prompt; from the rail the Inbox opens
// with it and closes again once the reply is sent or cancelled. enter queues
// the text, as the person, with this client's attach nonce: the daemon types
// it when the agent is at rest (see session/agent_queue.go), so a reply to a
// busy agent waits rather than landing in the middle of its turn, and a reply
// never answers a prompt, which the daemon checks again right before it types.
//
// x on a rail row with a queue drops the newest message still waiting. A drop
// of a message this client queued can be undone for inboxUndoWindow with u on
// the same row, which queues its text again.
//
// A key that did not come from the keyboard never sends: a draft send-keys
// touched is refused, as the deny reason and the mail reply refuse one, since
// a queued message from the person is typed without any check of the grants
// of whoever drove the keys. For the same reason such a key never drops a
// queued message with x or undoes a drop with u: both act as the person, with
// the attach nonce.

// inboxReplyMax bounds a reply, the daemon's bound for one queued message.
const inboxReplyMax = 16 << 10

// inboxReplyTimeout bounds one queue verb call.
const inboxReplyTimeout = 5 * time.Second

// inboxRemoteQueueRefusal is what the dock says when a key that did not come
// from the keyboard tries to drop a queued message or undo a drop.
const inboxRemoteQueueRefusal = "A key from send-keys does not change the queue: only keys from your keyboard queue or drop a message as you"

// inboxReplySentMax is how many sent replies this client keeps the text of,
// so a drop of one can be undone.
const inboxReplySentMax = 16

// inboxReplyState is the reply editor and what it remembers.
type inboxReplyState struct {
	// editor is the open editor, nil when none is.
	editor *inboxReplyEditor
	// sent is the text of the replies this client queued, by entry id.
	sent map[string]string
	// sentOrder is sent's ids, oldest first, for the bound.
	sentOrder []string
	// dropped is the last message x dropped, for u on the same row.
	dropped *inboxReplyDrop
	// noQueue is set once the daemon answered queue-prompt with
	// unknown_verb: it is older than the queue, and the reply keys then do
	// what they did before they were bound.
	noQueue bool
}

// inboxReplyEditor is one reply being typed.
type inboxReplyEditor struct {
	session string
	window  string
	who     string
	// context says where the agent is, as the editor's label shows it:
	// "done 4m", "working".
	context string
	draft   string
	// automated is set when a key that did not come from the keyboard
	// touched the draft. Such a draft is never sent.
	automated bool
	// closeAfter closes the Inbox when the editor closes, for an editor the
	// rail opened.
	closeAfter bool
}

// inboxReplyDrop is one dropped message, for undo.
type inboxReplyDrop struct {
	session, window, who, text string
	at                         time.Time
}

// InboxRepliedMsg is the answer to a queue-prompt the reply editor sent.
type InboxRepliedMsg struct {
	Session, Window, Who, Text string
	ID                         string
	Position                   int
	Queued                     int
	Delivering                 bool
	// Requeue marks a reply queued again by an undo of a drop.
	Requeue bool
	Err     error
}

// InboxQueueDroppedMsg is the answer to the x on a rail row.
type InboxQueueDroppedMsg struct {
	Session, Window, Who string
	// ID is the entry dropped, empty when there was none to drop.
	ID string
	// Left is how many messages still wait.
	Left int
	Err  error
}

// inboxReplySupported reports whether the daemon holding the Inbox can take
// queue-prompt, as far as this client knows.
func (m *OS) inboxReplySupported() bool {
	return !m.Inbox.reply.noQueue
}

// inboxReplyKind reports whether an Inbox item is one r replies to with the
// editor: a finished turn or an error, of a pane on this machine.
func inboxReplyKind(it session.AttentionItem) bool {
	return (it.Kind == session.AttentionFinished || it.Kind == session.AttentionErrored) && it.Window != ""
}

// inboxReplyAgent opens the reply editor for a finished or errored item. ok
// is false when it did not, and InboxReply then answers as it always has.
func (m *OS) inboxReplyAgent(it session.AttentionItem) (cmd tea.Cmd, ok bool) {
	if !inboxReplyKind(it) || !m.inboxReplySupported() {
		return nil, false
	}
	if it.Host != "" {
		m.ShowNotification(inboxWho(it)+" is on "+printableTitle(it.Host)+". Attach there to reply", "info", m.Settings.NotificationDuration)
		return nil, true
	}
	context := "done"
	if it.Kind == session.AttentionErrored {
		context = "errored"
	}
	context += " " + inboxWait(it.Since, time.Now())
	m.openInboxReply(it.Session, it.Window, inboxWho(it), context, false)
	return nil, true
}

// openInboxReply opens the editor for a pane, refusing a pane that waits on a
// prompt: a reply queued for it would sit until the prompt is answered, and
// the person should answer the prompt first.
func (m *OS) openInboxReply(sessionID, windowID, who, context string, closeAfter bool) bool {
	// The state the rail draws, so a pane with an ask-human question open
	// is refused like one on a prompt, as its row reads.
	if state, seq, _, ok := m.railPane(sessionID, windowID); ok {
		if drawn, _ := m.railAgentState(windowID, state, seq); drawn == "needs_input" {
			m.ShowNotification(who+" is waiting on a prompt. Answer it first ("+m.inboxKeyOr(config.ActionInboxPeek, "space")+" to peek).", "info", m.Settings.NotificationDuration)
			return false
		}
	}
	if !m.inboxCanMark("Replying") {
		return false
	}
	if sessionID == "" {
		sessionID = m.sidebarCurrentSessionID()
	}
	m.Inbox.approvals.armed = inboxArmed{}
	m.Inbox.reply.editor = &inboxReplyEditor{
		session:    sessionID,
		window:     windowID,
		who:        who,
		context:    context,
		automated:  m.ProcessingRemoteKeys,
		closeAfter: closeAfter,
	}
	return true
}

// SidebarAgentReply opens the reply editor for a rail agent row's pane. The
// Inbox opens under it on the pane's item, when it has one, and closes again
// when the reply is sent or cancelled.
func (m *OS) SidebarAgentReply(sessionID, windowID string) (tea.Cmd, bool) {
	if !m.inboxReplySupported() {
		return nil, false
	}
	state, _, label, ok := m.railPane(sessionID, windowID)
	if !ok {
		m.ShowNotification("That pane is on another machine. Attach there to reply", "info", m.Settings.NotificationDuration)
		return nil, true
	}
	who := printableTitle(label)
	if who == "" {
		who = shortWindowLabel(windowID)
	}
	context := sidebarStateWords(state)
	if state == "" {
		context = ""
	}
	wasOpen := m.ShowInbox
	if !m.openInboxReply(sessionID, windowID, who, context, !wasOpen) {
		return nil, true
	}
	if !wasOpen {
		m.OpenInbox("")
	}
	for _, it := range m.Inbox.Items {
		if it.Host == "" && it.Window == windowID && (sessionID == "" || it.Session == sessionID) {
			m.Inbox.SelectedID = it.ID
			m.clampInboxSelection()
			break
		}
	}
	return nil, true
}

// InboxReplyOpen reports whether the reply editor is open, where every
// printable key is text.
func (m *OS) InboxReplyOpen() bool {
	return m.ShowInbox && m.Inbox.reply.editor != nil
}

// InboxReplyType appends typed text to the reply.
func (m *OS) InboxReplyType(text string) {
	ed := m.Inbox.reply.editor
	if ed == nil || text == "" {
		return
	}
	if m.ProcessingRemoteKeys {
		ed.automated = true
	}
	for _, ch := range text {
		if len(ed.draft)+len(string(ch)) > inboxReplyMax || !printableRune(ch, false) {
			continue
		}
		ed.draft += string(ch)
	}
}

// InboxReplyBackspace removes the last rune of the reply.
func (m *OS) InboxReplyBackspace() {
	ed := m.Inbox.reply.editor
	if ed == nil || ed.draft == "" {
		return
	}
	if m.ProcessingRemoteKeys {
		ed.automated = true
	}
	rs := []rune(ed.draft)
	ed.draft = string(rs[:len(rs)-1])
}

// InboxReplyCancel closes the editor and drops what was typed.
func (m *OS) InboxReplyCancel() {
	ed := m.Inbox.reply.editor
	m.Inbox.reply.editor = nil
	if ed != nil && ed.closeAfter {
		m.CloseInbox()
	}
}

// InboxReplySend queues the reply. An empty reply sends nothing and keeps
// the editor open.
func (m *OS) InboxReplySend() tea.Cmd {
	ed := m.Inbox.reply.editor
	if ed == nil {
		return nil
	}
	text := strings.TrimSpace(ed.draft)
	if text == "" {
		return nil
	}
	if ed.automated || m.ProcessingRemoteKeys {
		m.InboxReplyCancel()
		m.ShowNotification("A reply that send-keys typed is not sent: only keys from your keyboard queue a message as you", "error", m.Settings.NotificationDuration)
		return nil
	}
	nonce := m.inboxNonce()
	if nonce == "" {
		m.ShowNotification("This daemon issued no attach nonce, so it cannot tell you from an agent. Update the daemon", "error", m.Settings.NotificationDuration*2)
		return nil
	}
	m.InboxReplyCancel()
	return m.inboxQueueCmd(ed.session, ed.window, ed.who, text, nonce, false)
}

// inboxQueueCmd sends queue-prompt as the person.
func (m *OS) inboxQueueCmd(sessionID, windowID, who, text, nonce string, requeue bool) tea.Cmd {
	call := m.inboxCaller()
	params := map[string]any{"session": sessionID, "window": windowID, "text": text, "human_nonce": nonce}
	return func() tea.Msg {
		msg := InboxRepliedMsg{Session: sessionID, Window: windowID, Who: who, Text: text, Requeue: requeue}
		raw, err := call("queue-prompt", params, inboxReplyTimeout)
		if err != nil {
			msg.Err = err
			return msg
		}
		var res struct {
			ID         string `json:"id"`
			Position   int    `json:"position"`
			Queued     int    `json:"queued"`
			Delivering bool   `json:"delivering"`
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			msg.Err = err
			return msg
		}
		msg.ID, msg.Position, msg.Queued, msg.Delivering = res.ID, res.Position, res.Queued, res.Delivering
		return msg
	}
}

// applyInboxReplied says what happened to a reply, and keeps its text so a
// drop of it can be undone.
func (m *OS) applyInboxReplied(msg InboxRepliedMsg) {
	if msg.Err != nil {
		var callErr *session.VerbCallError
		if errors.As(msg.Err, &callErr) {
			switch callErr.Code {
			case session.ErrVerbUnknownVerb:
				m.Inbox.reply.noQueue = true
				m.ShowNotification("This daemon cannot queue a reply. Restart it with a newer tuios: tuios kill-server", "error", m.Settings.NotificationDuration*2)
				return
			case session.ErrVerbQueueFull:
				m.ShowNotification(msg.Who+" already has as many messages queued as [agents.queue] max allows. Nothing was queued", "error", m.Settings.NotificationDuration*2)
				return
			}
		}
		m.ShowNotification("The reply to "+msg.Who+" was not queued: "+msg.Err.Error(), "error", m.Settings.NotificationDuration*2)
		return
	}
	m.rememberInboxReply(msg.ID, msg.Text)
	switch {
	case msg.Delivering:
		m.ShowNotification("Typing your reply to "+msg.Who, "success", m.Settings.NotificationDuration)
	case msg.Queued <= 1:
		m.ShowNotification("Queued for "+msg.Who+". It is typed when "+msg.Who+" is at rest", "info", m.Settings.NotificationDuration)
	default:
		m.ShowNotification("Queued for "+msg.Who+", "+strconv.Itoa(msg.Position)+" of "+strconv.Itoa(msg.Queued)+". Each is typed at a rest", "info", m.Settings.NotificationDuration)
	}
}

// rememberInboxReply keeps a sent reply's text by its entry id.
func (m *OS) rememberInboxReply(id, text string) {
	if id == "" {
		return
	}
	st := &m.Inbox.reply
	if st.sent == nil {
		st.sent = make(map[string]string)
	}
	if _, ok := st.sent[id]; !ok {
		st.sentOrder = append(st.sentOrder, id)
	}
	st.sent[id] = text
	for len(st.sentOrder) > inboxReplySentMax {
		delete(st.sent, st.sentOrder[0])
		st.sentOrder = st.sentOrder[1:]
	}
}

// railQueued is how many messages wait for a pane of this machine, as the
// rail knows it.
func (m *OS) railQueued(sessionID, windowID string) int {
	if sessionID == "" || sessionID == m.sidebarCurrentSessionID() {
		for _, w := range m.Windows {
			if w != nil && w.ID == windowID {
				return w.AgentQueued
			}
		}
	}
	if m.DaemonClient == nil || m.AttachedHost != "" {
		return 0
	}
	for _, w := range m.DaemonClient.SessionWindows(sessionID) {
		if w.ID == windowID {
			return w.AgentQueued
		}
	}
	return 0
}

// SidebarAgentCancelQueued drops the newest queued message of a rail agent
// row's pane. A row with nothing queued answers false, and x does what it
// does on the rail.
func (m *OS) SidebarAgentCancelQueued(sessionID, windowID string) (tea.Cmd, bool) {
	if !m.inboxReplySupported() || m.railQueued(sessionID, windowID) == 0 {
		return nil, false
	}
	// Dropping a message cancels it as the person, with the attach nonce,
	// which may take back a message the person queued. Only keys from the
	// keyboard do that.
	if m.ProcessingRemoteKeys {
		m.ShowNotification(inboxRemoteQueueRefusal, "error", m.Settings.NotificationDuration)
		return nil, true
	}
	if !m.inboxCanMark("Dropping a queued message") {
		return nil, true
	}
	nonce := m.inboxNonce()
	if nonce == "" {
		m.ShowNotification("This daemon issued no attach nonce, so it cannot tell you from an agent. Update the daemon", "error", m.Settings.NotificationDuration*2)
		return nil, true
	}
	if sessionID == "" {
		sessionID = m.sidebarCurrentSessionID()
	}
	_, _, label, _ := m.railPane(sessionID, windowID)
	who := printableTitle(label)
	if who == "" {
		who = shortWindowLabel(windowID)
	}
	call := m.inboxCaller()
	return func() tea.Msg {
		msg := InboxQueueDroppedMsg{Session: sessionID, Window: windowID, Who: who}
		raw, err := call("list-queued", map[string]any{"session": sessionID, "window": windowID}, inboxReplyTimeout)
		if err != nil {
			msg.Err = err
			return msg
		}
		var list struct {
			Entries []struct {
				ID    string `json:"id"`
				State string `json:"state"`
			} `json:"entries"`
		}
		if err := json.Unmarshal(raw, &list); err != nil {
			msg.Err = err
			return msg
		}
		// The newest one still waiting. One being typed cannot be taken
		// back.
		id := ""
		for i := len(list.Entries) - 1; i >= 0; i-- {
			if list.Entries[i].State != "delivering" {
				id = list.Entries[i].ID
				break
			}
		}
		if id == "" {
			return msg
		}
		raw, err = call("cancel-queued", map[string]any{"session": sessionID, "window": windowID, "id": id, "human_nonce": nonce}, inboxReplyTimeout)
		if err != nil {
			msg.Err = err
			return msg
		}
		var res struct {
			Queued int `json:"queued"`
		}
		_ = json.Unmarshal(raw, &res)
		msg.ID, msg.Left = id, res.Queued
		return msg
	}, true
}

// applyInboxQueueDropped says what x did, and keeps the text of a message
// this client queued for u.
func (m *OS) applyInboxQueueDropped(msg InboxQueueDroppedMsg) {
	if msg.Err != nil {
		var callErr *session.VerbCallError
		if errors.As(msg.Err, &callErr) && callErr.Code == session.ErrVerbUnknownVerb {
			m.Inbox.reply.noQueue = true
			m.ShowNotification("This daemon has no queue. Restart it with a newer tuios: tuios kill-server", "error", m.Settings.NotificationDuration*2)
			return
		}
		m.ShowNotification("Nothing was dropped: "+msg.Err.Error(), "error", m.Settings.NotificationDuration*2)
		return
	}
	if msg.ID == "" {
		m.ShowNotification("Nothing queued for "+msg.Who+" can be dropped: a message being typed cannot be taken back", "info", m.Settings.NotificationDuration)
		return
	}
	st := &m.Inbox.reply
	text, known := st.sent[msg.ID]
	if !known {
		st.dropped = nil
		m.ShowNotification("Dropped 1 queued message to "+msg.Who, "info", m.Settings.NotificationDuration)
		return
	}
	delete(st.sent, msg.ID)
	st.sentOrder = slices.DeleteFunc(st.sentOrder, func(id string) bool { return id == msg.ID })
	st.dropped = &inboxReplyDrop{session: msg.Session, window: msg.Window, who: msg.Who, text: text, at: time.Now()}
	key := m.sidebarAgentKeyOr(config.ActionAgentUnread, "u")
	m.ShowNotification("Dropped 1 queued message to "+msg.Who+". "+key+" undoes.", "info", inboxUndoNotice)
}

// sidebarAgentUndoDrop queues again the message x dropped from this row a
// moment ago. ok is false when there is none, and u then marks the row
// unread as it does.
func (m *OS) sidebarAgentUndoDrop(sessionID, windowID string) (tea.Cmd, bool) {
	st := &m.Inbox.reply
	d := st.dropped
	if d == nil || time.Since(d.at) >= inboxUndoWindow || d.window != windowID || (sessionID != "" && d.session != sessionID) {
		return nil, false
	}
	// The undo queues the text as the person, with the attach nonce, so a
	// key from send-keys or a tape must not do it, as InboxReplySend
	// refuses one. The drop stays, for the person's own u.
	if m.ProcessingRemoteKeys {
		m.ShowNotification(inboxRemoteQueueRefusal, "error", m.Settings.NotificationDuration)
		return nil, true
	}
	st.dropped = nil
	nonce := m.inboxNonce()
	if nonce == "" {
		return nil, false
	}
	return m.inboxQueueCmd(d.session, d.window, d.who, d.text, nonce, true), true
}

// sidebarAgentKeyOr is the key an agent row action is bound to, or fallback.
func (m *OS) sidebarAgentKeyOr(action, fallback string) string {
	reg := m.KeybindRegistry
	if reg == nil {
		reg = defaultInboxKeys()
	}
	if presses := config.PressesByAction(reg)[action]; len(presses) > 0 {
		return presses[0]
	}
	return fallback
}

// inboxReplyDetail is the editor under the list and its hints, when it is
// open.
func (m *OS) inboxReplyDetail() (detail func(width int) []string, hints []overlay.Hint, ok bool) {
	ed := m.Inbox.reply.editor
	if ed == nil {
		return nil, nil, false
	}
	detail = func(width int) []string { return inboxReplyLines(ed, width) }
	hints = []overlay.Hint{{Key: overlay.EnterKey(), Label: "send when ready"}, {Key: "esc", Label: "cancel"}}
	return detail, hints, true
}

// inboxReplyEditorLines bounds how many lines the draft takes under the list.
const inboxReplyEditorLines = 4

// inboxReplyLines draws the editor: who it is for and where that agent is,
// then the draft with the cursor, its last lines when it is long.
func inboxReplyLines(ed *inboxReplyEditor, width int) []string {
	width = max(width-2, 8)
	label := "Reply to " + ed.who
	if ed.context != "" {
		label += " (" + ed.context + ")"
	}
	text := label + ": " + printableRunes(ed.draft) + "_"
	lines := wrapPlain(text, width)
	if len(lines) > inboxReplyEditorLines {
		lines = lines[len(lines)-inboxReplyEditorLines:]
	}
	for i := range lines {
		lines[i] = "  " + lines[i]
	}
	return lines
}

// sidebarAgentQueuedFigure is what a rail agent row shows at its right edge
// in place of the elapsed time while messages wait in the pane's queue, empty
// for none. A row that needs you keeps its wait there: how long it has waited
// on you is the figure it must not lose, and nothing queued is typed until it
// is answered anyway.
func (m *OS) sidebarAgentQueuedFigure(e sidebarAgentEntry) string {
	if e.Queued <= 0 || sidebarAgentGroup(e.State, e.DoneSeen) == sidebarGroupNeedsYou {
		return ""
	}
	return strconv.Itoa(e.Queued) + " queued"
}

// sidebarAgentQueuedFigures are the forms the queued figure takes on a row,
// longest first: the words, then the count and a q. Empty when the row shows
// no figure.
func (m *OS) sidebarAgentQueuedFigures(e sidebarAgentEntry) []string {
	full := m.sidebarAgentQueuedFigure(e)
	if full == "" {
		return nil
	}
	return []string{full, strconv.Itoa(e.Queued) + "q"}
}
