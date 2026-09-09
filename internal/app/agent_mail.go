package app

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/sound"
)

// The agent mailbox on the client. The daemon owns the ring (see
// session/verb_mailbox.go); this is the person's view of it: a mirror of the
// messages the daemon pushed, the unread counts the rail draws, and the overlay
// that reads a thread and answers it.
//
// The mirror exists so an idle client costs nothing. A message arrives as a
// push (MsgAgentMail) and is stored here; nothing polls, and an attached
// client with no agent traffic does no work because this feature exists. The
// ring is read once per attach, off the Update goroutine, so a client that
// joined after a conversation started still sees all of it. A read receipt is
// pushed the same way, so the count beside a pane follows the agent actually
// reading rather than this client's guess.
//
// The person's address is "human", the reserved inbox the daemon knows (see
// session.AgentInboxHuman). Mail to it is what raises an alert here; a reply
// from the overlay is sent from it; and the overlay draws that label as "you".

// agentMailKeep bounds the mirror, matching the daemon's ring.
const agentMailKeep = 256

// agentMailBottom is a scroll offset past any conversation, which the render
// clamps to the last line. Opening a thread lands on its newest message.
const agentMailBottom = 1 << 30

// AgentMailMsg is one MsgAgentMail push from the daemon.
type AgentMailMsg struct {
	Payload session.AgentMailPayload
}

// AgentMailLoadMsg asks Update to read the ring again, after a session switch.
type AgentMailLoadMsg struct{}

// AgentMailMarkMsg asks Update to mark the person's mail in one thread read,
// after a dock message opened it.
type AgentMailMarkMsg struct {
	Thread uint64
}

// AgentMailLoadedMsg is the ring read back from the daemon.
type AgentMailLoadedMsg struct {
	Messages []session.AgentMessage
	Evicted  uint64
	Err      error
}

// AgentMailSentMsg is the outcome of a reply the person sent.
type AgentMailSentMsg struct {
	Err error
}

// AgentMailMarkedMsg is the outcome of marking the person's mail read. It
// carries nothing: the receipt arrives as a push like any other.
type AgentMailMarkedMsg struct {
	Err error
}

// AgentMailState is the mailbox overlay's state and the mirror behind it.
type AgentMailState struct {
	// Messages is the mirror of the session's ring, oldest first.
	Messages []session.AgentMessage
	// Evicted is how many older messages the daemon's ring has dropped, as the
	// last read reported it. The list says so when it is not zero.
	Evicted uint64
	// Gen counts changes to the mirror, so the rail's render cache can tell one
	// state from the next without comparing them.
	Gen uint64
	// SeenID is the newest message id the person has had on screen. A thread
	// with a newer message is marked new in the list.
	SeenID uint64
	// Inbox narrows the list to threads touching one window, when the overlay
	// was opened from that window's row. Empty lists every thread.
	Inbox string
	// Selected is the cursor in the thread list.
	Selected int
	// Scroll is the list's scroll offset in the thread list, and the line
	// offset of the conversation in the thread view.
	Scroll int
	// Thread is the open thread, zero for the list of threads.
	Thread uint64
	// Composing is true while the reply line is open. Draft is its text.
	Composing bool
	Draft     string
	// Error is the last failure, drawn in the panel until the next action.
	Error string
	// Loading is true between a read being asked for and the daemon answering.
	Loading bool
	// Sending is true between enter on a reply and the daemon answering.
	Sending bool
}

// agentMailThread is one conversation as the list draws it.
type agentMailThread struct {
	ID      uint64
	Kind    string
	From    string
	To      string
	Subject string
	Count   int
	LastID  uint64
	LastAt  int64
	// Unread is true while a message to the person in this thread is unread.
	Unread bool
	// New is true while the thread holds a message the person has not seen.
	New bool
}

// agentMailName is what to call a party to a message: its label, "you" for
// the person at this client, or "all" for a notice with no recipient.
func agentMailName(id, label string, recipient bool) string {
	switch {
	case id == session.AgentInboxHuman:
		return "you"
	case label != "":
		return printableTitle(label)
	case id != "":
		return shortWindowLabel(id)
	case recipient:
		return "all"
	default:
		return "someone"
	}
}

// shortWindowLabel is the first eight characters of a window id, which is how
// list-windows prints one.
func shortWindowLabel(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// agentMailSummary is the one line a message is known by: its subject, else
// the first line of its text.
func agentMailSummary(m session.AgentMessage) string {
	if s := strings.TrimSpace(m.Subject); s != "" {
		return printableTitle(s)
	}
	first, _, _ := strings.Cut(strings.TrimSpace(m.Text), "\n")
	return printableTitle(first)
}

// agentMailAge is how long ago a message was sent, in at most three cells.
func agentMailAge(sentAt int64, now time.Time) string {
	if sentAt <= 0 {
		return ""
	}
	d := now.Sub(time.Unix(0, sentAt))
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + "m"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d.Hours())) + "h"
	default:
		return strconv.Itoa(int(d.Hours())/24) + "d"
	}
}

