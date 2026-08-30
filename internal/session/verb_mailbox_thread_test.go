package session

import (
	"slices"
	"strconv"
	"testing"
	"time"
)

// Threading tests. A thread id is a message id, so everything here is about one
// rule and the two ways the bounded ring makes it hard: a reply belongs to the
// thread of the message it answers, and the ring forgets.

// idOf reads a message id out of a verb result.
func idOf(t *testing.T, res map[string]any, key string) uint64 {
	t.Helper()
	n, ok := res[key].(float64)
	if !ok {
		t.Fatalf("result has no %s: %v", key, res)
	}
	return uint64(n)
}

// messageIDs lists the ids a read returned, in the order it returned them.
func messageIDs(t *testing.T, res map[string]any) []uint64 {
	t.Helper()
	msgs, _ := res["messages"].([]any)
	out := make([]uint64, 0, len(msgs))
	for _, raw := range msgs {
		m, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("message is not an object: %v", raw)
		}
		n, _ := m["id"].(float64)
		out = append(out, uint64(n))
	}
	return out
}

// TestAThreadHoldsTheWholeConversation is the shape the feature exists for: A
// says something, B answers it, A answers the answer, and one filter returns
// those three in the order they were said and nothing else. The reply to the
// reply is the part that matters: it names the middle message, not the first,
// and it still has to land in the same thread.
func TestAThreadHoldsTheWholeConversation(t *testing.T) {
	d, sp := startTestDaemon(t)
	_, a, b := twoWindowSession(t, d, "thread")
	c := dialVerb(t, sp)

	first := result(t, c.call(t, `{"id":1,"verb":"send-agent-message","params":{"session":"thread","to":"`+b+`","from":"`+a+`","text":"please retest"}}`))
	root := idOf(t, first, "message_id")
	if got := idOf(t, first, "thread_id"); got != root {
		t.Fatalf("a message that starts a thread has thread_id %d, want its own id %d", got, root)
	}

	reply := result(t, c.call(t, `{"id":2,"verb":"send-agent-message","params":{"session":"thread","to":"`+a+`","from":"`+b+`","reply_to":`+u(root)+`,"text":"retested, green"}}`))
	replyID := idOf(t, reply, "message_id")
	if got := idOf(t, reply, "thread_id"); got != root {
		t.Fatalf("a reply is in thread %d, want the thread it answered, %d", got, root)
	}

	// The reply to the reply names the middle message, and must not start a
	// thread of its own.
	third := result(t, c.call(t, `{"id":3,"verb":"send-agent-message","params":{"session":"thread","to":"`+b+`","from":"`+a+`","reply_to":`+u(replyID)+`,"text":"thanks, merging"}}`))
	thirdID := idOf(t, third, "message_id")
	if got := idOf(t, third, "thread_id"); got != root {
		t.Fatalf("a reply to a reply is in thread %d, want %d", got, root)
	}

	// Something said in the same session that is not part of the conversation.
	other := result(t, c.call(t, `{"id":4,"verb":"send-agent-message","params":{"session":"thread","to":"`+b+`","from":"`+a+`","text":"unrelated"}}`))
	otherID := idOf(t, other, "message_id")

	want := []uint64{root, replyID, thirdID}
	read := result(t, c.call(t, `{"id":5,"verb":"read-agent-messages","params":{"session":"thread","thread":`+u(root)+`}}`))
	if got := messageIDs(t, read); !slices.Equal(got, want) {
		t.Fatalf("the thread read back as %v, want %v in that order", got, want)
	}
	if idOf(t, read, "thread") != root {
		t.Errorf("the read did not report which thread it filtered on: %v", read["thread"])
	}

	// Any id in the thread names the thread, so a caller holding the reply it
	// just read does not have to trace back to the first message.
	byReply := result(t, c.call(t, `{"id":6,"verb":"read-agent-messages","params":{"session":"thread","thread":`+u(replyID)+`}}`))
	if got := messageIDs(t, byReply); !slices.Equal(got, want) {
		t.Fatalf("filtering on a reply's id read back %v, want the whole thread %v", got, want)
	}

	// And the unrelated message is its own thread, holding only itself.
	alone := result(t, c.call(t, `{"id":7,"verb":"read-agent-messages","params":{"session":"thread","thread":`+u(otherID)+`}}`))
	if got := messageIDs(t, alone); !slices.Equal(got, []uint64{otherID}) {
		t.Fatalf("an unthreaded message read back as %v, want only itself", got)
	}
}

