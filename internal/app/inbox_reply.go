package app

import (
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/composer"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/units"
)

const inboxReplyMax = 16 << 10

const inboxReplyTimeout = 5 * time.Second

const inboxRemoteQueueRefusal = "A key from send-keys does not change the queue: only keys from your keyboard queue or drop a message as you"

const (
	inboxReplySentMax      = 16
	inboxReplySentMaxBytes = 16 << 20
)

type inboxReplyState struct {
	editor     *inboxReplyEditor
	sent       map[string]inboxSentReply
	sentOrder  []string
	sentBytes  int
	dropped    *inboxReplyDrop
	drafts     map[string]*composer.Model
	draftOrder []string
	bulkDraft  *composer.Model
	sending    map[uint64]*composer.Model
	sendSeq    uint64
	holdSeq    uint64
}

const inboxReplyDraftsMax = 16

const inboxReplyRows = 8

func (m *OS) composerRows(def int) int {
	if m.UserConfig == nil {
		return def
	}
	return m.UserConfig.Agents.ReplyRowsOr(def)
}

func inboxDraftKey(sessionID, windowID string) string { return sessionID + "/" + windowID }

type inboxReplyEditor struct {
	session    string
	window     string
	host       string
	who        string
	context    string
	comp       *composer.Model
	closeAfter bool
	targets    []replyTarget
	draftWires
	typing    composer.TypingHold
	arrived   map[string]inboxArrival
	holdArmed bool
}

type inboxSentReply struct {
	text  string
	parts []composer.Part
}

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
	// Now marks a reply typed into the pane at once with send-text, not
	// queued. See inbox_reply_now.go.
	Now bool
	Err error
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

func inboxReplyKind(it session.AttentionItem) bool {
	return (it.Kind == session.AttentionFinished || it.Kind == session.AttentionErrored) && it.Window != ""
}

func (m *OS) inboxReplyAgent(it session.AttentionItem) (cmd tea.Cmd, ok bool) {
	if !inboxReplyKind(it) {
		return nil, false
	}
	context := "done"
	if it.Kind == session.AttentionErrored {
		context = "errored"
	}
	context += " " + units.Waited(it.Since, time.Now())
	if it.Host != "" {
		m.openHostReply(it.Host, it.Session, it.Window, inboxWho(it), context)
		return nil, true
	}
	m.openInboxReply(it.Session, it.Window, inboxWho(it), context, false)
	return nil, true
}