// agentMailUnreadFor counts the unread messages waiting in one inbox, as the
// mirror knows them. The person's inbox is session.AgentInboxHuman.
func (m *OS) agentMailUnreadFor(inbox string) int {
	n := 0
	for i := range m.AgentMail.Messages {
		mm := &m.AgentMail.Messages[i]
		if mm.Kind == "message" && mm.To == inbox && mm.ReadAt == 0 {
			n++
		}
	}
	return n
}

// AgentMailUnread is how many messages are waiting for the person.
func (m *OS) AgentMailUnread() int { return m.agentMailUnreadFor(session.AgentInboxHuman) }

// noteAgentMail applies one push: a stored message, or a receipt for messages
// an inbox read marked. It runs in Update, on the message the read loop queued.
func (m *OS) noteAgentMail(p session.AgentMailPayload) {
	st := &m.AgentMail
	if len(p.ReadIDs) > 0 {
		read := map[uint64]bool{}
		for _, id := range p.ReadIDs {
			read[id] = true
		}
		for i := range st.Messages {
			if read[st.Messages[i].ID] && st.Messages[i].ReadAt == 0 {
				st.Messages[i].ReadAt = p.ReadAt
			}
		}
		m.agentMailChanged()
		return
	}
	msg := p.Message
	if msg.ID == 0 {
		return
	}
	for _, have := range st.Messages {
		if have.ID == msg.ID {
			return
		}
	}
	st.Messages = append(st.Messages, msg)
	if len(st.Messages) > agentMailKeep {
		st.Messages = st.Messages[len(st.Messages)-agentMailKeep:]
	}
	sort.SliceStable(st.Messages, func(a, b int) bool { return st.Messages[a].ID < st.Messages[b].ID })
	m.agentMailChanged()

	// A message the overlay is already showing needs no announcement, and the
	// view follows it.
	if m.ShowAgentMail && st.Thread == msg.ThreadID {
		st.SeenID = max(st.SeenID, msg.ID)
		st.Scroll = agentMailBottom
		return
	}
	m.considerMailAlert(msg)
}

// agentMailChanged records that the mirror moved, so the rail redraws.
func (m *OS) agentMailChanged() {
	m.AgentMail.Gen++
	m.sidebarCache.invalidate()
}

// considerMailAlert decides whether a message earns an alert. Mail to the
// person and a notice to the session do: they are the two things an agent can
// say that are meant for a human to hear. A message between two agents is
// their business and only counts against the recipient's row on the rail. The
// person's own reply comes back as a push too, and is not news.
func (m *OS) considerMailAlert(msg session.AgentMessage) {
	if msg.From == session.AgentInboxHuman {
		return
	}
	switch {
	case msg.Kind == "message" && msg.To == session.AgentInboxHuman:
	case msg.Kind == "notice":
	default:
		return
	}
	policy := m.agentAlertPolicy()
	if !policy.Enabled || policy.Quiet(time.Now()) {
		return
	}
	text := agentMailName(msg.From, msg.FromLabel, false) + " to " + agentMailName(msg.To, msg.ToLabel, true) + ": " + agentMailSummary(msg)

	if policy.Dock {
		m.ShowNotificationFrom(text, "info", m.Settings.NotificationDuration,
			NotifTarget{SessionID: m.sidebarCurrentSessionID(), WindowID: msg.From, Thread: msg.ThreadID})
	}
	// The same sinks a needs_input transition writes to, for the same reason:
	// the person is being asked for something, wherever they are looking.
	var seq []byte
	if policy.Notify && !m.BrowserClient {
		seq = hostNotifySequence(text, m.detectOuterMultiplexer())
	}
	if policy.PlaysBell() {
		seq = append(seq, 0x07)
	}
	m.writeHostSequence(seq)
	if policy.PlaysAudio() {
		sound.Play(sound.Request{Cue: sound.CueAttention, File: policy.CueFile("needs_input"), Cooldown: policy.SoundCooldown})
	}
}

// OpenAgentMail shows the mailbox at its list of threads and re-reads the ring
// from the daemon, off the Update goroutine. Shared by the keybinding, the
// palette entry and the rail's agents header.
func (m *OS) OpenAgentMail() tea.Cmd {
	return m.openAgentMailFor("")
}

