package session

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
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

// TestInboxFollowsAgentStateInEverySession is the case the rail's alert could
// not see: a pane in a session nobody is attached to going to needs_input.
func TestInboxFollowsAgentStateInEverySession(t *testing.T) {
	d, sp := startTestDaemon(t)
	_, a, b := twoWindowSession(t, d, "fan-1")
	other := makeSessionWithWindow(t, d, "fan-2")
	c := dialVerb(t, sp)

	setAgentState(t, c, "fan-1", a, "needs_input", "approval", "approve Bash: rm -rf build")
	setAgentState(t, c, "fan-1", b, "errored", "", "rate limited")
	setAgentState(t, c, "fan-2", other.GetState().Windows[0].ID, "needs_input", "question", "which branch?")

	items := waitAttention(t, c, "three transitions", func(items []map[string]any) bool { return len(items) == 3 })
	var kinds []string
	for _, it := range items {
		kinds = append(kinds, it["kind"].(string))
	}
	if fmt.Sprint(kinds) != "[approval question errored]" {
		t.Errorf("Inbox order %v, want approval, question, errored", kinds)
	}
	if items[0]["summary"] != "approve Bash: rm -rf build" || items[0]["session"] != "fan-1" {
		t.Errorf("the approval item is %v", items[0])
	}

	only, res := listAttention(t, c, `{"session":"fan-2"}`)
	if len(only) != 1 || only[0]["session"] != "fan-2" {
		t.Errorf("session filter gave %v", only)
	}
	counts, _ := res["counts"].(map[string]any)
	if counts["approval"] != float64(1) || counts["finished"] != float64(0) {
		t.Errorf("counts %v", counts)
	}

	resp := c.call(t, `{"id":1,"verb":"list-attention","params":{"kinds":["urgent"]}}`)
	if code := errCode(t, resp); code != ErrVerbInvalidParams {
		t.Errorf("an unknown kind answered %s", code)
	}

	setAgentState(t, c, "fan-1", a, "working", "", "")
	waitAttention(t, c, "leaving needs_input", func(items []map[string]any) bool { return !hasKind("approval", a)(items) })
}

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

func TestInboxMailOpensAndClosesOnRead(t *testing.T) {
	d, sp := startTestDaemon(t)
	_, a, _ := twoWindowSession(t, d, "work")
	c := dialVerb(t, sp)

	sendJSON(t, c, 1, map[string]any{"session": "work", "to": AgentInboxHuman, "from": a, "subject": "ready to merge?", "text": "the suite is green"})
	items := waitAttention(t, c, "mail to the person", hasKind(AttentionMail, a))
	if items[0]["summary"] != "ready to merge?" || items[0]["thread"] == nil {
		t.Errorf("the mail item is %v", items[0])
	}

	// A peek is not a read.
	result(t, c.call(t, `{"id":1,"verb":"read-agent-messages","params":{"session":"work","to":"human","peek":true}}`))
	if items, _ := listAttention(t, c, ""); len(items) != 1 {
		t.Fatalf("a peek closed the mail item")
	}
	result(t, c.call(t, `{"id":1,"verb":"read-agent-messages","params":{"session":"work","to":"human"}}`))
	waitAttention(t, c, "reading the person's inbox", isEmpty)
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

// TestInboxDismissingMailMarksItRead keeps the rail's mail count and the Inbox
// in agreement.
func TestInboxDismissingMailMarksItRead(t *testing.T) {
	d, sp := startTestDaemon(t)
	_, a, _ := twoWindowSession(t, d, "work")
	c := dialVerb(t, sp)
	tui := attachTUI(t, sp, "work")

	sendJSON(t, c, 1, map[string]any{"session": "work", "to": AgentInboxHuman, "from": a, "text": "look at this"})
	items := waitAttention(t, c, "mail", hasKind(AttentionMail, a))
	result(t, c.call(t, fmt.Sprintf(`{"id":1,"verb":"dismiss-attention","params":{"id":%q,"human_nonce":%q}}`, items[0]["id"], tui.HumanNonce())))
	if n := d.agents.unreadCounts("work")[AgentInboxHuman]; n != 0 {
		t.Errorf("the person still has %d unread after dismissing the thread", n)
	}
}

// TestInboxFinishedClosesWhenAClientFocusesThePane is the per-client "seen"
// rule the daemon already applies to finished_unread.
func TestInboxFinishedClosesWhenAClientFocusesThePane(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess, a, b := twoWindowSession(t, d, "work")
	c := dialVerb(t, sp)

	st := sess.GetState()
	st.FocusedWindowID = b
	sess.UpdateState(st)

	setAgentState(t, c, "work", a, "working", "", "")
	setAgentState(t, c, "work", a, "done", "", "")
	waitAttention(t, c, "a finished turn", hasKind(AttentionFinished, a))

	st = sess.GetState()
	st.FocusedWindowID = a
	sess.UpdateState(st)
	waitAttention(t, c, "focusing the pane", isEmpty)
}

func TestInboxClosesWhenTheSessionEnds(t *testing.T) {
	d, sp := startTestDaemon(t)
	_, a, _ := twoWindowSession(t, d, "work")
	c := dialVerb(t, sp)
	setAgentState(t, c, "work", a, "errored", "", "")
	waitAttention(t, c, "errored", hasKind(AttentionErrored, a))
	if err := d.manager.DeleteSession("work"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	waitAttention(t, c, "the session ending", isEmpty)
}

// TestInboxSurvivesADaemonRestart restarts a real daemon over the same state
// directory: the finished turn nobody looked at is still waiting, and the
// approval, whose prompt died with its process, is not.
func TestInboxSurvivesADaemonRestart(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", testutil.RuntimeDir(t))
	t.Cleanup(useResurrectionDir(t.TempDir()))

	start := func() (*Daemon, *verbConn) {
		d := NewDaemon(&DaemonConfig{Version: "test"})
		if err := d.Start(); err != nil {
			t.Fatalf("daemon Start: %v", err)
		}
		sp, err := GetSocketPath()
		if err != nil {
			t.Fatalf("GetSocketPath: %v", err)
		}
		return d, dialVerb(t, sp)
	}

	d, c := start()
	_, a, b := twoWindowSession(t, d, "work")
	setAgentState(t, c, "work", a, "working", "", "")
	setAgentState(t, c, "work", a, "done", "", "all green")
	setAgentState(t, c, "work", b, "needs_input", "approval", "ok?")
	before := waitAttention(t, c, "two items", func(items []map[string]any) bool { return len(items) == 2 })
	var finishedID string
	for _, it := range before {
		if it["kind"] == AttentionFinished {
			finishedID = it["id"].(string)
		}
	}
	_ = c.conn.Close()
	d.Stop()

	d2, c2 := start()
	t.Cleanup(d2.Stop)
	items, _ := listAttention(t, c2, "")
	if len(items) != 1 || items[0]["kind"] != AttentionFinished || items[0]["window"] != a || items[0]["id"] != finishedID {
		t.Fatalf("after a restart the Inbox holds %v, want only the finished item %s", items, finishedID)
	}
}
