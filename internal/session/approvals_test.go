package session

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
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
	c := dialVerb(t, sp)
	params := map[string]any{"session": session, "window": window, "harness": "claude"}
	if len(options) > 0 {
		params["options"] = options
	}
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

// TestApprovalDisabledHoldsNothing is the default: no [agents.approvals], so
// the hook is answered at once and the harness asks as it always did.
func TestApprovalDisabledHoldsNothing(t *testing.T) {
	d, sp := startTestDaemon(t)
	_, a, _ := twoWindowSession(t, d, "work")
	c := dialVerb(t, sp)
	setAgentState(t, c, "work", a, "needs_input", "approval", "ok?")

	start := time.Now()
	res := result(t, c.call(t, `{"id":1,"verb":"request-approval","params":{"session":"work","window":"`+a+`","harness":"claude-code"}}`))
	if res["decision"] != "" || res["reason"] != approvalEndDisabled || res["request_id"] != "" {
		t.Fatalf("a disabled harness answered %v", res)
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("a disabled request took %s", time.Since(start))
	}
	items, _ := listAttention(t, c, "")
	if len(items) != 1 || items[0]["request_id"] != nil {
		t.Errorf("a disabled request changed the item: %v", items)
	}

	// Another harness than the one enabled is disabled too.
	enableApprovals(t, d, time.Second)
	res = result(t, c.call(t, `{"id":1,"verb":"request-approval","params":{"session":"work","window":"`+a+`","harness":"opencode"}}`))
	if res["reason"] != approvalEndDisabled {
		t.Errorf("opencode, not enabled, answered %v", res)
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
	res := result(t, c.call(t, `{"id":1,"verb":"request-approval","params":{"session":"work","window":"`+a+`","harness":"claude-code"}}`))
	if res["reason"] != approvalEndNotBlocked || res["decision"] != "" {
		t.Errorf("a pane not on needs_input answered %v", res)
	}
	// A question is not an approval.
	setAgentState(t, c, "work", a, "needs_input", "question", "which?")
	res = result(t, c.call(t, `{"id":1,"verb":"request-approval","params":{"session":"work","window":"`+a+`","harness":"claude-code"}}`))
	if res["reason"] != approvalEndNotBlocked {
		t.Errorf("a question answered %v", res)
	}

	// A process inside a pane may hold only its own pane's prompt.
	setAgentState(t, c, "work", b, "needs_input", "approval", "ok?")
	d.approvalPeer = func(*connState) (bool, string) { return true, a }
	resp := c.call(t, `{"id":1,"verb":"request-approval","params":{"session":"work","window":"`+b+`","harness":"claude-code"}}`)
	if code := errCode(t, resp); code != ErrVerbForbidden {
		t.Errorf("a request for another pane answered %s", code)
	}
	d.approvalPeer = func(*connState) (bool, string) { return true, "" }
	resp = c.call(t, `{"id":1,"verb":"request-approval","params":{"session":"work","window":"`+b+`","harness":"claude-code"}}`)
	if code := errCode(t, resp); code != ErrVerbForbidden {
		t.Errorf("a pane process that could not be placed answered %s", code)
	}
	d.approvalPeer = nil

	// Over a link, never.
	raw := json.RawMessage(`{"session":"work","window":"` + b + `","harness":"claude-code"}`)
	if _, verr := d.verbRequestApproval(&connState{viaLink: true, linkHuman: true}, raw); verr == nil || verr.Code != ErrVerbForbidden {
		t.Errorf("a request over a link answered %v", verr)
	}

	// An option that is not a decision.
	resp = c.call(t, `{"id":1,"verb":"request-approval","params":{"session":"work","window":"`+b+`","harness":"claude-code","options":["yes"]}}`)
	if code := errCode(t, resp); code != ErrVerbInvalidParams {
		t.Errorf("options [yes] answered %s", code)
	}
}

// TestApprovalOnlyOffersWhatTheHarnessCanDo keeps a reply to what the hook
// said the harness can take.
func TestApprovalOnlyOffersWhatTheHarnessCanDo(t *testing.T) {
	d, sp := startTestDaemon(t)
	enableApprovals(t, d, 30*time.Second)
	_, a, _ := twoWindowSession(t, d, "work")
	makeSessionWithWindow(t, d, "other")
	c := dialVerb(t, sp)
	tui := attachTUI(t, sp, "other")

	setAgentState(t, c, "work", a, "needs_input", "approval", "ok?")
	pending, _ := requestApproval(t, sp, "work", a)
	id := heldItem(t, c, a)["request_id"].(string)
	if code := errCode(t, reply(c, t, id, ApprovalAlways, tui.HumanNonce())); code != ErrVerbInvalidParams {
		t.Fatalf("always, which was not offered, answered %s", code)
	}
	// By pane rather than by request id.
	res := result(t, c.call(t, fmt.Sprintf(`{"id":1,"verb":"reply-approval","params":{"session":"work","window":%q,"decision":"deny","message":"not on main","human_nonce":%q}}`, a, tui.HumanNonce())))
	if res["decision"] != ApprovalDeny {
		t.Fatalf("a reply by pane answered %v", res)
	}
	got := awaitResult(t, pending)
	if got["decision"] != ApprovalDeny || got["message"] != "not on main" {
		t.Errorf("the hook got %v", got)
	}
}

// TestApprovalNotHeldForAPaneThePersonIsLookingAt: a held prompt shows nothing
// in the pane, so a pane an attached client has in front of it is not held.
func TestApprovalNotHeldForAPaneThePersonIsLookingAt(t *testing.T) {
	d, sp := startTestDaemon(t)
	enableApprovals(t, d, 30*time.Second)
	sess, a, _ := twoWindowSession(t, d, "work")
	st := sess.GetState()
	st.FocusedWindowID = a
	sess.UpdateState(st)
	c := dialVerb(t, sp)
	attachTUI(t, sp, "work")

	setAgentState(t, c, "work", a, "needs_input", "approval", "ok?")
	res := result(t, c.call(t, `{"id":1,"verb":"request-approval","params":{"session":"work","window":"`+a+`","harness":"claude-code"}}`))
	if res["reason"] != approvalEndViewed || res["decision"] != "" {
		t.Errorf("a focused pane answered %v", res)
	}
}

func TestApprovalPolicyFromConfig(t *testing.T) {
	p := ApprovalPolicyFromConfig(config.ApprovalsConfig{Enabled: []string{"claude", " OpenCode ", ""}, HoldSeconds: 1000})
	if !p.Enabled["claude-code"] || !p.Enabled["opencode"] || len(p.Enabled) != 2 {
		t.Errorf("enabled = %v", p.Enabled)
	}
	if p.holdFor() != maxApprovalHold {
		t.Errorf("hold 1000s became %s, want the cap", p.holdFor())
	}
	if (ApprovalPolicy{}).holdFor() != DefaultApprovalHold {
		t.Errorf("no hold became %s", (ApprovalPolicy{}).holdFor())
	}
	if (ApprovalPolicy{Hold: time.Second}).holdFor() != minApprovalHold {
		t.Errorf("a one second hold is not raised to the floor")
	}
	if len(ApprovalPolicyFromConfig(config.ApprovalsConfig{}).Enabled) != 0 {
		t.Error("an empty table enabled something")
	}
}