func (m *OS) openInboxReply(sessionID, windowID, who, context string, closeAfter bool) bool {
	if state, seq, _, ok := m.railPane(sessionID, windowID); ok {
		if drawn, _ := m.railAgentState(windowID, state, seq); drawn == "needs_input" {
			m.ShowNotification(who+" is waiting on a prompt. Answer it first ("+m.inboxKeyOr(config.ActionInboxPeek, "space")+" to peek).", "warning", m.Settings.NotificationDuration)
			return false
		}
	}
	if m.Inbox.call == nil && m.DaemonClient == nil {
		return m.inboxCanMark("Replying")
	}
	if sessionID == "" {
		sessionID = m.sidebarCurrentSessionID()
	}
	m.answerEnd(answerArmed, "reply")
	comp := m.takeInboxDraft(sessionID, windowID)
	if comp == nil {
		comp = composer.New(composer.Options{MaxRows: m.composerRows(inboxReplyRows), Placeholder: "Type your reply.", Images: true, SendNow: true, ListKey: m.composerListKey, ListKeyHint: m.composerListKeyHint})
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

func (m *OS) openHostReply(host, sessionID, windowID, who, context string) bool {
	if sessionID == "" {
		return false
	}
	if !m.inboxCanMark("Replying") {
		return false
	}
	if m.AttachedHost != "" {
		m.ShowNotification("Replying to a third machine needs a client attached to this machine's daemon", "warning", m.Settings.NotificationDuration)
		return false
	}
	m.answerEnd(answerArmed, "reply")
	comp := m.takeInboxDraft(sessionID, windowID)
	if comp == nil {
		comp = composer.New(composer.Options{MaxRows: m.composerRows(inboxReplyRows), Placeholder: "Type your reply.", Images: true, ListKey: m.composerListKey, ListKeyHint: m.composerListKeyHint})
	}
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

func (m *OS) restoreNotice(msg InboxRepliedMsg, notice string) {
	if ed := m.Inbox.reply.editor; ed != nil && ed.session == msg.Session && ed.window == msg.Window {
		ed.comp.SetNotice(notice)
		return
	}
	m.ShowNotification(notice+" Press r on "+msg.Who+" to open the draft.", "error", m.Settings.NotificationDuration*2)
}

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
	state, _, label, ok := m.railPane(sessionID, windowID)
	if !ok {
		m.ShowNotification("That pane is on another machine. Attach there to reply", "warning", m.Settings.NotificationDuration)
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
		m.noteInboxTyped()
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
	m.noteInboxTyped()
	return tea.Batch(m.inboxReplyPasteCmd(ed, text), m.armInboxTypingRelease())
}

// InboxReplyKey gives a key to the reply editor and does what it asks.
func (m *OS) InboxReplyKey(msg tea.KeyPressMsg) tea.Cmd {
	ed := m.Inbox.reply.editor
	if ed == nil {
		return nil
	}
	m.noteInboxTyped()
	var cmd tea.Cmd
	switch ed.comp.Update(msg, m.ProcessingRemoteKeys) {
	case composer.EventSend:
		cmd = m.InboxReplySend()
	case composer.EventSendNow:
		cmd = m.InboxReplySendNow()
	case composer.EventClose:
		m.InboxReplyCancel()
	case composer.EventCopy, composer.EventCut:
		cmd = m.CopyFromEditor(ed.comp.Copied())
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
		if !ed.comp.Automated() && !m.ProcessingRemoteKeys && len(ed.targets) == 0 {
			cmd = m.replyHistoryAdd(ed.session, ed.window, ed.comp.Cleared())
		}
	}
	m.replyPreviewClosed()
	return tea.Batch(cmd, m.replyPreviewCmd(), m.replyMentionCmd(), m.armInboxTypingRelease())
}

// InboxReplyCancel closes the editor. A draft with text in it is kept for
// the pane. A draft a key from send-keys touched is not: it can never be
// sent.
func (m *OS) InboxReplyCancel() {
	ed := m.Inbox.reply.editor
	m.Inbox.reply.editor = nil
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

// InboxReplySend queues the reply. An empty reply sends nothing, keeps the
// editor open and says so. A reply with invisible characters, or larger than the
// daemon takes, stays open with a line that says why.
func (m *OS) InboxReplySend() tea.Cmd {
	ed := m.Inbox.reply.editor
	if ed == nil {
		return nil
	}
	if ed.comp.Empty() {
		ed.comp.SetInfo("The reply is empty. Type a message to send it.")
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
	shown := text
	blocks := ed.comp.Blocks() > 0
	if !blocks && len(text) > inboxReplyMax {
		ed.comp.SetNotice("The message is larger than 16 KB. Make it shorter to send.")
		return nil
	}
	if blocks {
		if why := replyPartsRefusal(parts); why != "" {
			ed.comp.SetNotice(why)
			return nil
		}
		if n, _ := ed.comp.Images(); n > 0 || len(text) > inboxReplyMax {
			text = ""
		}
	}
	nonce := m.inboxNonce()
	if nonce == "" {
		m.ShowNotification(m.noNonceText(), "error", m.Settings.NotificationDuration*2)
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
		m.Inbox.agents.marks = nil
		m.Inbox.agents.marksGen++
		cmds := make([]tea.Cmd, 0, len(ed.targets))
		for _, t := range ed.targets {
			st.sendSeq++
			st.sending[st.sendSeq] = ed.comp.Clone()
			m.addEcho("", t.session, t.window, t.who, shown, parts, st.sendSeq)
			cmds = append(cmds, m.inboxQueueCmd(t.session, t.window, t.who, text, nonce, false, st.sendSeq, parts))
		}
		return tea.Batch(cmds...)
	}
	st.sendSeq++
	st.sending[st.sendSeq] = ed.comp
	m.addEcho(ed.host, ed.session, ed.window, ed.who, shown, parts, st.sendSeq)
	m.threadFollowOnSend(ed.host, ed.session, ed.window)
	return m.inboxQueueCmdOn(ed.host, ed.session, ed.window, ed.who, text, nonce, false, st.sendSeq, parts)
}

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

const (
	inboxReplyPartsMax = 64
	inboxReplyBytesMax = 8 << 20
)

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

func (m *OS) queueCaller(host string) inboxVerbCall {
	if host != "" {
		return m.inboxCaller()
	}
	return m.agentCaller()
}

func (m *OS) inboxQueueCmd(sessionID, windowID, who, text, nonce string, requeue bool, send uint64, parts []composer.Part) tea.Cmd {
	return m.inboxQueueCmdOn("", sessionID, windowID, who, text, nonce, requeue, send, parts)
}

func (m *OS) inboxQueueCmdOn(host, sessionID, windowID, who, text, nonce string, requeue bool, send uint64, parts []composer.Part) tea.Cmd {
	call := m.queueCaller(host)
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
		if err := jsonx.Unmarshal(raw, &res); err != nil {
			msg.Err = err
			return msg
		}
		msg.ID, msg.Position, msg.Queued, msg.Delivering = res.ID, res.Position, res.Queued, res.Delivering
		return msg
	}
}

type replyPart struct {
	Kind string `json:"kind"`
	Text string `json:"text,omitempty"`
	N    int    `json:"n,omitempty"`
	Data []byte `json:"data,omitempty"`
}

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

func (m *OS) applyInboxReplied(msg InboxRepliedMsg) {
	if comp, ok := m.Inbox.reply.sending[msg.Send]; ok {
		delete(m.Inbox.reply.sending, msg.Send)
		if msg.Err != nil {
			m.restoreInboxDraft(msg.Session, msg.Window, comp)
		}
	}
	if msg.Err != nil && msg.Now {
		m.ShowNotification("The reply to "+msg.Who+" was not sent: "+msg.Err.Error()+". Your draft is kept", "error", m.Settings.NotificationDuration*2)
		return
	}
	if msg.Err != nil {
		if callErr, ok := errors.AsType[*session.VerbCallError](msg.Err); ok {
			switch callErr.Code {
			case session.ErrVerbInvalidParams:
				if msg.Parts != nil && msg.Text == "" && strings.Contains(callErr.Message, "text is required") {
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
	case msg.Now:
		m.ShowNotification("Sent your reply to "+msg.Who+" now", "success", m.Settings.NotificationDuration)
	case msg.Delivering:
		m.ShowNotification("Typing your reply to "+msg.Who, "success", m.Settings.NotificationDuration)
	case msg.Queued <= 1:
		m.ShowNotification("Queued for "+msg.Who+". It is typed when "+msg.Who+" is at rest", "info", m.Settings.NotificationDuration)
	default:
		m.ShowNotification("Queued for "+msg.Who+", "+strconv.Itoa(msg.Position)+" of "+strconv.Itoa(msg.Queued)+". Each is typed at a rest", "info", m.Settings.NotificationDuration)
	}
}

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

func (st *inboxReplyState) forgetSent(id string) {
	if r, ok := st.sent[id]; ok {
		st.sentBytes -= r.size()
		delete(st.sent, id)
	}
	if i := slices.Index(st.sentOrder, id); i >= 0 {
		st.sentOrder = slices.Delete(st.sentOrder, i, i+1)
	}
}

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
	if m.railQueued(sessionID, windowID) == 0 {
		return nil, false
	}
	if m.ProcessingRemoteKeys {
		m.ShowNotification(inboxRemoteQueueRefusal, "error", m.Settings.NotificationDuration)
		return nil, true
	}
	if !m.inboxCanMark("Dropping a queued message") {
		return nil, true
	}
	nonce := m.inboxNonce()
	if nonce == "" {
		m.ShowNotification(m.noNonceText(), "error", m.Settings.NotificationDuration*2)
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
	call := m.queueCaller("")
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
		if err := jsonx.Unmarshal(raw, &list); err != nil {
			msg.Err = err
			return msg
		}
		id := ""
		for _, v := range slices.Backward(list.Entries) {
			if v.State != "delivering" {
				id = v.ID
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
		if err := jsonx.Unmarshal(raw, &res); err != nil {
			msg.Err = errfmt.Errorf("read the cancel-queued answer: %w", err)
			return msg
		}
		msg.ID, msg.Left = id, res.Queued
		return msg
	}, true
}

func (m *OS) applyInboxQueueDropped(msg InboxQueueDroppedMsg) {
	if msg.Err == nil {
		m.echoDropped(msg.Session, msg.Window, msg.ID)
	}
	if msg.Err != nil {
		m.ShowNotification("Nothing was dropped: "+msg.Err.Error(), "error", m.Settings.NotificationDuration*2)
		return
	}
	if msg.ID == "" {
		m.ShowNotification("Nothing queued for "+msg.Who+" can be dropped: a message being typed cannot be taken back", "warning", m.Settings.NotificationDuration)
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
	m.showUndoOffer("Dropped 1 queued message to " + msg.Who + ". " + key + " undoes.")
}

func (m *OS) sidebarAgentUndoDrop(sessionID, windowID string) (tea.Cmd, bool) {
	st := &m.Inbox.reply
	d := st.dropped
	if d == nil || time.Since(d.at) >= m.inboxUndoWindow() || d.window != windowID || (sessionID != "" && d.session != sessionID) {
		return nil, false
	}
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

func (m *OS) inboxReplyHints() ([]overlay.Hint, bool) {
	ed := m.Inbox.reply.editor
	if ed == nil {
		return nil, false
	}
	hints := slices.DeleteFunc(slices.Clone(ed.comp.Hints(overlay.UseASCII())), func(h composer.Hint) bool {
		return replyAreaHints[h.Label]
	})
	return composerHints(hints), true
}

var replyAreaHints = map[string]bool{"send": true, "new line": true, "paste image": true}

func (m *OS) sidebarAgentQueuedFigure(e sidebarAgentEntry) string {
	if e.Queued <= 0 || sidebarAgentGroup(e.State, e.DoneSeen) == sidebarGroupNeedsYou {
		return ""
	}
	return strconv.Itoa(e.Queued) + " queued"
}

func (m *OS) sidebarAgentQueuedFigures(e sidebarAgentEntry) []string {
	full := m.sidebarAgentQueuedFigure(e)
	if full == "" {
		return nil
	}
	return []string{full, strconv.Itoa(e.Queued) + "q"}
}