// OpenAgentMailForWindow shows the mailbox narrowed to the threads one window
// took part in, which is what the rail's agent rows open.
func (m *OS) OpenAgentMailForWindow(windowID string) tea.Cmd {
	return m.openAgentMailFor(windowID)
}

func (m *OS) openAgentMailFor(inbox string) tea.Cmd {
	st := &m.AgentMail
	m.ShowAgentMail = true
	st.Inbox = inbox
	st.Thread = 0
	st.Selected = 0
	st.Scroll = 0
	st.Composing = false
	st.Draft = ""
	st.Error = ""
	st.Sending = false
	return m.agentMailLoad()
}

// agentMailLoad asks the daemon for the ring, when there is one to ask.
func (m *OS) agentMailLoad() tea.Cmd {
	if !m.IsDaemonSession || m.DaemonClient == nil {
		return nil
	}
	name := m.DaemonClient.SessionName()
	if name == "" {
		name = m.SessionName
	}
	if name == "" {
		return nil
	}
	m.AgentMail.Loading = true
	return agentMailLoadCmd(name)
}

// OpenAgentMailThread shows one conversation, the way a dock message about it
// does when it is clicked. It returns the command that marks the person's mail
// in it read, or nil when there is none.
func (m *OS) OpenAgentMailThread(thread uint64) tea.Cmd {
	st := &m.AgentMail
	m.ShowAgentMail = true
	st.Inbox = ""
	st.Composing = false
	st.Draft = ""
	st.Error = ""
	st.Sending = false
	st.Thread = thread
	st.Scroll = agentMailBottom
	for _, mm := range m.agentMailThreadMessages(thread) {
		st.SeenID = max(st.SeenID, mm.ID)
	}
	return m.agentMailMarkRead(thread)
}

// CloseAgentMail hides the mailbox. Everything on screen counts as seen.
func (m *OS) CloseAgentMail() {
	st := &m.AgentMail
	if n := len(st.Messages); n > 0 {
		st.SeenID = max(st.SeenID, st.Messages[n-1].ID)
	}
	m.ShowAgentMail = false
	st.Composing = false
	st.Draft = ""
	st.Error = ""
}

// resetAgentMail forgets the mirror, on a session switch: the ring is per
// session and the one just left has nothing to say about the one joined.
func (m *OS) resetAgentMail() {
	m.AgentMail = AgentMailState{}
	m.ShowAgentMail = false
	m.sidebarCache.invalidate()
}

// agentMailLoadCmd reads the whole ring without marking anything read: the
// person looking at a message is not the agent it was addressed to.
func agentMailLoadCmd(sessionName string) tea.Cmd {
	return func() tea.Msg {
		client, err := session.DialVerbClient()
		if err != nil {
			return AgentMailLoadedMsg{Err: err}
		}
		defer func() { _ = client.Close() }()
		raw, err := client.Call("read-agent-messages", map[string]any{
			"session": sessionName,
			"peek":    true,
			"limit":   agentMailKeep,
		})
		if err != nil {
			return AgentMailLoadedMsg{Err: err}
		}
		var res struct {
			Messages []session.AgentMessage `json:"messages"`
			Evicted  uint64                 `json:"evicted"`
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			return AgentMailLoadedMsg{Err: err}
		}
		return AgentMailLoadedMsg{Messages: res.Messages, Evicted: res.Evicted}
	}
}

// applyAgentMailLoaded merges what the daemon holds into the mirror. Runs in
// Update and does no I/O. The push may have delivered something newer than
// the read, so the two are merged rather than the read winning outright.
func (m *OS) applyAgentMailLoaded(msg AgentMailLoadedMsg) {
	st := &m.AgentMail
	st.Loading = false
	if msg.Err != nil {
		st.Error = "The daemon did not answer. " + msg.Err.Error()
		return
	}
	byID := map[uint64]session.AgentMessage{}
	for _, mm := range st.Messages {
		byID[mm.ID] = mm
	}
	for _, mm := range msg.Messages {
		byID[mm.ID] = mm
	}
	merged := make([]session.AgentMessage, 0, len(byID))
	for _, mm := range byID {
		merged = append(merged, mm)
	}
	sort.Slice(merged, func(a, b int) bool { return merged[a].ID < merged[b].ID })
	if len(merged) > agentMailKeep {
		merged = merged[len(merged)-agentMailKeep:]
	}
	st.Messages = merged
	st.Evicted = msg.Evicted
	m.agentMailChanged()
}

