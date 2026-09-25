package session

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// setAgentState reports a state for a window over the verb socket.
func setAgentState(t *testing.T, c *verbConn, session, window, state, kind, message string) {
	t.Helper()
	params := map[string]any{"session": session, "window": window, "state": state}
	if kind != "" {
		params["kind"] = kind
	}
	if message != "" {
		params["message"] = message
	}
	raw, _ := json.Marshal(params)
	result(t, c.call(t, fmt.Sprintf(`{"id":1,"verb":"set-agent-state","params":%s}`, raw)))
}

// listAttention returns the items list-attention answers, and its result.
func listAttention(t *testing.T, c *verbConn, params string) ([]map[string]any, map[string]any) {
	t.Helper()
	if params == "" {
		params = "{}"
	}
	res := result(t, c.call(t, `{"id":1,"verb":"list-attention","params":`+params+`}`))
	raw, _ := res["items"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, it := range raw {
		out = append(out, it.(map[string]any))
	}
	return out, res
}

// waitAttention polls list-attention until pred holds.
func waitAttention(t *testing.T, c *verbConn, why string, pred func([]map[string]any) bool) []map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second * testDeadlineScale)
	for {
		items, _ := listAttention(t, c, "")
		if pred(items) {
			return items
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: the Inbox holds %v", why, items)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func hasKind(kind, window string) func([]map[string]any) bool {
	return func(items []map[string]any) bool {
		for _, it := range items {
			if it["kind"] == kind && (window == "" || it["window"] == window) {
				return true
			}
		}
		return false
	}
}

func isEmpty(items []map[string]any) bool { return len(items) == 0 }

// TestInboxEventsResumeFromAListing holds the contract the client relies on:
// list, then subscribe after the listing's seq, and nothing is missed or
// repeated.
func TestInboxEventsResumeFromAListing(t *testing.T) {
	d, sp := startTestDaemon(t)
	_, a, _ := twoWindowSession(t, d, "work")
	c := dialVerb(t, sp)

	_, res := listAttention(t, c, "")
	seq := uint64(res["seq"].(float64))
	boot := res["boot_id"].(string)

	setAgentState(t, c, "work", a, "needs_input", "approval", "ok?")

	sub := dialVerb(t, sp)
	ack := result(t, sub.call(t, fmt.Sprintf(`{"id":1,"verb":"subscribe","params":{"types":["attention"],"after_seq":%d,"boot_id":%q}}`, seq, boot)))
	if ack["replayed"] != float64(1) {
		t.Fatalf("the resume replayed %v attention events, want 1", ack["replayed"])
	}
	ev := readStreamEvent(t, sub)
	att, _ := ev["attention"].(map[string]any)
	if ev["type"] != EventAttention || ev["action"] != AttentionOpened || att["kind"] != AttentionApproval || ev["session"] != "work" || ev["window"] != a {
		t.Fatalf("replayed %v", ev)
	}

	setAgentState(t, c, "work", a, "working", "", "")
	ev = readStreamEvent(t, sub)
	att, _ = ev["attention"].(map[string]any)
	if ev["action"] != AttentionClosed || att["closed"] != AttentionClosedResolved {
		t.Fatalf("live close was %v", ev)
	}
}

// readStreamEvent reads one event line off a subscribed connection.
func readStreamEvent(t *testing.T, c *verbConn) map[string]any {
	t.Helper()
	_ = c.conn.SetReadDeadline(time.Now().Add(5 * time.Second * testDeadlineScale))
	line, err := c.r.ReadBytes('\n')
	if err != nil {
		t.Fatalf("read event: %v", err)
	}
	var ev map[string]any
	if err := json.Unmarshal(line, &ev); err != nil {
		t.Fatalf("decode event %q: %v", line, err)
	}
	return ev
}

// TestInboxDismissIsForThePerson covers who may clear the queue: a caller with
// no attach nonce is refused, and a client attached right now is not.
func TestInboxDismissIsForThePerson(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess, a, _ := twoWindowSession(t, d, "work")
	makeSessionWithWindow(t, d, "other")
	c := dialVerb(t, sp)

	setAgentState(t, c, "work", a, "working", "", "")
	setAgentState(t, c, "work", a, "done", "", "finished the refactor")
	items := waitAttention(t, c, "a finished turn", hasKind(AttentionFinished, a))
	id := items[0]["id"].(string)

	for _, nonce := range []string{"", "0123456789abcdef0123456789abcdef"} {
		resp := c.call(t, fmt.Sprintf(`{"id":1,"verb":"dismiss-attention","params":{"id":%q,"human_nonce":%q}}`, id, nonce))
		if code := errCode(t, resp); code != ErrVerbNotHuman {
			t.Fatalf("dismiss with nonce %q answered %s, want %s", nonce, code, ErrVerbNotHuman)
		}
	}

	// A client attached to another session may still dismiss: the Inbox spans
	// every session.
	tui := attachTUI(t, sp, "other")
	res := result(t, c.call(t, fmt.Sprintf(`{"id":1,"verb":"dismiss-attention","params":{"id":%q,"human_nonce":%q}}`, id, tui.HumanNonce())))
	if res["dismissed"] != true || res["kind"] != AttentionFinished {
		t.Errorf("dismiss answered %v", res)
	}
	if items, _ := listAttention(t, c, ""); len(items) != 0 {
		t.Errorf("the dismissed item is still listed: %v", items)
	}
	// Dismissing a finished turn is having seen it.
	for _, w := range sess.GetState().Windows {
		if w.ID == a && sess.finishedUnread(&w) {
			t.Errorf("finished_unread is still true after the dismiss")
		}
	}

	resp := c.call(t, fmt.Sprintf(`{"id":1,"verb":"dismiss-attention","params":{"id":%q,"human_nonce":%q}}`, id, tui.HumanNonce()))
	if code := errCode(t, resp); code != ErrVerbInvalidParams {
		t.Errorf("a second dismiss answered %s", code)
	}
}
