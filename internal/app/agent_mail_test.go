package app

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// The mailbox on the client. These are the claims a person can check on
// screen: mail arrives and is announced, the rail counts it, the overlay
// reads it, and a reply goes back out. Every route in is proved at the level
// that dispatches it, not by the presence of an entry in a table.

// mailOS is a client attached to "main" with the rail up, from the sections
// fixture, plus a second agent pane the messages here are between.
func mailOS(t *testing.T) *OS {
	t.Helper()
	m, _ := sectionsTestOS(t, 120, 30)
	m.ClientEventChan = make(chan ClientEvent, 4)
	return m
}

// mail is a message as the daemon would push it.
func mail(id uint64, from, fromLabel, to, toLabel, subject, text string) session.AgentMessage {
	kind := "message"
	if to == "" {
		kind = "notice"
	}
	return session.AgentMessage{
		ID: id, Kind: kind, From: from, FromLabel: fromLabel, To: to, ToLabel: toLabel,
		Subject: subject, Text: text, ThreadID: id, SentAt: 1,
	}
}

// TestMailToThePersonIsAnnouncedAndCounted: a message to human raises a dock
// message naming the sender, counts as unread for the person, and re-arms the
// client event listener. A message between two agents raises nothing and
// counts against the recipient's row instead.
func TestMailToThePersonIsAnnouncedAndCounted(t *testing.T) {
	m := mailOS(t)

	_, cmd := m.Update(AgentMailMsg{Payload: session.AgentMailPayload{
		Message: mail(7, "cccccccc3333", "build", session.AgentInboxHuman, "human", "which retry policy?", "exponential or fixed?"),
	}})
	if cmd == nil {
		t.Fatal("a mail push returned no command, so nothing is listening for the next event")
	}
	if got := m.AgentMailUnread(); got != 1 {
		t.Errorf("unread for the person = %d, want 1", got)
	}
	if n := len(m.Notifications); n == 0 {
		t.Fatal("mail to the person raised no dock message")
	}
	last := m.Notifications[len(m.Notifications)-1]
	if !strings.Contains(last.Message, "build to you: which retry policy?") {
		t.Errorf("the dock message reads %q, want the sender, the recipient and the subject", last.Message)
	}
	if last.Target == nil || last.Target.Thread != 7 {
		t.Errorf("the dock message does not point at its thread: %+v", last.Target)
	}

	// Between two agents: counted on the recipient's row, and silent.
	before := len(m.Notifications)
	m.Update(AgentMailMsg{Payload: session.AgentMailPayload{
		Message: mail(8, "cccccccc3333", "build", "bbbbbbbb2222", "refactor", "retest", "please retest"),
	}})
	if len(m.Notifications) != before {
		t.Error("a message between two agents raised a dock message")
	}
	if got := m.agentMailUnreadFor("bbbbbbbb2222"); got != 1 {
		t.Errorf("unread for the recipient pane = %d, want 1", got)
	}

	// The agent reads it: the receipt clears the row.
	m.Update(AgentMailMsg{Payload: session.AgentMailPayload{ReadIDs: []uint64{8}, ReadAt: 2}})
	if got := m.agentMailUnreadFor("bbbbbbbb2222"); got != 0 {
		t.Errorf("after the receipt unread for the recipient pane = %d, want 0", got)
	}
}

// TestRailShowsUnreadMail: the agents header carries the person's count, and
// an agent row carries its own.
func TestRailShowsUnreadMail(t *testing.T) {
	m, tree := sectionsTestOS(t, 120, 30)
	m.noteAgentMail(session.AgentMailPayload{Message: mail(1, "cccccccc3333", "build", session.AgentInboxHuman, "human", "q", "q?")})
	m.noteAgentMail(session.AgentMailPayload{Message: mail(2, "cccccccc3333", "build", "bbbbbbbb2222", "refactor", "retest", "please")})
	m.noteAgentMail(session.AgentMailPayload{Message: mail(3, "cccccccc3333", "build", "bbbbbbbb2222", "refactor", "again", "please")})

	lines := railPlain(t, m, tree)
	header := lineOf(lines, "agents")
	if header < 0 {
		t.Fatalf("no agents header:\n%s", strings.Join(lines, "\n"))
	}
	if !strings.Contains(lines[header], sidebarMailGlyph()+" 1") {
		t.Errorf("the agents header does not count the person's mail: %q", lines[header])
	}
	row := railAgentRow(m, lines, "bbbbbbbb2222")
	if !strings.Contains(row, sidebarMailGlyph()+" 2") {
		t.Errorf("the refactor row does not count its unread mail: %q", row)
	}
}

