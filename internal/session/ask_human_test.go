package session

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// askAsync starts an ask-human call on its own connection and returns a
// channel with its response.
func askAsync(t *testing.T, sp string, params map[string]any) <-chan map[string]any {
	t.Helper()
	c := dialVerb(t, sp)
	raw, _ := json.Marshal(params)
	c.send(t, fmt.Sprintf(`{"id":1,"verb":"ask-human","params":%s}`, raw))
	out := make(chan map[string]any, 1)
	go func() {
		_ = c.conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		line, err := c.r.ReadBytes('\n')
		if err != nil {
			out <- map[string]any{"read_error": err.Error()}
			return
		}
		var resp map[string]any
		_ = json.Unmarshal(line, &resp)
		out <- resp
	}()
	return out
}

// awaitAskItem waits for the ask item to reach the Inbox and returns it.
func awaitAskItem(t *testing.T, c *verbConn) map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		res := result(t, c.call(t, `{"id":1,"verb":"list-attention","params":{"kinds":["ask"]}}`))
		if items, _ := res["items"].([]any); len(items) > 0 {
			return items[0].(map[string]any)
		}
		if time.Now().After(deadline) {
			t.Fatal("no ask item reached the Inbox")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// answerAsk calls answer-ask with the given nonce.
func answerAskCall(t *testing.T, c *verbConn, requestID, answer, nonce string) map[string]any {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"request_id": requestID, "answer": answer, "human_nonce": nonce})
	return c.call(t, fmt.Sprintf(`{"id":1,"verb":"answer-ask","params":%s}`, raw))
}

