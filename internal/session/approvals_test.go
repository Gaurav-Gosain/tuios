package session

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// enableApprovals turns approvals on for claude-code with a hold of hold.
func enableApprovals(t *testing.T, d *Daemon, hold time.Duration) {
	t.Helper()
	oldMin := minApprovalHold
	minApprovalHold = time.Millisecond
	t.Cleanup(func() { minApprovalHold = oldMin })
	d.SetApprovalPolicy(ApprovalPolicy{Enabled: map[string]bool{"claude-code": true}, Hold: hold})
}

// requestApproval starts a request-approval call on its own connection and
// returns a channel with its response.
func requestApproval(t *testing.T, sp, session, window string, options ...string) (<-chan map[string]any, *verbConn) {
	t.Helper()
	params := map[string]any{"session": session, "window": window, "harness": "claude", "summary": testHeldLine}
	if len(options) > 0 {
		params["options"] = options
	}
	if slices.Contains(options, ApprovalAlways) {
		params["always_scope"] = []string{testHeldScope}
	}
	return requestApprovalWith(t, sp, params)
}

// testHeldLine and testHeldScope are the line and the always rule the test
// hook holds a prompt with.
const (
	testHeldLine  = "approve Bash: go test ./..."
	testHeldScope = "Bash(go test:*) in .claude/settings.local.json"
)

// requestApprovalWith is requestApproval with every parameter given.
func requestApprovalWith(t *testing.T, sp string, params map[string]any) (<-chan map[string]any, *verbConn) {
	t.Helper()
	c := dialVerb(t, sp)
	raw, _ := json.Marshal(params)
	c.send(t, fmt.Sprintf(`{"id":1,"verb":"request-approval","params":%s}`, raw))
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
	return out, c
}

// awaitResult waits for a request-approval answer.
func awaitResult(t *testing.T, ch <-chan map[string]any) map[string]any {
	t.Helper()
	select {
	case resp := <-ch:
		return result(t, resp)
	case <-time.After(10 * time.Second * testDeadlineScale):
		t.Fatal("request-approval never answered")
		return nil
	}
}

// heldItem waits for the pane's approval item to carry a request id.
func heldItem(t *testing.T, c *verbConn, window string) map[string]any {
	t.Helper()
	items := waitAttention(t, c, "a held approval", func(items []map[string]any) bool {
		for _, it := range items {
			if it["window"] == window && it["request_id"] != nil {
				return true
			}
		}
		return false
	})
	for _, it := range items {
		if it["window"] == window {
			return it
		}
	}
	return nil
}

func reply(c *verbConn, t *testing.T, requestID, decision, nonce string) map[string]any {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"request_id": requestID, "decision": decision, "human_nonce": nonce})
	return c.call(t, fmt.Sprintf(`{"id":1,"verb":"reply-approval","params":%s}`, raw))
}