// TestRailMailTokenOpensTheMailbox is the route in from the rail, by mouse
// and by keyboard, at the level of the click and the cursor's enter.
func TestRailMailTokenOpensTheMailbox(t *testing.T) {
	m, tree := sectionsTestOS(t, 120, 30)
	railPlain(t, m, tree)

	var token sidebarRowHit
	found := false
	for _, h := range m.SidebarHits {
		if h.Kind == sidebarRowAgentMail {
			token, found = h, true
		}
	}
	if !found {
		t.Fatal("the agents header has no mail token to click")
	}
	if !m.SidebarClick(token.X0, token.Y0, false) {
		t.Fatal("a click on the mail token was not the rail's")
	}
	if !m.ShowAgentMail {
		t.Error("clicking the mail token did not open the mailbox")
	}
	m.TakeSidebarCmd()
	m.CloseAgentMail()

	// The keyboard: cursor on the token, enter.
	m.EnterSidebarFocus()
	idx := m.sidebarFirstRowOfKind(sidebarRowAgentMail)
	if idx < 0 {
		t.Fatal("the mail token is not a keyboard row")
	}
	m.sidebarSetCursor(idx)
	m.SidebarActivateCursor()
	if !m.ShowAgentMail {
		t.Error("enter on the mail token did not open the mailbox")
	}
}

// TestPaletteOpensTheMailbox is the route in from the palette, run the way
// the palette runs an entry: query typed, enter pressed.
func TestPaletteOpensTheMailbox(t *testing.T) {
	m := mailOS(t)
	m.OpenCommandPalette()
	m.CommandPaletteQuery = "Mail: open inbox"
	m.CommandPaletteSelected = 0
	m.ActivateCommandPalette()
	if !m.ShowAgentMail {
		t.Fatal("the palette entry did not open the mailbox")
	}
	if m.ShowCommandPalette {
		t.Error("the palette stayed open over the mailbox")
	}
}

// TestPaletteListsAThreadWaitingForThePerson: unread mail is findable by what
// it says, and selecting it opens that thread.
func TestPaletteListsAThreadWaitingForThePerson(t *testing.T) {
	m := mailOS(t)
	m.noteAgentMail(session.AgentMailPayload{Message: mail(5, "cccccccc3333", "build", session.AgentInboxHuman, "human", "which retry policy?", "exponential or fixed?")})
	m.OpenCommandPalette()
	m.CommandPaletteQuery = "retry policy"
	m.CommandPaletteSelected = 0
	filtered := m.filteredPaletteItems()
	if len(filtered) == 0 || !strings.Contains(filtered[0].Name, "Mail #5 build → you: which retry policy?") {
		t.Fatalf("the palette does not list the unread thread:\n%s", paletteNames(filtered))
	}
	m.ActivateCommandPalette()
	if !m.ShowAgentMail || m.AgentMail.Thread != 5 {
		t.Errorf("selecting the entry opened mailbox=%v thread=%d, want thread 5", m.ShowAgentMail, m.AgentMail.Thread)
	}
}

// TestMailboxEmptyStateTeaches: with nothing in the ring the overlay says what
// it is and what makes something appear here.
func TestMailboxEmptyStateTeaches(t *testing.T) {
	m := mailOS(t)
	m.OpenAgentMail()
	m.AgentMail.Loading = false
	out, _, _ := m.renderAgentMail()
	plain := stripANSIForTrace(out)
	for _, want := range agentMailEmptyLines {
		for _, piece := range strings.Fields(want) {
			if !strings.Contains(plain, piece) {
				t.Errorf("the empty state is missing %q (from %q):\n%s", piece, want, plain)
				break
			}
		}
	}
	if !strings.Contains(plain, "send-agent-message") {
		t.Errorf("the empty state does not say what makes mail appear:\n%s", plain)
	}
}