// TestAskHumanReturnsThePersonsAnswer is the whole round trip: a question with
// three answers goes in the Inbox, an agent cannot answer it, a client attached
// right now can, and the waiting call gets the answer marked verified.
func TestAskHumanReturnsThePersonsAnswer(t *testing.T) {
	d, sp := startTestDaemon(t)
	_, a, _ := twoWindowSession(t, d, "work")
	c := dialVerb(t, sp)

	done := askAsync(t, sp, map[string]any{"session": "work", "window": a, "question": "Deploy to staging?", "options": []string{"yes", "no", "later"}, "timeout": 20000})
	item := awaitAskItem(t, c)
	if item["summary"] != "Deploy to staging?" || item["window"] != a {
		t.Fatalf("ask item = %v, want the question about the asking pane", item)
	}
	opts, _ := item["options"].([]any)
	if len(opts) != 3 || opts[1] != "no" {
		t.Fatalf("ask item options = %v, want yes, no, later", opts)
	}
	id, _ := item["request_id"].(string)

	// No nonce: an agent cannot answer the person's question.
	if code := errCode(t, answerAskCall(t, c, id, "yes", "")); code != ErrVerbNotHuman {
		t.Fatalf("answer without a nonce: code %q, want %q", code, ErrVerbNotHuman)
	}
	tui := attachTUI(t, sp, "work")
	nonce := tui.HumanNonce()
	if code := errCode(t, answerAskCall(t, c, id, "maybe", nonce)); code != ErrVerbInvalidParams {
		t.Fatalf("an answer that is not an option: code %q, want %q", code, ErrVerbInvalidParams)
	}
	select {
	case resp := <-done:
		t.Fatalf("the ask returned before anyone answered: %v", resp)
	default:
	}

	res := result(t, answerAskCall(t, c, id, "no", nonce))
	if res["applied"] != true || res["answer"] != "no" {
		t.Fatalf("answer-ask = %v, want applied no", res)
	}
	select {
	case resp := <-done:
		got := result(t, resp)
		if got["status"] != "answered" || got["answer"] != "no" || got["answer_index"] != float64(2) || got["verified_human"] != true {
			t.Fatalf("ask-human = %v, want answered no (2), verified", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the ask did not return after the answer")
	}
	// The first answer stands.
	res = result(t, answerAskCall(t, c, id, "yes", nonce))
	if res["applied"] != false || res["answer"] != "no" {
		t.Fatalf("a second answer = %v, want not applied, no standing", res)
	}
}

// TestAskHumanLateAnswerIsMailedToThePane covers the question nobody was
// there for: the wait runs out, the call says pending, and the answer that
// comes later lands in the asking pane's inbox from human, verified, where
// wait-for agent-message finds it. Coming back with the request id reads it
// too.
func TestAskHumanLateAnswerIsMailedToThePane(t *testing.T) {
	d, sp := startTestDaemon(t)
	_, a, _ := twoWindowSession(t, d, "work")
	c := dialVerb(t, sp)

	res := result(t, c.call(t, `{"id":1,"verb":"ask-human","params":{"session":"work","window":"`+a+`","question":"Merge now?","options":["merge","wait"],"timeout":150}}`))
	if res["status"] != AskPending {
		t.Fatalf("ask-human after its wait = %v, want pending", res)
	}
	id, _ := res["request_id"].(string)
	item := awaitAskItem(t, c)
	if item["request_id"] != id {
		t.Fatalf("the question left the Inbox when the wait ended: %v", item)
	}

	tui := attachTUI(t, sp, "work")
	result(t, answerAskCall(t, c, id, "merge", tui.HumanNonce()))

	msgs := result(t, c.call(t, `{"id":1,"verb":"read-agent-messages","params":{"session":"work","to":"`+a+`"}}`))
	list, _ := msgs["messages"].([]any)
	if len(list) != 1 {
		t.Fatalf("inbox of the asking pane = %v, want the one answer", list)
	}
	m := list[0].(map[string]any)
	if m["from"] != AgentInboxHuman || m["text"] != "merge" || m["verified_human"] != true {
		t.Fatalf("mailed answer = %v, want merge from human, verified", m)
	}

	again := result(t, c.call(t, `{"id":1,"verb":"ask-human","params":{"request_id":"`+id+`"}}`))
	if again["status"] != "answered" || again["answer"] != "merge" {
		t.Fatalf("coming back for the answer = %v, want answered merge", again)
	}
}

// TestAskHumanFromAPaneAsksOnlyAsThatPane holds a pane to its own questions:
// it cannot have an answer mailed into another agent's inbox, and it cannot
// come back for another pane's question.
func TestAskHumanFromAPaneAsksOnlyAsThatPane(t *testing.T) {
	d, sp := startTestDaemon(t)
	_, a, b := twoWindowSession(t, d, "work")
	d.approvalPeer = func(*connState) (bool, string) { return true, a }
	c := dialVerb(t, sp)

	resp := c.call(t, `{"id":1,"verb":"ask-human","params":{"session":"work","window":"`+b+`","question":"Ship it?","options":["yes"],"wait":false}}`)
	if code := errCode(t, resp); code != ErrVerbForbidden {
		t.Fatalf("asking as another pane: code %q, want %q", code, ErrVerbForbidden)
	}
	res := result(t, c.call(t, `{"id":1,"verb":"ask-human","params":{"session":"work","question":"Ship it?","options":["yes"],"wait":false}}`))
	if item := awaitAskItem(t, c); item["window"] != a {
		t.Fatalf("an ask from a pane with no window named = %v, want it asked as the pane", item)
	}

	d.approvalPeer = func(*connState) (bool, string) { return true, b }
	resp = c.call(t, `{"id":1,"verb":"ask-human","params":{"request_id":"`+res["request_id"].(string)+`","wait":false}}`)
	if code := errCode(t, resp); code != ErrVerbForbidden {
		t.Fatalf("another pane coming back for the question: code %q, want %q", code, ErrVerbForbidden)
	}
}

// TestAskHumanEndsWithItsPane checks the ways a question ends with no answer:
// a newer question from the same pane supersedes it, and the pane closing
// ends it, and the waiting call is told which.
func TestAskHumanEndsWithItsPane(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess, a, _ := twoWindowSession(t, d, "work")
	c := dialVerb(t, sp)

	first := askAsync(t, sp, map[string]any{"session": "work", "window": a, "question": "One?", "options": []string{"yes"}})
	awaitAskItem(t, c)
	second := askAsync(t, sp, map[string]any{"session": "work", "window": a, "question": "Two?", "options": []string{"yes"}})
	select {
	case resp := <-first:
		if got := result(t, resp); got["status"] != AttentionClosedSuperseded {
			t.Fatalf("the first question = %v, want superseded", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the first question did not end when the pane asked another")
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		item := awaitAskItem(t, c)
		if item["summary"] == "Two?" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the second question never replaced the first: %v", item)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := sess.CloseDaemonWindow(a); err != nil {
		t.Fatalf("CloseDaemonWindow: %v", err)
	}
	select {
	case resp := <-second:
		if got := result(t, resp); got["status"] != AttentionClosedWindow {
			t.Fatalf("the second question = %v, want window_closed", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the question did not end when its pane closed")
	}
}

// TestAskHumanRefusesWhatTheInboxCannotShow keeps the question and the
// answers to what the Inbox draws as written, so the person answers what the
// asker asked.
func TestAskHumanRefusesWhatTheInboxCannotShow(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "work")
	c := dialVerb(t, sp)
	cases := map[string]map[string]any{
		"two lines":       {"question": "Deploy?\nreally", "options": []string{"yes"}},
		"no options":      {"question": "Deploy?"},
		"ten options":     {"question": "Pick", "options": []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10"}},
		"same option":     {"question": "Pick", "options": []string{"a", "a"}},
		"escape in label": {"question": "Pick", "options": []string{"a\x1b[31m"}},
	}
	for name, params := range cases {
		params["session"] = "work"
		params["wait"] = false
		raw, _ := json.Marshal(params)
		resp := c.call(t, fmt.Sprintf(`{"id":1,"verb":"ask-human","params":%s}`, raw))
		if code := errCode(t, resp); code != ErrVerbInvalidParams {
			t.Errorf("%s: code %q, want %q", name, code, ErrVerbInvalidParams)
		}
	}
}
