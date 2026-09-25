package session

import (
	"slices"
	"strconv"
	"testing"
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

// u renders an id for a JSON literal in these tests.
func u(id uint64) string {
	return strconv.FormatUint(id, 10)
}