// TestAnUnthreadedMessageCarriesItsOwnThreadOnTheWire is the zero case, checked
// where a reader sees it rather than in the struct. A message nobody replied to
// must still name a thread, because a reader that has to special-case a missing
// thread id is a reader that will get it wrong; and reply_to must be absent
// rather than present and zero, because zero is not a message id.
func TestAnUnthreadedMessageCarriesItsOwnThreadOnTheWire(t *testing.T) {
	d, sp := startTestDaemon(t)
	_, a, b := twoWindowSession(t, d, "zero")
	c := dialVerb(t, sp)

	sent := result(t, c.call(t, `{"id":1,"verb":"send-agent-message","params":{"session":"zero","to":"`+b+`","from":"`+a+`","text":"no reply_to here"}}`))
	id := idOf(t, sent, "message_id")
	if v, ok := sent["reply_to"].(float64); !ok || v != 0 {
		t.Errorf("send reported reply_to = %v, want 0", sent["reply_to"])
	}
	if sent["reply_to_missing"] != false {
		t.Errorf("send reported reply_to_missing = %v, want false", sent["reply_to_missing"])
	}

	read := result(t, c.call(t, `{"id":2,"verb":"read-agent-messages","params":{"session":"zero","to":"`+b+`"}}`))
	msgs, _ := read["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	m := msgs[0].(map[string]any)
	if _, present := m["reply_to"]; present {
		t.Errorf("an unthreaded message carries reply_to on the wire: %v", m["reply_to"])
	}
	if _, present := m["reply_to_missing"]; present {
		t.Errorf("an unthreaded message carries reply_to_missing on the wire: %v", m["reply_to_missing"])
	}
	thread, present := m["thread_id"].(float64)
	if !present {
		t.Fatalf("a message arrived with no thread_id: %v", m)
	}
	if uint64(thread) != id {
		t.Errorf("thread_id = %v, want the message's own id %d", thread, id)
	}
	// And it is filterable by that thread like any other.
	byThread := result(t, c.call(t, `{"id":3,"verb":"read-agent-messages","params":{"session":"zero","thread":`+u(id)+`}}`))
	if got := messageIDs(t, byThread); !slices.Equal(got, []uint64{id}) {
		t.Errorf("filtering on an unthreaded message's own thread read back %v, want %v", got, []uint64{id})
	}
}

// TestReplyToAMessageThatNeverExistedIsRefused separates the caller's mistake
// from the ring's forgetting. An id past the last one issued has never named
// anything, so accepting it would store a reply pointing at nothing, forever.
func TestReplyToAMessageThatNeverExistedIsRefused(t *testing.T) {
	d, sp := startTestDaemon(t)
	_, a, b := twoWindowSession(t, d, "ghost")
	c := dialVerb(t, sp)

	resp := c.call(t, `{"id":1,"verb":"send-agent-message","params":{"session":"ghost","to":"`+b+`","from":"`+a+`","reply_to":99999,"text":"answering nothing"}}`)
	if code := errCode(t, resp); code != ErrVerbInvalidParams {
		t.Fatalf("code = %q, want %q", code, ErrVerbInvalidParams)
	}
	e := resp["error"].(map[string]any)
	hint, _ := e["hint"].(map[string]any)
	if hint == nil || hint["param"] != "reply_to" {
		t.Errorf("the refusal did not name reply_to: %v", e)
	}

	// And nothing was stored: a refused send must not half-run.
	read := result(t, c.call(t, `{"id":2,"verb":"read-agent-messages","params":{"session":"ghost"}}`))
	if n, _ := read["messages"].([]any); len(n) != 0 {
		t.Errorf("the refused reply was stored anyway: %v", read)
	}
}

// TestReplyToAnEvictedParentStillThreads is the case the bound forces, proved by
// filling the ring rather than by asserting a number.
//
// The ring drops its oldest, so the message being answered will often be gone.
// Refusing the reply would be wrong: the reply is still the answer to that
// message, and the sender has no way to know the ring had moved on. Silently
// starting an unrelated thread would be worse, because it loses the one fact the
// reader wanted. So the thread is rooted on the id the reply named, every reply
// to that same parent lands together, and the message says the parent is gone.
func TestReplyToAnEvictedParentStillThreads(t *testing.T) {
	bus := newAgentBus()

	root := bus.send("s", AgentMessage{Kind: agentMsgNotice, Text: "the question"})
	live := bus.send("s", AgentMessage{Kind: agentMsgNotice, Text: "an answer", ReplyTo: root.ID})
	if live.ThreadID != root.ID || live.ReplyToMissing {
		t.Fatalf("a reply to a live parent: thread %d missing %v, want thread %d and not missing", live.ThreadID, live.ReplyToMissing, root.ID)
	}

	// Fill the ring until the root has actually been dropped, rather than
	// assuming a count does it.
	for range agentMailboxMaxMessages + 8 {
		bus.send("s", AgentMessage{Kind: agentMsgNotice, Text: "filler"})
	}
	bus.mu.Lock()
	gone := bus.box("s").find(root.ID) == nil
	bus.mu.Unlock()
	if !gone {
		t.Fatal("the ring still holds the root, so this test proves nothing")
	}

	late := bus.send("s", AgentMessage{Kind: agentMsgNotice, Text: "a late answer", ReplyTo: root.ID})
	if late.ThreadID != root.ID {
		t.Errorf("a reply to an evicted parent is in thread %d, want the parent's id %d", late.ThreadID, root.ID)
	}
	if !late.ReplyToMissing {
		t.Error("a reply to an evicted parent did not report that the parent is gone")
	}

	// The property that matters: two replies to the same evicted parent are
	// still one conversation.
	later := bus.send("s", AgentMessage{Kind: agentMsgNotice, Text: "and another", ReplyTo: root.ID})
	if later.ThreadID != late.ThreadID {
		t.Errorf("two replies to the same evicted parent are in threads %d and %d, want one thread", late.ThreadID, later.ThreadID)
	}
	res := bus.read("s", readQuery{thread: root.ID, limit: 1000})
	if len(res.Messages) != 2 || res.Messages[0].ID != late.ID || res.Messages[1].ID != later.ID {
		t.Errorf("the thread of an evicted parent read back %d message(s), want the two replies", len(res.Messages))
	}
}

// TestReplyToAnEvictedParentIsAcceptedOverTheSocket is the same case at the
// verb, because that is where a refusal would live if there were one.
func TestReplyToAnEvictedParentIsAcceptedOverTheSocket(t *testing.T) {
	d, sp := startTestDaemon(t)
	_, a, b := twoWindowSession(t, d, "aged")
	c := dialVerb(t, sp)

	first := result(t, c.call(t, `{"id":1,"verb":"send-agent-message","params":{"session":"aged","to":"`+b+`","from":"`+a+`","text":"the question"}}`))
	root := idOf(t, first, "message_id")

	// Fill the ring past the root. The rate cap counts calls, not stores, so
	// the filling goes straight at the ring; the eviction it causes is the real
	// one.
	for range agentMailboxMaxMessages + 2 {
		d.agents.send("aged", AgentMessage{Kind: agentMsgNotice, Text: "filler"})
	}
	d.agents.mu.Lock()
	gone := d.agents.box("aged").find(root) == nil
	d.agents.mu.Unlock()
	if !gone {
		t.Fatal("the ring still holds the root, so this test proves nothing")
	}

	late := result(t, c.call(t, `{"id":2,"verb":"send-agent-message","params":{"session":"aged","to":"`+b+`","from":"`+a+`","reply_to":`+u(root)+`,"text":"late answer"}}`))
	if idOf(t, late, "thread_id") != root {
		t.Errorf("thread_id = %v, want the evicted parent's id %d", late["thread_id"], root)
	}
	if late["reply_to_missing"] != true {
		t.Errorf("reply_to_missing = %v, want true for a parent the ring dropped", late["reply_to_missing"])
	}
}

// TestAnUnknownThreadReadsEmptyRatherThanFailing pins the answer for a thread
// nothing is left of. It cannot be told apart from a thread nobody started,
// because the ring forgets, so both give the same empty answer instead of an
// error a caller would have to guess the meaning of.
func TestAnUnknownThreadReadsEmptyRatherThanFailing(t *testing.T) {
	d, sp := startTestDaemon(t)
	_, a, b := twoWindowSession(t, d, "empty")
	c := dialVerb(t, sp)

	c.call(t, `{"id":1,"verb":"send-agent-message","params":{"session":"empty","to":"`+b+`","from":"`+a+`","text":"in some other thread"}}`)

	read := result(t, c.call(t, `{"id":2,"verb":"read-agent-messages","params":{"session":"empty","thread":4242}}`))
	if n, _ := read["messages"].([]any); len(n) != 0 {
		t.Errorf("an unknown thread returned %d message(s), want none", len(n))
	}
	if read["total"] != float64(0) {
		t.Errorf("total = %v, want 0", read["total"])
	}
	if read["thread"] != float64(4242) {
		t.Errorf("thread = %v, want the id that was asked for", read["thread"])
	}
}

// TestThreadIdsMeanNothingOutsideTheirSession holds the boundary the rings
// already have. Ids are issued by one counter, so an id from another session is
// a number this session's ring has never held, and it must read as empty rather
// than as somebody else's conversation.
func TestThreadIdsMeanNothingOutsideTheirSession(t *testing.T) {
	d, sp := startTestDaemon(t)
	_, a1, b1 := twoWindowSession(t, d, "one")
	_, a2, b2 := twoWindowSession(t, d, "two")
	c := dialVerb(t, sp)

	here := result(t, c.call(t, `{"id":1,"verb":"send-agent-message","params":{"session":"one","to":"`+b1+`","from":"`+a1+`","text":"session one"}}`))
	mine := idOf(t, here, "message_id")
	c.call(t, `{"id":2,"verb":"send-agent-message","params":{"session":"two","to":"`+b2+`","from":"`+a2+`","text":"session two"}}`)

	read := result(t, c.call(t, `{"id":3,"verb":"read-agent-messages","params":{"session":"two","thread":`+u(mine)+`}}`))
	if n, _ := read["messages"].([]any); len(n) != 0 {
		t.Errorf("another session's thread id matched %d message(s) here, want none", len(n))
	}
}

// TestWaitForAThreadIgnoresEverythingElse is the precision the filter is for. A
// wait with no thread wakes on any mail, which is right for "am I wanted" and
// wrong for "did anyone answer me": the message that arrives may be about
// something else entirely. With a thread, only the answer resolves it.
func TestWaitForAThreadIgnoresEverythingElse(t *testing.T) {
	d, sp := startTestDaemon(t)
	_, a, b := twoWindowSession(t, d, "waitthread")
	c := dialVerb(t, sp)

	asked := result(t, c.call(t, `{"id":1,"verb":"send-agent-message","params":{"session":"waitthread","to":"`+b+`","from":"`+a+`","text":"please retest"}}`))
	root := idOf(t, asked, "message_id")

	waiter := dialVerb(t, sp)
	done := make(chan map[string]any, 1)
	go func() {
		done <- waiter.call(t, `{"id":1,"verb":"wait-for","params":{"condition":"agent-message","session":"waitthread","window":"`+a+`","thread":`+u(root)+`,"timeout":10000}}`)
	}()
	time.Sleep(200 * time.Millisecond)

	// Mail that is not an answer. Without the thread this resolves the wait.
	c.call(t, `{"id":2,"verb":"send-agent-message","params":{"session":"waitthread","to":"`+a+`","from":"`+b+`","text":"unrelated, about something else"}}`)
	time.Sleep(300 * time.Millisecond)
	select {
	case resp := <-done:
		t.Fatalf("the wait resolved on mail from another thread: %v", resp)
	default:
	}

	answer := result(t, c.call(t, `{"id":3,"verb":"send-agent-message","params":{"session":"waitthread","to":"`+a+`","from":"`+b+`","reply_to":`+u(root)+`,"text":"retested, green"}}`))
	answerID := idOf(t, answer, "message_id")

	select {
	case resp := <-done:
		res := result(t, resp)
		if res["matched"] != true {
			t.Fatalf("wait did not match: %v", res)
		}
		if got := idOf(t, res, "message_id"); got != answerID {
			t.Errorf("the wait matched message %d, want the answer %d", got, answerID)
		}
		if got := idOf(t, res, "thread_id"); got != root {
			t.Errorf("the wait reported thread %d, want %d", got, root)
		}
	case <-time.After(12 * time.Second):
		t.Fatal("the wait did not resolve on the answer to its own thread")
	}
}

// TestAskAgentStillPutsNothingInTheRing records a decision as much as a fact.
// ask-agent types at a keyboard and reads back what the pane printed; its reply
// is pane output, not mail. Making it post an ask into the ring and wait for a
// message answering it would read as precision and would in fact break it
// against every harness that exists, because none of them send mail. So the
// mailbox's threading stops at the mailbox, and ask-agent is unchanged.
func TestAskAgentStillPutsNothingInTheRing(t *testing.T) {
	d, sp := startTestDaemon(t)
	_, a, b := twoWindowSession(t, d, "asknomail")
	c := dialVerb(t, sp)

	result(t, c.call(t, `{"id":1,"verb":"ask-agent","params":{"session":"asknomail","window":"`+b+`","from":"`+a+`","text":"echo tuios_ask_reply","settle":700,"timeout":15000}}`))

	read := result(t, c.call(t, `{"id":2,"verb":"read-agent-messages","params":{"session":"asknomail"}}`))
	if n, _ := read["messages"].([]any); len(n) != 0 {
		t.Errorf("ask-agent left %d message(s) in the ring, want none", len(n))
	}
}

// u renders an id for a JSON literal in these tests.
func u(id uint64) string {
	return strconv.FormatUint(id, 10)
}
