package session

import (
	"strings"
	"testing"
	"time"
)

// held reports whether the session's ring still holds the message with id.
func held(bus *agentBus, session string, id uint64) bool {
	bus.mu.Lock()
	defer bus.mu.Unlock()
	return bus.box(session).find(id) != nil
}

// TestFullRingDropsReadEntriesBeforeUnreadMail is the reason the ring has an
// order at all: unread mail is the one entry whose loss loses something, so a
// full ring drops read mail, ask records and notices first.
func TestFullRingDropsReadEntriesBeforeUnreadMail(t *testing.T) {
	bus := newAgentBus()
	read := bus.send("s", AgentMessage{Kind: agentMsgDirect, To: "alice", Text: "read this one"})
	bus.read("s", readQuery{inbox: "alice"})
	// Read an hour ago: mail read just now is kept longer
	// (TestFullRingKeepsMailReadJustNow).
	age(bus, "s", read.ID, 0, time.Hour)
	unread := bus.send("s", AgentMessage{Kind: agentMsgDirect, To: "alice", Text: "left unread"})
	ask := bus.send("s", AgentMessage{Kind: agentMsgAsk, To: "alice", Text: "an answer"})
	for range agentMailboxMaxMessages {
		bus.send("s", AgentMessage{Kind: agentMsgNotice, Text: "filler"})
	}

	if !held(bus, "s", unread.ID) {
		t.Error("the full ring dropped alice's unread message while it held notices")
	}
	if held(bus, "s", read.ID) || held(bus, "s", ask.ID) {
		t.Error("the full ring kept a read message or an ask record and dropped something else instead")
	}
	res := bus.read("s", readQuery{inbox: "alice", peek: true, limit: 1000})
	if res.EvictedUnread != 0 {
		t.Errorf("alice's inbox reports %d unread messages dropped, want 0: the one dropped was read", res.EvictedUnread)
	}

	// The positive half: with nothing left but unread mail, the oldest unread
	// message goes, the caps hold, and alice is told.
	for range agentMailboxMaxMessages {
		bus.send("s", AgentMessage{Kind: agentMsgDirect, To: "alice", Text: "more"})
	}
	if held(bus, "s", unread.ID) {
		t.Fatal("a ring of nothing but unread mail kept its oldest, so the cap did not hold")
	}
	res = bus.read("s", readQuery{inbox: "alice", peek: true, limit: 1000})
	if res.Total != agentMailboxMaxMessages {
		t.Errorf("alice's inbox holds %d, want the cap of %d", res.Total, agentMailboxMaxMessages)
	}
	if res.EvictedUnread == 0 {
		t.Error("alice lost unread mail and her inbox read does not say so")
	}
}

// age moves the send and read times of the message with id back by sent and
// read, so a test sets how old an entry is without waiting.
func age(bus *agentBus, session string, id uint64, sent, read time.Duration) {
	bus.mu.Lock()
	defer bus.mu.Unlock()
	m := bus.box(session).find(id)
	m.SentAt -= int64(sent)
	if m.ReadAt != 0 {
		m.ReadAt -= int64(read)
	}
}

// TestFullRingKeepsMailReadJustNow is the drop a lead saw: its reply to a
// message it had read seconds before said the message was gone. The ring was
// full of unread mail to other inboxes, and the old order dropped what nobody
// waited to read first, which was the one message just read. Unread mail a
// day old now goes before it.
//
// Negative control: with victim as it was (the oldest entry nobody waits to
// read, else the oldest), the message read just now is the first to go and
// this test fails.
func TestFullRingKeepsMailReadJustNow(t *testing.T) {
	bus := newAgentBus()
	var stale []AgentMessage
	for range agentMailboxMaxMessages - 1 {
		m := bus.send("s", AgentMessage{Kind: agentMsgDirect, To: "human", Text: "a report nobody read"})
		age(bus, "s", m.ID, 48*time.Hour, 0)
		stale = append(stale, m)
	}
	fresh := bus.send("s", AgentMessage{Kind: agentMsgDirect, To: "lead", From: "worker", Text: "may I push?"})
	bus.read("s", readQuery{inbox: "lead"})

	// The ring is full. The lead answers.
	reply := bus.send("s", AgentMessage{Kind: agentMsgDirect, To: "worker", From: "lead", Text: "yes", ReplyTo: fresh.ID})

	if !held(bus, "s", fresh.ID) {
		t.Fatal("the full ring dropped the message read just now while it held day-old unread mail")
	}
	if reply.ReplyToMissing {
		t.Error("the reply to the message read just now says its parent is gone")
	}
	if held(bus, "s", stale[0].ID) {
		t.Error("the oldest day-old unread message is still held, so the ring is over its cap")
	}
	if got := bus.read("s", readQuery{inbox: "human", peek: true}).EvictedUnread; got != 1 {
		t.Errorf("the human inbox reports %d unread messages dropped, want 1", got)
	}

	// The other side of the order: unread mail younger than a day still
	// wins over mail read just now.
	bus2 := newAgentBus()
	for range agentMailboxMaxMessages - 1 {
		bus2.send("s", AgentMessage{Kind: agentMsgDirect, To: "human", Text: "a report from this morning"})
	}
	fresh2 := bus2.send("s", AgentMessage{Kind: agentMsgDirect, To: "lead", Text: "may I push?"})
	bus2.read("s", readQuery{inbox: "lead"})
	bus2.send("s", AgentMessage{Kind: agentMsgDirect, To: "worker", Text: "yes", ReplyTo: fresh2.ID})
	if held(bus2, "s", fresh2.ID) {
		t.Error("the ring kept mail read just now over unread mail from the same day")
	}
}