// TestApprovalHeldAndAnsweredFromTheInbox is the round trip: the hook's
// request holds, the Inbox item carries the request, a caller with no attach
// nonce cannot answer it, the person can, the hook gets the decision, the item
// closes as answered and the pane moves on. A second reply gets the first
// one's answer back.
func TestApprovalHeldAndAnsweredFromTheInbox(t *testing.T) {
	d, sp := startTestDaemon(t)
	enableApprovals(t, d, 30*time.Second)
	sess, a, _ := twoWindowSession(t, d, "work")
	makeSessionWithWindow(t, d, "other")
	c := dialVerb(t, sp)
	tui := attachTUI(t, sp, "other")

	setAgentState(t, c, "work", a, "needs_input", "approval", "approve Bash: go test ./...")
	pending, _ := requestApproval(t, sp, "work", a, "once", "always", "deny")
	it := heldItem(t, c, a)
	id, _ := it["request_id"].(string)
	if opts := fmt.Sprint(it["options"]); opts != "[once always deny]" || it["expires"] == nil {
		t.Fatalf("the held item is %v", it)
	}

	for _, nonce := range []string{"", "0123456789abcdef0123456789abcdef"} {
		if code := errCode(t, reply(c, t, id, ApprovalOnce, nonce)); code != ErrVerbNotHuman {
			t.Fatalf("a reply with nonce %q answered %s, want %s", nonce, code, ErrVerbNotHuman)
		}
	}
	select {
	case resp := <-pending:
		t.Fatalf("the hold ended on a refused reply: %v", resp)
	default:
	}

	res := result(t, reply(c, t, id, ApprovalAlways, tui.HumanNonce()))
	if res["applied"] != true || res["decision"] != ApprovalAlways || res["window"] != a {
		t.Fatalf("the reply answered %v", res)
	}
	got := awaitResult(t, pending)
	if got["decision"] != ApprovalAlways || got["reason"] != approvalEndAnswered || got["request_id"] != id || got["answered_by"] == nil {
		t.Fatalf("the hook got %v", got)
	}
	waitAttention(t, c, "the answered approval closing", isEmpty)
	if w, _ := findWindowState(sess.GetState(), a); w.AgentState != AgentStateWorking {
		t.Errorf("the pane is %s after the answer, want working", w.AgentState.Name())
	}

	again := result(t, reply(c, t, id, ApprovalDeny, tui.HumanNonce()))
	if again["applied"] != false || again["decision"] != ApprovalAlways {
		t.Errorf("a second reply answered %v, want the first decision with applied false", again)
	}
}

// TestApprovalEndsWithNoDecision covers every way a hold ends without the
// person answering it. Each must give the hook no decision, so the harness
// asks in its pane, and each must leave the item without a request.
func TestApprovalEndsWithNoDecision(t *testing.T) {
	cases := []struct {
		name   string
		hold   time.Duration
		end    func(t *testing.T, sp string, c *verbConn, sess *Session, window, requestID string, tui *TUIClient, conn *verbConn)
		reason string
		// itemGone says the item closes, rather than staying open without a
		// request.
		itemGone bool
	}{
		{
			name: "timeout", hold: 150 * time.Millisecond, reason: approvalEndTimeout,
			end: func(*testing.T, string, *verbConn, *Session, string, string, *TUIClient, *verbConn) {},
		},
		{
			name: "the pane moves on", reason: approvalEndResolved, itemGone: true,
			end: func(t *testing.T, _ string, c *verbConn, _ *Session, w, _ string, _ *TUIClient, _ *verbConn) {
				setAgentState(t, c, "work", w, "working", "", "")
			},
		},
		{
			name: "the block becomes a question", reason: approvalEndResolved,
			end: func(t *testing.T, _ string, c *verbConn, _ *Session, w, _ string, _ *TUIClient, _ *verbConn) {
				setAgentState(t, c, "work", w, "needs_input", "question", "which branch?")
			},
		},
		{
			name: "dismissed", reason: AttentionClosedDismissed, itemGone: true,
			end: func(t *testing.T, _ string, c *verbConn, _ *Session, w, _ string, tui *TUIClient, _ *verbConn) {
				items, _ := listAttention(t, c, "")
				result(t, c.call(t, fmt.Sprintf(`{"id":1,"verb":"dismiss-attention","params":{"id":%q,"human_nonce":%q}}`, items[0]["id"], tui.HumanNonce())))
			},
		},
		{
			name: "handed back", reason: approvalEndHandedBack,
			end: func(t *testing.T, _ string, c *verbConn, _ *Session, _, id string, tui *TUIClient, _ *verbConn) {
				res := result(t, reply(c, t, id, ApprovalAsk, tui.HumanNonce()))
				if res["decision"] != ApprovalAsk || res["applied"] != true {
					t.Errorf("ask answered %v", res)
				}
			},
		},
		{
			name: "the person turns to the pane", reason: approvalEndViewed,
			end: func(t *testing.T, _ string, _ *verbConn, sess *Session, w, _ string, _ *TUIClient, _ *verbConn) {
				st := sess.GetState()
				st.FocusedWindowID = w
				sess.UpdateState(st)
			},
		},
		{
			name: "the hook goes away", reason: approvalEndCallerGone,
			end: func(_ *testing.T, _ string, _ *verbConn, _ *Session, _, _ string, _ *TUIClient, conn *verbConn) {
				_ = conn.conn.Close()
			},
		},
		{
			name: "a newer request for the pane", reason: approvalEndSuperseded,
			end: func(t *testing.T, sp string, _ *verbConn, _ *Session, w, _ string, _ *TUIClient, _ *verbConn) {
				// The newer request's own answer is not waited on here.
				requestApproval(t, sp, "work", w)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, sp := startTestDaemon(t)
			hold := tc.hold
			if hold == 0 {
				hold = 30 * time.Second
			}
			enableApprovals(t, d, hold)
			sess, a, b := twoWindowSession(t, d, "work")
			st := sess.GetState()
			st.FocusedWindowID = b
			sess.UpdateState(st)
			makeSessionWithWindow(t, d, "other")
			c := dialVerb(t, sp)
			tui := attachTUI(t, sp, "other")

			setAgentState(t, c, "work", a, "needs_input", "approval", "approve Edit: main.go")
			pending, conn := requestApproval(t, sp, "work", a)
			id := heldItem(t, c, a)["request_id"].(string)
			tc.end(t, sp, c, sess, a, id, tui, conn)

			if tc.reason == approvalEndCallerGone {
				// The response went nowhere; the hold is seen ending from the
				// item.
				waitAttention(t, c, "the item losing its request", func(items []map[string]any) bool {
					return len(items) == 1 && items[0]["request_id"] == nil
				})
				if _, held := d.attention.holdOn("work", a); held {
					t.Fatal("the hold outlived its caller")
				}
				return
			}
			got := awaitResult(t, pending)
			if got["decision"] != "" || got["reason"] != tc.reason {
				t.Fatalf("the hook got %v, want no decision and reason %s", got, tc.reason)
			}
			if tc.itemGone {
				waitAttention(t, c, "the item closing", func(items []map[string]any) bool { return !hasKind(AttentionApproval, a)(items) })
			} else {
				// The item stays, without this request. A newer request may
				// have put its own there.
				waitAttention(t, c, "the item losing its request", func(items []map[string]any) bool {
					for _, it := range items {
						if it["window"] == a && it["request_id"] == id {
							return false
						}
					}
					return hasKind(AttentionApproval, a)(items) || hasKind(AttentionQuestion, a)(items)
				})
			}
			// A reply after the hold ended decides nothing.
			resp := reply(c, t, id, ApprovalOnce, tui.HumanNonce())
			if code := errCode(t, resp); code != ErrVerbInvalidParams {
				t.Errorf("a reply after the hold ended answered %s", code)
			}
		})
	}
}