// agentMailThreads groups the mirror into conversations, newest activity
// first, narrowed to the overlay's inbox when it has one.
func (m *OS) agentMailThreads() []agentMailThread {
	st := &m.AgentMail
	byThread := map[uint64]*agentMailThread{}
	var order []uint64
	for _, mm := range st.Messages {
		if st.Inbox != "" && mm.From != st.Inbox && mm.To != st.Inbox {
			continue
		}
		th := byThread[mm.ThreadID]
		if th == nil {
			th = &agentMailThread{
				ID:      mm.ThreadID,
				Kind:    mm.Kind,
				From:    agentMailName(mm.From, mm.FromLabel, false),
				To:      agentMailName(mm.To, mm.ToLabel, true),
				Subject: agentMailSummary(mm),
			}
			byThread[mm.ThreadID] = th
			order = append(order, mm.ThreadID)
		}
		th.Count++
		th.LastID = mm.ID
		th.LastAt = mm.SentAt
		if mm.Kind == "message" && mm.To == session.AgentInboxHuman && mm.ReadAt == 0 {
			th.Unread = true
		}
		if mm.ID > st.SeenID {
			th.New = true
		}
	}
	out := make([]agentMailThread, 0, len(order))
	for _, id := range order {
		out = append(out, *byThread[id])
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].LastID > out[b].LastID })
	return out
}

// agentMailThreadMessages is one conversation, oldest first.
func (m *OS) agentMailThreadMessages(thread uint64) []session.AgentMessage {
	var out []session.AgentMessage
	for _, mm := range m.AgentMail.Messages {
		if mm.ThreadID == thread {
			out = append(out, mm)
		}
	}
	return out
}

// AgentMailMove moves the list cursor, or scrolls the open thread by lines.
func (m *OS) AgentMailMove(delta int) {
	st := &m.AgentMail
	if st.Thread != 0 {
		st.Scroll = max(st.Scroll+delta, 0)
		return
	}
	n := len(m.agentMailThreads())
	if n == 0 {
		st.Selected = 0
		return
	}
	st.Selected = clampInt(st.Selected+delta, 0, n-1)
}

// AgentMailSelect puts the list cursor on row idx.
func (m *OS) AgentMailSelect(idx int) {
	n := len(m.agentMailThreads())
	if n == 0 {
		return
	}
	m.AgentMail.Selected = clampInt(idx, 0, n-1)
}

// AgentMailOpenSelected opens the thread under the cursor, scrolled to its
// newest message, and returns the command that marks the person's mail in it
// read. It returns nil when there was nothing to open.
func (m *OS) AgentMailOpenSelected() tea.Cmd {
	st := &m.AgentMail
	threads := m.agentMailThreads()
	if st.Selected < 0 || st.Selected >= len(threads) {
		return nil
	}
	st.Thread = threads[st.Selected].ID
	st.Scroll = agentMailBottom
	st.Error = ""
	st.SeenID = max(st.SeenID, threads[st.Selected].LastID)
	return m.agentMailMarkRead(st.Thread)
}

// agentMailMarkRead marks the person's unread mail in a thread read, off the
// Update goroutine, when there is any. Reading anyone else's thread marks
// nothing, which is the rule the CLI follows too.
func (m *OS) agentMailMarkRead(thread uint64) tea.Cmd {
	unread := false
	for _, mm := range m.agentMailThreadMessages(thread) {
		if mm.Kind == "message" && mm.To == session.AgentInboxHuman && mm.ReadAt == 0 {
			unread = true
			break
		}
	}
	if !unread || !m.IsDaemonSession || m.DaemonClient == nil {
		return nil
	}
	name := m.DaemonClient.SessionName()
	if name == "" {
		name = m.SessionName
	}
	return agentMailMarkCmd(name, thread)
}

// agentMailMarkCmd is the marking read of the person's inbox for one thread.
// The receipt the daemon pushes back is what updates the mirror.
func agentMailMarkCmd(sessionName string, thread uint64) tea.Cmd {
	return func() tea.Msg {
		client, err := session.DialVerbClient()
		if err != nil {
			return AgentMailMarkedMsg{Err: err}
		}
		defer func() { _ = client.Close() }()
		_, err = client.Call("read-agent-messages", map[string]any{
			"session": sessionName,
			"to":      session.AgentInboxHuman,
			"thread":  thread,
			"limit":   agentMailKeep,
		})
		return AgentMailMarkedMsg{Err: err}
	}
}

// AgentMailBack leaves the open thread for the list, or closes the mailbox
// when the list is already showing.
func (m *OS) AgentMailBack() {
	st := &m.AgentMail
	if st.Thread == 0 {
		m.CloseAgentMail()
		return
	}
	st.Thread = 0
	st.Scroll = 0
	st.Composing = false
	st.Draft = ""
	st.Error = ""
}