// TestMailboxReadsAThreadAndRepliesToTheAgent: the list names the thread, the
// thread view shows the body, and a reply goes to the agent that last spoke,
// from the person's address, threaded on the newest message.
func TestMailboxReadsAThreadAndRepliesToTheAgent(t *testing.T) {
	m := mailOS(t)
	m.noteAgentMail(session.AgentMailPayload{Message: mail(5, "cccccccc3333", "build", session.AgentInboxHuman, "human", "which retry policy?", "exponential or fixed?")})
	m.OpenAgentMail()
	m.AgentMail.Loading = false

	list, _, rows := m.renderAgentMail()
	if plain := stripANSIForTrace(list); !strings.Contains(plain, "build → you") || !strings.Contains(plain, "which retry policy?") {
		t.Fatalf("the list does not name the thread:\n%s", plain)
	}
	if len(rows) != 1 {
		t.Fatalf("the list published %d hit rows, want 1", len(rows))
	}

	if cmd := m.AgentMailOpenSelected(); cmd == nil {
		t.Error("opening a thread with mail for the person returned no marking command")
	}
	if m.AgentMail.Thread != 5 {
		t.Fatalf("opened thread %d, want 5", m.AgentMail.Thread)
	}
	thread, _, _ := m.renderAgentMail()
	if plain := stripANSIForTrace(thread); !strings.Contains(plain, "exponential or fixed?") {
		t.Fatalf("the thread view does not show the body:\n%s", plain)
	}

	if !m.AgentMailStartReply() {
		t.Fatal("r did not open the reply line")
	}
	m.AgentMailType("take exponential")
	inbox, replyTo, ok := m.agentMailReplyTarget()
	if !ok || inbox != "cccccccc3333" || replyTo != 5 {
		t.Errorf("the reply is addressed to %q answering %d, want the build pane answering 5", inbox, replyTo)
	}
	if cmd := m.AgentMailSendReply(); cmd == nil {
		t.Fatal("enter on a draft returned no send command")
	}
	if !m.AgentMail.Sending {
		t.Error("the reply line does not say it is sending")
	}
	m.applyAgentMailSent(AgentMailSentMsg{})
	if m.AgentMail.Composing || m.AgentMail.Draft != "" {
		t.Error("a sent reply left the reply line open")
	}
}

// TestMailDockMessageOpensTheThread: activating a dock message about mail
// lands on the thread, where the reply is, not on the pane.
func TestMailDockMessageOpensTheThread(t *testing.T) {
	m := mailOS(t)
	// From nobody in particular: a pane-less sender is the case where the only
	// place the message can lead is the thread.
	m.Update(AgentMailMsg{Payload: session.AgentMailPayload{
		Message: mail(9, "", "", session.AgentInboxHuman, "human", "q", "q?"),
	}})
	if !m.JumpToNotification() {
		t.Fatal("there was no dock message to activate")
	}
	if !m.ShowAgentMail || m.AgentMail.Thread != 9 {
		t.Errorf("activating the message opened mailbox=%v thread=%d, want thread 9", m.ShowAgentMail, m.AgentMail.Thread)
	}
}

// TestMailboxKeepsTheIdleTickIdle: a mirror full of mail costs the
// maintenance tick nothing. The tick must not scan it.
func TestMailboxKeepsTheIdleTickIdle(t *testing.T) {
	m := idleOS(t, 3)
	for i := uint64(1); i <= 50; i++ {
		m.noteAgentMail(session.AgentMailPayload{Message: mail(i, "a", "a", "b", "b", "s", "t")})
	}
	for range 5 {
		m.Update(TickerMsg(time.Now()))
	}
	_, work0, _ := m.TickStats()
	for range 50 {
		m.Update(TickerMsg(time.Now()))
	}
	_, work1, _ := m.TickStats()
	if work1 != work0 {
		t.Errorf("the idle tick did %d units of work with mail in the mirror, want 0", work1-work0)
	}
}

// TestMailPushIsMappedToItsMessage: the client event listener turns a mail
// push, and a session switch's request for a re-read, into the messages Update
// handles. The read loop never touches the model.
func TestMailPushIsMappedToItsMessage(t *testing.T) {
	ch := make(chan ClientEvent, 2)
	ch <- ClientEvent{Type: "agent-mail", Mail: session.AgentMailPayload{Message: mail(4, "a", "a", "b", "b", "s", "t")}}
	got, ok := ListenForClientEvents(ch)().(AgentMailMsg)
	if !ok || got.Payload.Message.ID != 4 {
		t.Errorf("a mail push came out of the listener as %T %+v, want AgentMailMsg for message 4", got, got)
	}
	ch <- ClientEvent{Type: "agent-mail-load"}
	if _, ok := ListenForClientEvents(ch)().(AgentMailLoadMsg); !ok {
		t.Error("a re-read request did not come out of the listener as AgentMailLoadMsg")
	}
}

// TestMailChangesTheRailSignature: the rail's render cache keys on the
// mailbox, so a count that changed is drawn rather than served from the frame
// before.
func TestMailChangesTheRailSignature(t *testing.T) {
	m, _ := sectionsTestOS(t, 120, 30)
	before := m.sidebarSignature()
	m.AgentMail.Messages = append(m.AgentMail.Messages, mail(1, "cccccccc3333", "build", "bbbbbbbb2222", "refactor", "s", "t"))
	m.AgentMail.Gen++
	if m.sidebarSignature() == before {
		t.Error("mail arriving left the rail signature unchanged, so a cached rail would hide the count")
	}
}