// TestApprovalRefusals covers who may hold and what can be held.
func TestApprovalRefusals(t *testing.T) {
	d, sp := startTestDaemon(t)
	enableApprovals(t, d, 30*time.Second)
	_, a, b := twoWindowSession(t, d, "work")
	c := dialVerb(t, sp)

	// Not blocked: nothing to hold.
	res := result(t, c.call(t, `{"id":1,"verb":"request-approval","params":{"session":"work","window":"`+a+`","harness":"claude-code","summary":"ok?"}}`))
	if res["reason"] != approvalEndNotBlocked || res["decision"] != "" {
		t.Errorf("a pane not on needs_input answered %v", res)
	}
	// A question is not an approval.
	setAgentState(t, c, "work", a, "needs_input", "question", "which?")
	res = result(t, c.call(t, `{"id":1,"verb":"request-approval","params":{"session":"work","window":"`+a+`","harness":"claude-code","summary":"ok?"}}`))
	if res["reason"] != approvalEndNotBlocked {
		t.Errorf("a question answered %v", res)
	}

	// A process inside a pane may hold only its own pane's prompt.
	setAgentState(t, c, "work", b, "needs_input", "approval", "ok?")
	d.setApprovalPeer(func(*connState) (bool, string) { return true, a })
	resp := c.call(t, `{"id":1,"verb":"request-approval","params":{"session":"work","window":"`+b+`","harness":"claude-code","summary":"ok?"}}`)
	if code := errCode(t, resp); code != ErrVerbForbidden {
		t.Errorf("a request for another pane answered %s", code)
	}
	d.setApprovalPeer(func(*connState) (bool, string) { return true, "" })
	resp = c.call(t, `{"id":1,"verb":"request-approval","params":{"session":"work","window":"`+b+`","harness":"claude-code","summary":"ok?"}}`)
	if code := errCode(t, resp); code != ErrVerbForbidden {
		t.Errorf("a pane process that could not be placed answered %s", code)
	}
	d.setApprovalPeer(nil)

	// Over a link, never.
	raw := json.RawMessage(`{"session":"work","window":"` + b + `","harness":"claude-code"}`)
	if _, verr := d.verbRequestApproval(&connState{viaLink: true, linkHuman: true}, raw); verr == nil || verr.Code != ErrVerbForbidden {
		t.Errorf("a request over a link answered %v", verr)
	}

	// An option that is not a decision.
	resp = c.call(t, `{"id":1,"verb":"request-approval","params":{"session":"work","window":"`+b+`","harness":"claude-code","summary":"ok?","options":["yes"]}}`)
	if code := errCode(t, resp); code != ErrVerbInvalidParams {
		t.Errorf("options [yes] answered %s", code)
	}
}

