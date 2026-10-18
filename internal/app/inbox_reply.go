package app

import (
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/composer"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// Replying to an agent: the composer under the Inbox list (internal/composer)
// that queues a message with queue-prompt, typed the moment the agent comes
// to rest, and the queue's length at the right edge of the agent's rail row.
//
// The composer keeps line breaks and holds a long paste as one block. The
// blocks are put back in their place before the message is queued, so the
// daemon gets plain text, as it always has, within its 16 KiB bound. esc
// keeps the draft for the pane, and r on the same pane brings it back.
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
// so a drop of one can be undone. inboxReplySentMaxBytes bounds what they
// hold: a reply can carry 8 MB of images, and 16 of them would keep 128 MB
// in the client for an undo that is rarely used. The oldest go first, and
// the newest is always kept.
const (
	inboxReplySentMax      = 16
	inboxReplySentMaxBytes = 16 << 20
)

// inboxReplyState is the reply editor and what it remembers.
type inboxReplyState struct {
	// editor is the open editor, nil when none is.
	editor *inboxReplyEditor
	// sent is the replies this client queued, by entry id.
	sent map[string]inboxSentReply
	// sentOrder is sent's ids, oldest first, for the bound.
	sentOrder []string
	// sentBytes is what sent holds, by inboxSentReply.size.
	sentBytes int
	// dropped is the last message x dropped, for u on the same row.
	dropped *inboxReplyDrop
	// noQueue is set once the daemon answered queue-prompt with
	// unknown_verb: it is older than the queue, and the reply keys then do
	// what they did before they were bound.
	noQueue bool
	// drafts are the drafts esc closed, by pane, so r on the same pane
	// opens them again. draftOrder is their keys, oldest first, for the
	// bound.
	drafts     map[string]*composer.Model
	draftOrder []string
	// bulkDraft is the draft of a bulk reply that esc or ctrl+] closed, so
	// the next bulk reply opens it again. nil when there is none.
	bulkDraft *composer.Model
	// sending are the drafts of replies out to the daemon, by send, so a
	// reply the daemon refuses keeps its draft. Two replies to one pane can
	// be out at once, so the key is the send and not the pane.
	sending map[uint64]*composer.Model
	// sendSeq numbers the sends.
	sendSeq uint64
}

// inboxReplyDraftsMax is how many closed drafts the client keeps. They live
// in memory only and end with the client.
const inboxReplyDraftsMax = 16

// inboxReplyRows is how many rows the editor grows to before it scrolls.
const inboxReplyRows = 8

// inboxDraftKey is the key of a pane's draft.
func inboxDraftKey(sessionID, windowID string) string { return sessionID + "/" + windowID }

// inboxReplyEditor is one reply being typed.
type inboxReplyEditor struct {
	session string
	window  string
	// host is the linked machine the pane is on, empty for this one. The
	// reply then goes with host, and this machine's daemon passes it on.
	host string
	who  string
	// context says where the agent is, as the editor's label shows it:
	// "done 4m", "working".
	context string
	// comp is the draft. It records a key that did not come from the
	// keyboard, and such a draft is never sent.
	comp *composer.Model
	// closeAfter closes the Inbox when the editor closes, for an editor the
	// rail opened.
	closeAfter bool
	// targets are the agents a bulk reply goes to, nil for one agent. See
	// inbox_bulk.go.
	targets []replyTarget
	// draftWires is the previewer's, the @ list's and the history's state.
	// See draft_wiring.go.
	draftWires
}

// inboxSentReply is a reply this client queued, kept so a drop of it can be
// undone: its text, and its parts when it was sent in parts.
type inboxSentReply struct {
	text  string
	parts []composer.Part
}

// size is about the bytes r keeps: its text and each part's text and image.
func (r inboxSentReply) size() int {
	n := len(r.text)
	for _, p := range r.parts {
		n += len(p.Text)
		if p.Image != nil {
			n += len(p.Image.Data)
		}
	}
	return n
}

// inboxReplyDrop is one dropped message, for undo.
type inboxReplyDrop struct {
	session, window, who string
	reply                inboxSentReply
	at                   time.Time
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
	// Send is the number of the send whose draft sending holds, 0 for none.
	Send uint64
	// Parts is the reply in parts, nil for one sent as text.
	Parts []composer.Part
	Err   error
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
	context := "done"
	if it.Kind == session.AttentionErrored {
		context = "errored"
	}
	context += " " + inboxWait(it.Since, time.Now())
	if it.Host != "" {
		m.openHostReply(it.Host, it.Session, it.Window, inboxWho(it), context)
		return nil, true
	}
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
	comp := m.takeInboxDraft(sessionID, windowID)
	if comp == nil {
		comp = composer.New(composer.Options{MaxRows: inboxReplyRows, Placeholder: "Type your reply.", Images: true})
	}
	m.Inbox.reply.editor = &inboxReplyEditor{
		draftWires: m.wireDraft(comp, sessionID, windowID, true),
		session:    sessionID,
		window:     windowID,
		who:        who,
		context:    context,
		comp:       comp,
		closeAfter: closeAfter,
	}
	return true
}

// openHostReply opens the editor for a pane on a linked machine. The reply
// goes to this machine's daemon with host, which passes it on (see
// session/queue_host.go). The far daemon refuses it while the agent waits on
// a prompt, as it refuses any queued message then.
func (m *OS) openHostReply(host, sessionID, windowID, who, context string) bool {
	if sessionID == "" {
		return false
	}
	if !m.inboxCanMark("Replying") {
		return false
	}
	m.Inbox.approvals.armed = inboxArmed{}
	comp := m.takeInboxDraft(sessionID, windowID)
	if comp == nil {
		comp = composer.New(composer.Options{MaxRows: inboxReplyRows, Placeholder: "Type your reply.", Images: true})
	}
	// The @ list offers no files of the far pane's folder. The history is
	// kept under the far pane, as before.
	wires := m.wireDraft(comp, "", "", false)
	wires.histSession, wires.histWindow = sessionID, windowID
	m.Inbox.reply.editor = &inboxReplyEditor{
		draftWires: wires,
		session:    sessionID,
		window:     windowID,
		host:       host,
		who:        who + " on " + printableTitle(host),
		context:    context,
		comp:       comp,
	}
	return true
}

// takeInboxDraft takes the kept draft of a pane out of the store, nil when
// there is none.
func (m *OS) takeInboxDraft(sessionID, windowID string) *composer.Model {
	st := &m.Inbox.reply
	k := inboxDraftKey(sessionID, windowID)
	comp, ok := st.drafts[k]
	if !ok {
		return nil
	}
	delete(st.drafts, k)
	st.draftOrder = slices.DeleteFunc(st.draftOrder, func(o string) bool { return o == k })
	return comp
}

// keepInboxDraft stores a pane's draft for the next r on it. The oldest
// draft goes past the bound.
func (m *OS) keepInboxDraft(sessionID, windowID string, comp *composer.Model) {
	st := &m.Inbox.reply
	k := inboxDraftKey(sessionID, windowID)
	if st.drafts == nil {
		st.drafts = make(map[string]*composer.Model)
	}
	if _, ok := st.drafts[k]; !ok {
		st.draftOrder = append(st.draftOrder, k)
	}
	st.drafts[k] = comp
	for len(st.draftOrder) > inboxReplyDraftsMax {
		delete(st.drafts, st.draftOrder[0])
		st.draftOrder = st.draftOrder[1:]
	}
}

// restoreNotice puts a line under the draft of a reply the daemon refused, in
// the open editor when it is that pane's, and in the dock otherwise.
func (m *OS) restoreNotice(msg InboxRepliedMsg, notice string) {
	if ed := m.Inbox.reply.editor; ed != nil && ed.session == msg.Session && ed.window == msg.Window {
		ed.comp.SetNotice(notice)
		return
	}
	m.ShowNotification(notice+" Press r on "+msg.Who+" to open the draft.", "error", m.Settings.NotificationDuration*2)
}

// restoreInboxDraft gives a pane back the draft of a reply the daemon did not
// take. The person may have started a new draft for the pane while the reply
// was out: the editor may be open on it, or esc may have kept it. The draft
// that was not sent then goes at the top of that one, so neither is lost.
func (m *OS) restoreInboxDraft(sessionID, windowID string, comp *composer.Model) {
	if ed := m.Inbox.reply.editor; ed != nil && ed.session == sessionID && ed.window == windowID {
		ed.comp.PutBack(comp)
		ed.comp.SetNotice("Your reply that was not sent is back at the top.")
		return
	}
	if kept := m.takeInboxDraft(sessionID, windowID); kept != nil {
		kept.PutBack(comp)
		comp = kept
	}
	m.keepInboxDraft(sessionID, windowID, comp)
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

// InboxReplyType types text into the reply at the cursor.
func (m *OS) InboxReplyType(text string) {
	if ed := m.Inbox.reply.editor; ed != nil {
		ed.comp.Insert(text, m.ProcessingRemoteKeys)
	}
}

// InboxReplyPaste puts a paste into the reply: as text when it is short, as
// one block when it is long. Line breaks are kept. An empty paste looks for a
// clipboard image, and a paste that is the path of an image file attaches the
// image (inbox_reply_image.go): the command does that work.
func (m *OS) InboxReplyPaste(text string) tea.Cmd {
	ed := m.Inbox.reply.editor
	if ed == nil {
		return nil
	}
	return m.inboxReplyPasteCmd(ed, text)
}

// InboxReplyKey gives a key to the reply editor and does what it asks.
func (m *OS) InboxReplyKey(msg tea.KeyPressMsg) tea.Cmd {
	ed := m.Inbox.reply.editor
	if ed == nil {
		return nil
	}
	var cmd tea.Cmd
	switch ed.comp.Update(msg, m.ProcessingRemoteKeys) {
	case composer.EventSend:
		cmd = m.InboxReplySend()
	case composer.EventClose:
		m.InboxReplyCancel()
	case composer.EventPaste:
		cmd = m.inboxReplyClipboard(ed)
	case composer.EventOpenImage:
		cmd = m.replyOpenImage(ed.comp.OpenedImage())
	case composer.EventEditor:
		if m.ProcessingRemoteKeys {
			ed.comp.SetNotice("A key from send-keys does not open the editor.")
		} else {
			cmd = m.replyEditorCmd(ed.comp)
		}
	case composer.EventCleared:
		// A draft cleared by mistake is not lost: it goes to the history,
		// unless keys from outside the keyboard touched it.
		if !ed.comp.Automated() && !m.ProcessingRemoteKeys && len(ed.targets) == 0 {
			cmd = m.replyHistoryAdd(ed.session, ed.window, ed.comp.Cleared())
		}
	}
	// A view that closed takes its copy for the desktop viewer with it,
	// and one that opened, or a strip that changed, may need pictures.
	m.replyPreviewClosed()
	return tea.Batch(cmd, m.replyPreviewCmd(), m.replyMentionCmd())
}

// InboxReplyCancel closes the editor. A draft with text in it is kept for
// the pane. A draft a key from send-keys touched is not: it can never be
// sent.
func (m *OS) InboxReplyCancel() {
	ed := m.Inbox.reply.editor
	m.Inbox.reply.editor = nil
	// The image view shows the draft's images: it goes with the editor.
	m.Inbox.ck.img = nil
	if ed == nil {
		return
	}
	if !ed.comp.Empty() && !ed.comp.Automated() {
		ed.comp.SetNotice("")
		if len(ed.targets) > 0 {
			m.Inbox.reply.bulkDraft = ed.comp
		} else {
			m.keepInboxDraft(ed.session, ed.window, ed.comp)
		}
	}
	if ed.closeAfter {
		m.CloseInbox()
	}
}

// InboxReplySend queues the reply. An empty reply sends nothing and keeps
// the editor open. A reply with invisible characters, or larger than the
// daemon takes, stays open with a line that says why.
func (m *OS) InboxReplySend() tea.Cmd {
	ed := m.Inbox.reply.editor
	if ed == nil {
		return nil
	}
	if ed.comp.Empty() {
		return nil
	}
	if ed.comp.Automated() || m.ProcessingRemoteKeys {
		m.Inbox.reply.editor = nil
		m.Inbox.ck.img = nil
		if ed.closeAfter {
			m.CloseInbox()
		}
		m.ShowNotification("A reply that send-keys typed is not sent: only keys from your keyboard queue a message as you", "error", m.Settings.NotificationDuration)
		return nil
	}
	parts, ok := ed.comp.SubmitParts()
	if !ok {
		return nil
	}
	parts = trimReplyParts(parts)
	text := composer.JoinParts(parts)
	blocks := ed.comp.Blocks() > 0
	if !blocks && len(text) > inboxReplyMax {
		// A draft with no block was typed or pasted inline, and has no
		// paste to remove.
		ed.comp.SetNotice("The message is larger than 16 KB. Make it shorter to send.")
		return nil
	}
	if blocks {
		if why := replyPartsRefusal(parts); why != "" {
			ed.comp.SetNotice(why)
			return nil
		}
		// The daemon decides how each block travels. text goes too, for a
		// daemon older than parts, but only when it holds the whole message:
		// one that cannot gets no text, refuses, and says to update. An
		// image has no text, so a message with one never goes as text.
		if n, _ := ed.comp.Images(); n > 0 || len(text) > inboxReplyMax {
			text = ""
		}
	}
	nonce := m.inboxNonce()
	if nonce == "" {
		m.ShowNotification("This daemon issued no attach nonce, so it cannot tell you from an agent. Update the daemon", "error", m.Settings.NotificationDuration*2)
		return nil
	}
	m.Inbox.reply.editor = nil
	m.Inbox.ck.img = nil
	if ed.closeAfter {
		m.CloseInbox()
	}
	st := &m.Inbox.reply
	if st.sending == nil {
		st.sending = make(map[uint64]*composer.Model)
	}
	if !blocks {
		parts = nil
	}
	if len(ed.targets) > 0 {
		// The same message to each. A reply one of them refuses gets a
		// copy of the draft as that pane's own.
		m.Inbox.agents.marks = nil
		m.Inbox.agents.marksGen++
		cmds := make([]tea.Cmd, 0, len(ed.targets))
		for _, t := range ed.targets {
			st.sendSeq++
			st.sending[st.sendSeq] = ed.comp.Clone()
			cmds = append(cmds, m.inboxQueueCmd(t.session, t.window, t.who, text, nonce, false, st.sendSeq, parts))
		}
		return tea.Batch(cmds...)
	}
	st.sendSeq++
	st.sending[st.sendSeq] = ed.comp
	return m.inboxQueueCmdOn(ed.host, ed.session, ed.window, ed.who, text, nonce, false, st.sendSeq, parts)
}

// trimReplyParts drops the message's leading blank lines and trailing space.
// The first line's indent stays: it can be the indent of pasted code. Only
// text parts are trimmed. A block is sent as it was pasted.
func trimReplyParts(parts []composer.Part) []composer.Part {
	for len(parts) > 0 && parts[0].Kind == composer.PartText {
		parts[0].Text = strings.TrimLeft(parts[0].Text, "\n")
		if parts[0].Text != "" {
			break
		}
		parts = parts[1:]
	}
	for n := len(parts); n > 0 && parts[n-1].Kind == composer.PartText; n = len(parts) {
		parts[n-1].Text = strings.TrimRightFunc(parts[n-1].Text, unicode.IsSpace)
		if parts[n-1].Text != "" {
			break
		}
		parts = parts[:n-1]
	}
	return parts
}

// Bounds of a reply in parts, the daemon's (session/queue_parts.go).
const (
	inboxReplyPartsMax = 64
	inboxReplyBytesMax = 8 << 20
)

// replyPartsRefusal says why the daemon would refuse a reply in parts, or ""
// when it would not, so the draft stays open with the reason.
func replyPartsRefusal(parts []composer.Part) string {
	if len(parts) > inboxReplyPartsMax {
		return "The message has too many pastes. Remove a paste to send."
	}
	size, typed := 0, 0
	for _, p := range parts {
		size += len(p.Text)
		if p.Image != nil {
			size += len(p.Image.Data)
		}
		if p.Kind == composer.PartText {
			typed += len(p.Text)
		} else {
			// The label that names the file, as the daemon counts it.
			typed += 64 + 256
		}
	}
	if size > inboxReplyBytesMax {
		return "The message is larger than 8 MB. Remove a paste or an image to send."
	}
	if typed > inboxReplyMax {
		return "The typed text is larger than 16 KB. Make it shorter to send."
	}
	return ""
}

// inboxQueueCmd sends queue-prompt as the person.
func (m *OS) inboxQueueCmd(sessionID, windowID, who, text, nonce string, requeue bool, send uint64, parts []composer.Part) tea.Cmd {
	return m.inboxQueueCmdOn("", sessionID, windowID, who, text, nonce, requeue, send, parts)
}

// inboxQueueCmdOn is inboxQueueCmd for a pane on host, empty for this
// machine.
func (m *OS) inboxQueueCmdOn(host, sessionID, windowID, who, text, nonce string, requeue bool, send uint64, parts []composer.Part) tea.Cmd {
	call := m.inboxCaller()
	params := map[string]any{"session": sessionID, "window": windowID, "text": text, "human_nonce": nonce}
	if host != "" {
		params["host"] = host
	}
	if parts != nil {
		params["parts"] = wireReplyParts(parts)
	}
	return func() tea.Msg {
		msg := InboxRepliedMsg{Session: sessionID, Window: windowID, Who: who, Text: text, Parts: parts, Requeue: requeue, Send: send}
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

// replyPart is one part of a reply on the wire, as queue-prompt takes it.
type replyPart struct {
	Kind string `json:"kind"`
	Text string `json:"text,omitempty"`
	N    int    `json:"n,omitempty"`
	// Data is an image's file. JSON carries it in base64.
	Data []byte `json:"data,omitempty"`
}

// wireReplyParts is a reply's parts as queue-prompt takes them.
func wireReplyParts(parts []composer.Part) []replyPart {
	out := make([]replyPart, len(parts))
	for i, p := range parts {
		out[i] = replyPart{Kind: "text", Text: p.Text}
		switch p.Kind {
		case composer.PartPaste:
			out[i] = replyPart{Kind: "paste", Text: p.Text, N: p.N}
		case composer.PartImage:
			out[i] = replyPart{Kind: "image", N: p.N, Data: p.Image.Data}
		}
	}
	return out
}

// applyInboxReplied says what happened to a reply, and keeps its text so a
// drop of it can be undone.
func (m *OS) applyInboxReplied(msg InboxRepliedMsg) {
	// The draft of a reply the daemon took is done with. The draft of one
	// it refused is kept, so r on the pane opens it again.
	if comp, ok := m.Inbox.reply.sending[msg.Send]; ok {
		delete(m.Inbox.reply.sending, msg.Send)
		if msg.Err != nil {
			m.restoreInboxDraft(msg.Session, msg.Window, comp)
		}
	}
	if msg.Err != nil {
		if callErr, ok := errors.AsType[*session.VerbCallError](msg.Err); ok {
			switch callErr.Code {
			case session.ErrVerbUnknownVerb:
				m.Inbox.reply.noQueue = true
				m.ShowNotification("This daemon cannot queue a reply. "+m.restartHint(), "error", m.Settings.NotificationDuration*2)
				return
			case session.ErrVerbInvalidParams:
				if msg.Parts != nil && msg.Text == "" && strings.Contains(callErr.Message, "text is required") {
					// A daemon older than parts read no message: the reply
					// was too large to send as text too, or held an image.
					m.ShowNotification("This daemon cannot take images or large pastes. Your draft is kept. "+m.restartHint(), "error", m.Settings.NotificationDuration*2)
					return
				}
				if strings.Contains(callErr.Message, "a part is text or paste") {
					m.ShowNotification("This daemon cannot take images. Your draft is kept. "+m.restartHint(), "error", m.Settings.NotificationDuration*2)
					return
				}
				if strings.Contains(callErr.Message, "cannot take images") {
					m.restoreNotice(msg, "This agent cannot take images. Remove the image to send.")
					return
				}
			case session.ErrVerbQueueFull:
				if strings.Contains(callErr.Message, "bytes") {
					m.ShowNotification("The queue for "+msg.Who+" is full. Wait for "+msg.Who+" to take what is queued. Your draft is kept", "error", m.Settings.NotificationDuration*2)
					return
				}
				m.ShowNotification(msg.Who+" already has as many messages queued as [agents.queue] max allows. Nothing was queued", "error", m.Settings.NotificationDuration*2)
				return
			}
		}
		m.ShowNotification("The reply to "+msg.Who+" was not queued: "+msg.Err.Error()+". Your draft is kept", "error", m.Settings.NotificationDuration*2)
		return
	}
	m.rememberInboxReply(msg.ID, inboxSentReply{text: msg.Text, parts: msg.Parts})
	switch {
	case msg.Delivering:
		m.ShowNotification("Typing your reply to "+msg.Who, "success", m.Settings.NotificationDuration)
	case msg.Queued <= 1:
		m.ShowNotification("Queued for "+msg.Who+". It is typed when "+msg.Who+" is at rest", "info", m.Settings.NotificationDuration)
	default:
		m.ShowNotification("Queued for "+msg.Who+", "+strconv.Itoa(msg.Position)+" of "+strconv.Itoa(msg.Queued)+". Each is typed at a rest", "info", m.Settings.NotificationDuration)
	}
}

// rememberInboxReply keeps a sent reply by its entry id.
func (m *OS) rememberInboxReply(id string, r inboxSentReply) {
	if id == "" {
		return
	}
	st := &m.Inbox.reply
	if st.sent == nil {
		st.sent = make(map[string]inboxSentReply)
	}
	if old, ok := st.sent[id]; ok {
		st.sentBytes -= old.size()
	} else {
		st.sentOrder = append(st.sentOrder, id)
	}
	st.sent[id] = r
	st.sentBytes += r.size()
	for len(st.sentOrder) > 1 && (len(st.sentOrder) > inboxReplySentMax || st.sentBytes > inboxReplySentMaxBytes) {
		st.forgetSent(st.sentOrder[0])
	}
}

// forgetSent drops the sent reply id, and its bytes from the count.
func (st *inboxReplyState) forgetSent(id string) {
	if r, ok := st.sent[id]; ok {
		st.sentBytes -= r.size()
		delete(st.sent, id)
	}
	if i := slices.Index(st.sentOrder, id); i >= 0 {
		st.sentOrder = slices.Delete(st.sentOrder, i, i+1)
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
			m.ShowNotification("This daemon has no queue. "+m.restartHint(), "error", m.Settings.NotificationDuration*2)
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
	sent, known := st.sent[msg.ID]
	if !known {
		st.dropped = nil
		m.ShowNotification("Dropped 1 queued message to "+msg.Who, "info", m.Settings.NotificationDuration)
		return
	}
	st.forgetSent(msg.ID)
	st.dropped = &inboxReplyDrop{session: msg.Session, window: msg.Window, who: msg.Who, reply: sent, at: time.Now()}
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
	return m.inboxQueueCmd(d.session, d.window, d.who, d.reply.text, nonce, true, 0, d.reply.parts), true
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

// inboxReplyHints are the reply editor's key hints, when it is open.
func (m *OS) inboxReplyHints() ([]overlay.Hint, bool) {
	ed := m.Inbox.reply.editor
	if ed == nil {
		return nil, false
	}
	return composerHints(ed.comp.Hints(overlay.UseASCII())), true
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