// TestInboxReadReportsOnlyItsOwnUnreadDrops pins who is told what. The ring
// is the session's, so a count for the session in every inbox tells every
// reader it lost mail, and the reader that did lose some cannot tell.
func TestInboxReadReportsOnlyItsOwnUnreadDrops(t *testing.T) {
	bus := newAgentBus()
	for range 3 {
		bus.send("s", AgentMessage{Kind: agentMsgDirect, To: "alice", Text: "never read"})
	}
	bus.send("s", AgentMessage{Kind: agentMsgDirect, To: "bob", Text: "read in time"})
	bus.read("s", readQuery{inbox: "bob"})
	for range agentMailboxMaxMessages {
		bus.send("s", AgentMessage{Kind: agentMsgDirect, To: "carol", Text: "unread"})
	}

	alice := bus.read("s", readQuery{inbox: "alice", peek: true})
	bob := bus.read("s", readQuery{inbox: "bob", peek: true})
	all := bus.read("s", readQuery{limit: 1000})
	if alice.EvictedUnread != 3 {
		t.Errorf("alice's inbox reports %d unread messages dropped, want 3", alice.EvictedUnread)
	}
	if bob.EvictedUnread != 0 {
		t.Errorf("bob's inbox reports %d unread messages dropped, want 0: his only message was read", bob.EvictedUnread)
	}
	if all.Evicted != 4 || all.EvictedUnread != 3 {
		t.Errorf("the session read reports %d dropped and %d unread, want 4 and 3", all.Evicted, all.EvictedUnread)
	}
}

// TestNewestEntryIsNeverItsOwnVictim covers the send that finds the ring full
// of unread mail. Dropping what was just sent would answer the send with an id
// nobody can read back, so the oldest unread message goes instead.
func TestNewestEntryIsNeverItsOwnVictim(t *testing.T) {
	bus := newAgentBus()
	first := bus.send("s", AgentMessage{Kind: agentMsgDirect, To: "alice", Text: "oldest"})
	for range agentMailboxMaxMessages - 1 {
		bus.send("s", AgentMessage{Kind: agentMsgDirect, To: "alice", Text: "unread"})
	}
	notice := bus.send("s", AgentMessage{Kind: agentMsgNotice, Text: "deploying"})
	if !held(bus, "s", notice.ID) {
		t.Error("the notice just sent was dropped on arrival")
	}
	if held(bus, "s", first.ID) {
		t.Error("the ring is over its cap: the oldest unread message is still held")
	}
}

// TestByteCapAlsoSparesUnreadMail is the same order under the second cap,
// which is the one a ring of large messages reaches first.
func TestByteCapAlsoSparesUnreadMail(t *testing.T) {
	bus := newAgentBus()
	unread := bus.send("s", AgentMessage{Kind: agentMsgDirect, To: "alice", Text: "short and unread"})
	big := strings.Repeat("x", agentMsgMaxText)
	for range 2 * agentMailboxMaxBytes / agentMsgMaxText {
		bus.send("s", AgentMessage{Kind: agentMsgNotice, Text: big})
	}
	bus.mu.Lock()
	bytes := bus.box("s").bytes
	bus.mu.Unlock()
	if bytes > agentMailboxMaxBytes {
		t.Errorf("ring holds %d bytes, over the %d cap", bytes, agentMailboxMaxBytes)
	}
	if !held(bus, "s", unread.ID) {
		t.Error("the byte cap dropped alice's unread message while it held notices")
	}
}

// TestUnreadDropsSurviveAHandoff: a live upgrade carries the ring, so it
// carries what the ring owes each inbox too. A pane that closed has its count
// folded into the session's total, so the per-inbox map does not keep one key
// for every pane that ever lost mail, and the total still says what was lost.
func TestUnreadDropsSurviveAHandoff(t *testing.T) {
	bus := newAgentBus()
	bus.send("s", AgentMessage{Kind: agentMsgDirect, To: "alice", Text: "never read"})
	bus.send("s", AgentMessage{Kind: agentMsgDirect, To: "dave", Text: "never read"})
	for range agentMailboxMaxMessages {
		bus.send("s", AgentMessage{Kind: agentMsgDirect, To: "bob", Text: "unread"})
	}
	bus.forgetInbox("s", "dave")
	var mail handoffMail
	roundTripJSON(t, bus.handoffState(), &mail)
	next := newAgentBus()
	next.restore(mail)
	if got := next.read("s", readQuery{inbox: "alice", peek: true}).EvictedUnread; got != 1 {
		t.Errorf("after the handoff alice's inbox reports %d unread messages dropped, want 1", got)
	}
	if _, kept := mail.Boxes["s"].EvictedUnread["dave"]; kept {
		t.Error("the closed pane's count is still kept by its window")
	}
	if got := next.read("s", readQuery{limit: 1000}).EvictedUnread; got != 2 {
		t.Errorf("after the handoff the session reports %d unread messages dropped, want 2", got)
	}
}