// replyShown is reply with the summary the decision was made from.
func replyShown(c *verbConn, t *testing.T, requestID, decision, nonce, shown string) map[string]any {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"request_id": requestID, "decision": decision, "human_nonce": nonce, "summary": shown})
	return c.call(t, fmt.Sprintf(`{"id":1,"verb":"reply-approval","params":%s}`, raw))
}

// TestApprovalAnswersOnlyTheLineThePersonRead: while a hold runs the item
// shows the held call's line, even when a second call on the same pane is
// reported before its hook opens its own hold, so the text and the request id
// an answer carries always belong to one call. A reply made from another line
// is refused and the hold runs on.
func TestApprovalAnswersOnlyTheLineThePersonRead(t *testing.T) {
	d, sp := startTestDaemon(t)
	enableApprovals(t, d, 30*time.Second)
	_, a, _ := twoWindowSession(t, d, "work")
	makeSessionWithWindow(t, d, "other")
	c := dialVerb(t, sp)
	tui := attachTUI(t, sp, "other")

	setAgentState(t, c, "work", a, "needs_input", "approval", testHeldLine)
	pending, _ := requestApproval(t, sp, "work", a, "once", "always", "deny")
	it := heldItem(t, c, a)
	id := it["request_id"].(string)
	if it["summary"] != testHeldLine || fmt.Sprint(it["always_scope"]) != "["+testHeldScope+"]" {
		t.Fatalf("the held item is %v", it)
	}

	// Hook 2 reports its call; its hold is not open yet.
	second := "approve Bash: rm -rf build"
	setAgentState(t, c, "work", a, "needs_input", "approval", second)
	items, _ := listAttention(t, c, "")
	if len(items) != 1 || items[0]["summary"] != testHeldLine || items[0]["request_id"] != id {
		t.Fatalf("a second report while held left %v, want the held line on the held request", items)
	}

	// A reply made from the second call's line does not answer the first.
	res := result(t, replyShown(c, t, id, ApprovalOnce, tui.HumanNonce(), second))
	if res["applied"] != false || res["reason"] != approvalEndChanged || res["decision"] != "" || res["summary"] != testHeldLine {
		t.Fatalf("a reply from another line answered %v", res)
	}
	select {
	case got := <-pending:
		t.Fatalf("the hold ended on a refused reply: %v", got)
	default:
	}

	// Handing it back ends the hold, and the item then shows the newest
	// report, which is what the pane is on.
	result(t, replyShown(c, t, id, ApprovalAsk, tui.HumanNonce(), ""))
	if got := awaitResult(t, pending); got["decision"] != "" || got["reason"] != approvalEndHandedBack {
		t.Fatalf("the hook got %v", got)
	}
	items = waitAttention(t, c, "the item back on the newest line", func(items []map[string]any) bool {
		return len(items) == 1 && items[0]["request_id"] == nil && items[0]["summary"] == second
	})
	if items[0]["always_scope"] != nil {
		t.Errorf("the ended hold left its scope: %v", items[0])
	}

	// Hook 2's hold, answered from its own line, applies.
	pending, _ = requestApprovalWith(t, sp, map[string]any{"session": "work", "window": a, "harness": "claude", "summary": second})
	id2 := heldItem(t, c, a)["request_id"].(string)
	res = result(t, replyShown(c, t, id2, ApprovalOnce, tui.HumanNonce(), second))
	if res["applied"] != true || res["decision"] != ApprovalOnce {
		t.Fatalf("a reply from the held line answered %v", res)
	}
	if got := awaitResult(t, pending); got["decision"] != ApprovalOnce {
		t.Fatalf("the hook got %v", got)
	}
}

