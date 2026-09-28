package app

import (
	"encoding/json"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/sound"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
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

// AgentMailSentMsg is the outcome of a reply or a new message the person
// sent. New is true for a new message. It rides on the result because the
// line it was typed on may be closed by the time the daemon answers.
type AgentMailSentMsg struct {
	Err error
	New bool
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
	// SeenID is the newest message id the person has had on screen with the
	// whole list in view, which is how the mailbox is left when it closes.
	// A thread with a newer message is marked new in the list, unless Seen
	// holds that message.
	SeenID uint64
	// Seen holds the messages newer than SeenID that the person has had on
	// screen one thread at a time: a thread opened, or a message the person
	// wrote. They are kept by id, not by raising SeenID, because SeenID is a
	// high-water mark and raising it would mark older messages in other
	// threads seen too.
	Seen map[uint64]bool
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
	// DraftAutomated is true once anything other than the keyboard touched
	// the reply line: a key a send-keys or tape script routed to this client
	// opened it, typed into it or sent it. Such a reply is sent without the
	// attach nonce, so the daemon stores it as claimed_human, not as the
	// person's answer. Without this, an agent in a pane could drive this
	// client's own reply line with send-keys and have tuios sign its answer
	// as the person. It is cleared when the reply line closes.
	DraftAutomated bool
	// Error is the last failure, drawn in the panel until the next action.
	Error string
	// Loading is true between a read being asked for and the daemon answering.
	Loading bool
	// LoadingSince is when that read went out. The panel says it is reading
	// only once the read has run past overlay.LoadingDelay.
	LoadingSince time.Time
	// Sending is true between enter on a reply and the daemon answering.
	Sending bool
	// Picking is true while the list of agents a new message can go to is
	// open. PickSelected is its cursor.
	Picking      bool
	PickSelected int
	PickScroll   int
	// ComposeTo is the window a new message goes to, while the draft is a
	// new message rather than a reply. Empty while the draft is a reply.
	ComposeTo string
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
	// Link is true when a message in this thread arrived from another
	// machine. The row wears a different mark for it, so a person scanning
	// the list can tell mail from this machine from mail that came over a
	// link without opening either.
	Link bool
	// Anonymous is true when the thread's first message named no sender, as a
	// broadcast notice can.
	Anonymous bool
}

// agentMailFromLink reports whether a message arrived from another machine.
func agentMailFromLink(m session.AgentMessage) bool {
	return m.Origin == session.AgentOriginLink
}

// agentMailOriginHost is what to call the machine a message came from: the
// name it claimed, else "another machine".
func agentMailOriginHost(m session.AgentMessage) string {
	if host := printableTitle(m.OriginHost); host != "" {
		return host
	}
	return "another machine"
}

// agentMailSender is the sender's name as the overlay draws it. A sender on
// another machine is named with that machine, "REVIEWER @ buildbox", so the
// origin is in the name itself and not only in a mark beside it.
func agentMailSender(m session.AgentMessage) string {
	name := agentMailName(m.From, m.FromLabel, false)
	// Something said it was the person and the daemon could not match it to
	// an attached client, so it is not drawn as the person's own reply.
	if m.From == session.AgentInboxHuman && m.ClaimedHuman {
		name += " (unverified)"
	}
	if agentMailFromLink(m) {
		return name + " @ " + agentMailOriginHost(m)
	}
	return name
}

// agentMailName is what to call a party to a message: its label, "you" for
// the person at this client, or "all" for a notice with no recipient.
func agentMailName(id, label string, recipient bool) string {
	switch {
	case id == session.AgentInboxHuman:
		return "you"
	case label != "":
		return agentMailNotThePerson(id, printableTitle(label))
	case id != "":
		return shortWindowLabel(id)
	case recipient:
		return "all"
	default:
		return "someone"
	}
}

// agentMailNotThePerson keeps a pane from being drawn as the person. A label
// that reads "you" or "human", in any case, on anything but the person's own
// inbox gets the short window id after it, since "you" is how the mailbox
// names the person and a pane can set its title to anything.
func agentMailNotThePerson(id, name string) string {
	if id == session.AgentInboxHuman {
		return name
	}
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "you", "human":
		if id == "" {
			return name + " (not you)"
		}
		return name + " (" + shortWindowLabel(id) + ")"
	}
	return name
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

	// What the person wrote is not news to the person. A claim to be the
	// person is, since something else wrote it.
	if msg.From == session.AgentInboxHuman && !msg.ClaimedHuman {
		m.agentMailSee(msg.ID)
	}

	// A message the overlay is already showing needs no announcement, and the
	// view follows it.
	if m.ShowAgentMail && st.Thread == msg.ThreadID {
		m.agentMailSee(msg.ID)
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

// mailAlertPolicy resolves the [notifications.mail] policy over the agent
// one, per call for the reason agentAlertPolicy is.
func (m *OS) mailAlertPolicy() config.MailAlertPolicy {
	agent := m.agentAlertPolicy()
	if m.UserConfig == nil {
		return config.ResolveMailAlerts(nil, agent)
	}
	return config.ResolveMailAlerts(&m.UserConfig.Notifications.Mail, agent)
}

// considerMailAlert decides whether a message earns an alert. Mail to the
// person and a notice to the session do: they are the two things an agent can
// say that are meant for a human to hear. A message between two agents is
// their business and only counts against the recipient's row on the rail,
// unless notifications.mail.between_agents asks for it. The person's own reply
// comes back as a push too, and is not news.
func (m *OS) considerMailAlert(msg session.AgentMessage) {
	if msg.From == session.AgentInboxHuman {
		return
	}
	policy := m.mailAlertPolicy()
	switch {
	case msg.Kind == "message" && msg.To == session.AgentInboxHuman:
	case msg.Kind == "notice":
	case msg.Kind == "message" && policy.BetweenAgents:
	default:
		return
	}
	if !policy.Enabled || policy.Quiet(time.Now()) {
		return
	}
	text := agentMailSender(msg) + " to " + agentMailName(msg.To, msg.ToLabel, true) + ": " + agentMailSummary(msg)

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
	if m.learnOff(learnNoteMail) {
		return nil
	}
	st := &m.AgentMail
	m.ShowAgentMail = true
	st.Inbox = inbox
	st.Thread = 0
	st.Selected = 0
	st.Scroll = 0
	st.Composing = false
	st.Draft = ""
	st.DraftAutomated = false
	st.Error = ""
	st.Sending = false
	st.Picking = false
	st.ComposeTo = ""
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
	m.AgentMail.LoadingSince = time.Now()
	return agentMailLoadCmd(m.agentMailDialer(), name)
}

// agentMailDialer is how a mailbox command reaches the daemon that holds the
// ring: this machine's daemon directly, or, while this client is attached to
// a session on another machine, that machine's daemon through the link. The
// ring is the session's, so it lives where the session does.
func (m *OS) agentMailDialer() agentMailDial {
	host, build := m.AttachedHost, ""
	if m.DaemonClient != nil {
		build = m.DaemonClient.ClientVersion()
	}
	return func() (*session.VerbClient, error) {
		if host == "" {
			return session.DialVerbClient()
		}
		c, _, err := session.DialVerbClientThroughHost(host, build)
		return c, err
	}
}

// agentMailDial opens a verb connection to the daemon that holds the ring.
type agentMailDial func() (*session.VerbClient, error)

// OpenAgentMailThread shows one conversation, the way a dock message about it
// does when it is clicked. It returns the command that marks the person's mail
// in it read, or nil when there is none.
func (m *OS) OpenAgentMailThread(thread uint64) tea.Cmd {
	st := &m.AgentMail
	m.ShowAgentMail = true
	st.Inbox = ""
	st.Composing = false
	st.Draft = ""
	st.DraftAutomated = false
	st.Error = ""
	st.Sending = false
	st.Picking = false
	st.ComposeTo = ""
	st.Thread = thread
	st.Scroll = agentMailBottom
	m.agentMailSeeThread(thread)
	return m.agentMailMarkRead(thread)
}

// agentMailSee records one message as seen, without touching any other.
func (m *OS) agentMailSee(id uint64) {
	st := &m.AgentMail
	if id <= st.SeenID {
		return
	}
	if st.Seen == nil {
		st.Seen = map[uint64]bool{}
	}
	st.Seen[id] = true
}

// agentMailSeeThread records every message in one thread as seen.
func (m *OS) agentMailSeeThread(thread uint64) {
	for _, mm := range m.agentMailThreadMessages(thread) {
		m.agentMailSee(mm.ID)
	}
}

// CloseAgentMail hides the mailbox. Everything on screen counts as seen.
func (m *OS) CloseAgentMail() {
	st := &m.AgentMail
	if n := len(st.Messages); n > 0 {
		st.SeenID = max(st.SeenID, st.Messages[n-1].ID)
	}
	// Everything up to the high-water mark is seen now, so the set is empty.
	st.Seen = nil
	m.ShowAgentMail = false
	st.Composing = false
	st.Draft = ""
	st.DraftAutomated = false
	st.Error = ""
	st.Picking = false
	st.ComposeTo = ""
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
func agentMailLoadCmd(dial agentMailDial, sessionName string) tea.Cmd {
	return func() tea.Msg {
		client, err := dial()
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
				From:    agentMailSender(mm),
				To:      agentMailName(mm.To, mm.ToLabel, true),
				Subject: agentMailSummary(mm),

				Anonymous: mm.From == "" && mm.FromLabel == "",
			}
			byThread[mm.ThreadID] = th
			order = append(order, mm.ThreadID)
		}
		if agentMailFromLink(mm) {
			th.Link = true
		}
		th.Count++
		th.LastID = mm.ID
		th.LastAt = mm.SentAt
		if mm.Kind == "message" && mm.To == session.AgentInboxHuman && mm.ReadAt == 0 {
			th.Unread = true
		}
		if mm.ID > st.SeenID && !st.Seen[mm.ID] {
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
	if st.Picking {
		if n := len(m.agentMailComposeTargets()); n > 0 {
			st.PickSelected = m.listStep(st.PickSelected, delta, n)
		}
		return
	}
	if st.Thread != 0 {
		st.Scroll = max(st.Scroll+delta, 0)
		return
	}
	n := len(m.agentMailThreads())
	if n == 0 {
		st.Selected = 0
		return
	}
	st.Selected = m.listStep(st.Selected, delta, n)
}

// AgentMailSelect puts the list cursor on row idx.
func (m *OS) AgentMailSelect(idx int) {
	if m.AgentMail.Picking {
		if n := len(m.agentMailComposeTargets()); n > 0 {
			m.AgentMail.PickSelected = clampInt(idx, 0, n-1)
		}
		return
	}
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
	if st.Picking {
		m.AgentMailPick()
		return nil
	}
	threads := m.agentMailThreads()
	if st.Selected < 0 || st.Selected >= len(threads) {
		return nil
	}
	st.Thread = threads[st.Selected].ID
	st.Scroll = agentMailBottom
	st.Error = ""
	m.agentMailSeeThread(st.Thread)
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
	return agentMailMarkCmd(m.agentMailDialer(), name, thread)
}

// agentMailMarkCmd is the marking read of the person's inbox for one thread.
// The receipt the daemon pushes back is what updates the mirror.
func agentMailMarkCmd(dial agentMailDial, sessionName string, thread uint64) tea.Cmd {
	return func() tea.Msg {
		client, err := dial()
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
	if st.Picking {
		st.Picking = false
		return
	}
	if st.Thread == 0 {
		m.CloseAgentMail()
		return
	}
	st.Thread = 0
	st.Scroll = 0
	st.Composing = false
	st.Draft = ""
	st.DraftAutomated = false
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
	// A sender on another machine is not a window here, so a reply to it
	// is a notice in this ring: the sender reads it back over the link, in
	// the thread it started, and nothing is typed at anyone.
	return "", replyTo, true
}

// thisMachineName is the name this client signs a reply with when the ring
// is on another machine.
func thisMachineName() string {
	if h := os.Getenv("TUIOS_HOST"); h != "" {
		return h
	}
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return h
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
	st.DraftAutomated = m.ProcessingRemoteKeys
	return true
}

// AgentMailCancelReply closes the reply line and drops the draft.
func (m *OS) AgentMailCancelReply() {
	m.AgentMail.Composing = false
	m.AgentMail.Draft = ""
	m.AgentMail.DraftAutomated = false
	m.AgentMail.ComposeTo = ""
}

// agentMailComposeTargets are the panes a new message can go to: the windows
// of this session that run an agent, in window order. The mailbox is the
// session's, so a pane in another session is not an address here.
func (m *OS) agentMailComposeTargets() []*terminal.Window {
	var out []*terminal.Window
	for _, w := range m.Windows {
		if w != nil && w.AgentState != "" {
			out = append(out, w)
		}
	}
	return out
}

// AgentMailStartNew opens the list of agents a new message can go to. It
// works from the list of threads only, and reports whether the list opened.
// With no agent in the session it says so and opens nothing.
func (m *OS) AgentMailStartNew() bool {
	st := &m.AgentMail
	if st.Thread != 0 || st.Composing || st.Picking || st.Sending {
		return false
	}
	if !m.IsDaemonSession || m.DaemonClient == nil {
		return false
	}
	if len(m.agentMailComposeTargets()) == 0 {
		st.Error = "No agent runs in this session. Start an agent in a pane, then write to it."
		return false
	}
	st.Picking = true
	st.PickSelected = 0
	// A mailbox opened for one pane starts the list on that pane.
	for i, w := range m.agentMailComposeTargets() {
		if w.ID == st.Inbox {
			st.PickSelected = i
		}
	}
	st.Error = ""
	return true
}

// AgentMailPick chooses the agent under the picker's cursor and opens the
// message line for it.
func (m *OS) AgentMailPick() bool {
	st := &m.AgentMail
	targets := m.agentMailComposeTargets()
	if !st.Picking || len(targets) == 0 {
		return false
	}
	w := targets[clampInt(st.PickSelected, 0, len(targets)-1)]
	st.Picking = false
	st.ComposeTo = w.ID
	st.Composing = true
	st.Draft = ""
	st.Error = ""
	st.DraftAutomated = m.ProcessingRemoteKeys
	return true
}

// noteDraftTouched marks the draft automated when the key that touched it
// did not come from the keyboard. See DraftAutomated.
func (m *OS) noteDraftTouched() {
	if m.ProcessingRemoteKeys {
		m.AgentMail.DraftAutomated = true
	}
}

// AgentMailType appends typed text to the draft.
func (m *OS) AgentMailType(text string) {
	if !m.AgentMail.Composing || text == "" {
		return
	}
	m.noteDraftTouched()
	m.AgentMail.Draft += text
	m.AgentMail.Error = ""
}

// AgentMailBackspace removes the last rune of the draft.
func (m *OS) AgentMailBackspace() {
	st := &m.AgentMail
	if !st.Composing || st.Draft == "" {
		return
	}
	m.noteDraftTouched()
	r := []rune(st.Draft)
	st.Draft = string(r[:len(r)-1])
}

// AgentMailClearDraft empties the draft and keeps the reply line open. A draft
// the person cleared by hand is theirs again from here.
func (m *OS) AgentMailClearDraft() {
	if m.AgentMail.Composing {
		m.AgentMail.Draft = ""
		m.AgentMail.DraftAutomated = m.ProcessingRemoteKeys
	}
}

// agentMailReplyNonce is the attach nonce a reply carries: attach, the nonce
// of this client's attach, or "" for a reply that anything other than the
// keyboard touched. See DraftAutomated.
func (m *OS) agentMailReplyNonce(attach string) string {
	if m.AgentMail.DraftAutomated || m.ProcessingRemoteKeys {
		return ""
	}
	return attach
}

// AgentMailSendReply sends the draft as a reply to the open thread, from the
// person's address, off the Update goroutine. An empty draft sends nothing.
func (m *OS) AgentMailSendReply() tea.Cmd {
	st := &m.AgentMail
	text := strings.TrimSpace(st.Draft)
	if !st.Composing || st.Sending {
		return nil
	}
	if text == "" {
		st.Error = "Type a message first."
		return nil
	}
	// A new message goes to the chosen pane and answers nothing, so the
	// daemon starts a thread with it.
	inbox, replyTo, ok := st.ComposeTo, uint64(0), st.ComposeTo != ""
	if !ok {
		inbox, replyTo, ok = m.agentMailReplyTarget()
	}
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
	return agentMailSendCmd(m.agentMailDialer(), m.AttachedHost != "", name, inbox, replyTo, text, m.agentMailReplyNonce(m.DaemonClient.HumanNonce()))
}

// agentMailSendCmd is the send-agent-message call a reply makes. remote says
// the ring is on another machine, in which case the reply signs with this
// machine's name, as any sender over a link does. nonce is the one the daemon
// issued in this client's attach reply, and it is what makes the daemon store
// the reply as verified_human rather than as a claim. It is sent only when
// there is one, since a daemon that issues none also refuses the parameter.
func agentMailSendCmd(dial agentMailDial, remote bool, sessionName, inbox string, replyTo uint64, text, nonce string) tea.Cmd {
	return func() tea.Msg {
		client, err := dial()
		if err != nil {
			return AgentMailSentMsg{Err: err, New: replyTo == 0}
		}
		defer func() { _ = client.Close() }()
		_, err = client.Call("send-agent-message", agentMailReplyParams(remote, sessionName, inbox, replyTo, text, nonce))
		// A reply always answers a message, so no reply_to is a new message.
		return AgentMailSentMsg{Err: err, New: replyTo == 0}
	}
}

// agentMailReplyParams is the send-agent-message request a reply makes.
func agentMailReplyParams(remote bool, sessionName, inbox string, replyTo uint64, text, nonce string) map[string]any {
	params := map[string]any{
		"session": sessionName,
		"text":    text,
		"from":    session.AgentInboxHuman,
	}
	if replyTo != 0 {
		params["reply_to"] = replyTo
	}
	if remote {
		params["from_host"] = thisMachineName()
	}
	if nonce != "" {
		params["human_nonce"] = nonce
	}
	if inbox != "" {
		params["to"] = inbox
	}
	return params
}

// applyAgentMailSent closes the reply line on success and says what happened
// on failure. The sent message itself arrives as a push, like any other.
func (m *OS) applyAgentMailSent(msg AgentMailSentMsg) {
	st := &m.AgentMail
	st.Sending = false
	if msg.Err != nil {
		what := "The reply"
		if msg.New {
			what = "The message"
		}
		st.Error = what + " did not send. " + msg.Err.Error()
		return
	}
	if msg.New && st.ComposeTo != "" {
		// A new message starts the newest thread, which is the top row.
		st.Selected = 0
		st.Scroll = 0
	}
	st.Composing = false
	st.Draft = ""
	st.DraftAutomated = false
	st.ComposeTo = ""
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