// agentMailReplyTarget is who a reply to the open thread goes to: the sender
// of the newest message that was not the person, so answering a thread answers
// the agent that last spoke in it. A thread nobody but the person has spoken in
// is answered with a notice, which every agent in the session can read.
func (m *OS) agentMailReplyTarget() (inbox string, replyTo uint64, ok bool) {
	msgs := m.agentMailThreadMessages(m.AgentMail.Thread)
	if len(msgs) == 0 {
		return "", 0, false
	}
	replyTo = msgs[len(msgs)-1].ID
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].From != "" && msgs[i].From != session.AgentInboxHuman {
			return msgs[i].From, replyTo, true
		}
	}
	return "", replyTo, true
}

// AgentMailStartReply opens the reply line under the open thread.
func (m *OS) AgentMailStartReply() bool {
	st := &m.AgentMail
	if st.Thread == 0 || st.Sending {
		return false
	}
	if _, _, ok := m.agentMailReplyTarget(); !ok {
		return false
	}
	st.Composing = true
	st.Draft = ""
	st.Error = ""
	st.Scroll = agentMailBottom
	return true
}

// AgentMailCancelReply closes the reply line and drops the draft.
func (m *OS) AgentMailCancelReply() {
	m.AgentMail.Composing = false
	m.AgentMail.Draft = ""
}

// AgentMailType appends typed text to the draft.
func (m *OS) AgentMailType(text string) {
	if !m.AgentMail.Composing || text == "" {
		return
	}
	m.AgentMail.Draft += text
}

// AgentMailBackspace removes the last rune of the draft.
func (m *OS) AgentMailBackspace() {
	st := &m.AgentMail
	if !st.Composing || st.Draft == "" {
		return
	}
	r := []rune(st.Draft)
	st.Draft = string(r[:len(r)-1])
}

// AgentMailClearDraft empties the draft and keeps the reply line open.
func (m *OS) AgentMailClearDraft() {
	if m.AgentMail.Composing {
		m.AgentMail.Draft = ""
	}
}

// AgentMailSendReply sends the draft as a reply to the open thread, from the
// person's address, off the Update goroutine. An empty draft sends nothing.
func (m *OS) AgentMailSendReply() tea.Cmd {
	st := &m.AgentMail
	text := strings.TrimSpace(st.Draft)
	if !st.Composing || text == "" || st.Sending {
		return nil
	}
	inbox, replyTo, ok := m.agentMailReplyTarget()
	if !ok {
		return nil
	}
	if !m.IsDaemonSession || m.DaemonClient == nil {
		st.Error = "Mail needs the daemon. Start a session with: tuios new"
		return nil
	}
	name := m.DaemonClient.SessionName()
	if name == "" {
		name = m.SessionName
	}
	st.Sending = true
	st.Error = ""
	return agentMailSendCmd(name, inbox, replyTo, text)
}

// agentMailSendCmd is the send-agent-message call a reply makes.
func agentMailSendCmd(sessionName, inbox string, replyTo uint64, text string) tea.Cmd {
	return func() tea.Msg {
		client, err := session.DialVerbClient()
		if err != nil {
			return AgentMailSentMsg{Err: err}
		}
		defer func() { _ = client.Close() }()
		params := map[string]any{
			"session":  sessionName,
			"text":     text,
			"reply_to": replyTo,
			"from":     session.AgentInboxHuman,
		}
		if inbox != "" {
			params["to"] = inbox
		}
		_, err = client.Call("send-agent-message", params)
		return AgentMailSentMsg{Err: err}
	}
}

// applyAgentMailSent closes the reply line on success and says what happened
// on failure. The sent message itself arrives as a push, like any other.
func (m *OS) applyAgentMailSent(msg AgentMailSentMsg) {
	st := &m.AgentMail
	st.Sending = false
	if msg.Err != nil {
		st.Error = "The reply did not send. " + msg.Err.Error()
		return
	}
	st.Composing = false
	st.Draft = ""
}

// AgentMailFocusPane closes the mailbox and focuses the pane that last spoke in
// the open thread, so the person can type at the agent directly. It reports
// whether a pane was focused.
func (m *OS) AgentMailFocusPane() bool {
	inbox, _, ok := m.agentMailReplyTarget()
	if !ok || inbox == "" {
		return false
	}
	m.CloseAgentMail()
	_, focused := m.sidebarFocusWindow(sidebarRowHit{
		Kind:        sidebarRowAgent,
		SessionID:   m.sidebarCurrentSessionID(),
		WindowID:    inbox,
		WindowIndex: -1,
	})
	return focused
}