// TestApprovalNotHeldUnlessShownWhole: the daemon refuses to hold a prompt
// whose line the Inbox would have to cut, mask or rewrite, whatever the hook
// checked, and drops always unless the rules it adds can be shown.
func TestApprovalNotHeldUnlessShownWhole(t *testing.T) {
	d, sp := startTestDaemon(t)
	enableApprovals(t, d, 30*time.Second)
	_, a, _ := twoWindowSession(t, d, "work")
	c := dialVerb(t, sp)
	setAgentState(t, c, "work", a, "needs_input", "approval", "ok?")

	for name, line := range map[string]string{
		"too long":          "approve Bash: echo " + strings.Repeat("a", attentionMaxSummary),
		"a newline":         "approve Bash: ls\nrm -rf ~",
		"doubled spaces":    "approve Bash: ls  -la",
		"a bidi override":   "approve Bash: echo ‮ftp",
		"a masked secret":   "approve Bash: API_TOKEN=abcdef123456 make",
		"a control char":    "approve Bash: ls\x1b[2J",
		"a zero width char": "approve Bash: rm​ -rf",
	} {
		t.Run(name, func(t *testing.T) {
			raw, _ := json.Marshal(map[string]any{"session": "work", "window": a, "harness": "claude-code", "summary": line})
			res := result(t, c.call(t, fmt.Sprintf(`{"id":1,"verb":"request-approval","params":%s}`, raw)))
			if res["reason"] != approvalEndNotShown || res["decision"] != "" || res["request_id"] != "" {
				t.Fatalf("a line with %s answered %v", name, res)
			}
			if items, _ := listAttention(t, c, ""); len(items) != 1 || items[0]["request_id"] != nil {
				t.Fatalf("a line with %s left %v", name, items)
			}
		})
	}

	// No summary at all is a bad call.
	if code := errCode(t, c.call(t, `{"id":1,"verb":"request-approval","params":{"session":"work","window":"`+a+`","harness":"claude-code"}}`)); code != ErrVerbInvalidParams {
		t.Errorf("no summary answered %s", code)
	}

	// Always is offered only with a scope it can show.
	for name, scope := range map[string][]string{
		"no scope":         nil,
		"too many rules":   {"a", "b", "c", "d", "e"},
		"a rule with a CR": {"Bash(ls:*)\r in .claude/settings.json"},
	} {
		t.Run(name, func(t *testing.T) {
			params := map[string]any{"session": "work", "window": a, "harness": "claude", "summary": testHeldLine, "options": []string{"once", "always", "deny"}}
			if scope != nil {
				params["always_scope"] = scope
			}
			pending, conn := requestApprovalWith(t, sp, params)
			it := heldItem(t, c, a)
			if opts := fmt.Sprint(it["options"]); opts != "[once deny]" || it["always_scope"] != nil {
				t.Errorf("with %s the held item is %v", name, it)
			}
			_ = conn.conn.Close()
			waitAttention(t, c, "the hold ending", func(items []map[string]any) bool {
				return len(items) == 1 && items[0]["request_id"] == nil
			})
			select {
			case <-pending:
			case <-time.After(5 * time.Second):
			}
		})
	}
}
