package session

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/integration"
	"github.com/Gaurav-Gosain/tuios/internal/risk"
)

// enableRiskyApprovals turns approvals on for claude-code with the shipped
// risk rules and plans held, the way a config naming claude-code reads.
func enableRiskyApprovals(t *testing.T, d *Daemon, hold time.Duration, mut func(*ApprovalPolicy)) {
	t.Helper()
	oldMin := minApprovalHold
	minApprovalHold = time.Millisecond
	t.Cleanup(func() { minApprovalHold = oldMin })
	p := ApprovalPolicyFromConfig(config.ApprovalsConfig{Enabled: []string{"claude-code"}})
	p.Hold = hold
	if mut != nil {
		mut(&p)
	}
	d.SetApprovalPolicy(p)
}

const riskyLine = "approve Bash: rm -rf build/"

// replyWith answers with extra params beside the usual ones.
func replyWith(c *verbConn, t *testing.T, requestID, decision, nonce string, extra map[string]any) map[string]any {
	t.Helper()
	params := map[string]any{"request_id": requestID, "decision": decision, "human_nonce": nonce}
	for k, v := range extra {
		params[k] = v
	}
	return callP(c, t, "reply-approval", params)
}

func itemRisk(it map[string]any) []string {
	var out []string
	raw, _ := it["risk"].([]any)
	for _, r := range raw {
		out = append(out, fmt.Sprint(r))
	}
	return out
}

// TestRiskyHeldApprovalNeedsTheAcknowledgement: a held call a risk rule matched
// is marked on its item, an allow without risk_ack naming exactly those rules
// answers nothing and leaves the hold running, a deny needs no acknowledgement,
// and an allow with it goes through.
//
// Negative control: with the risk check removed from answer, the first allow
// without risk_ack is applied and the hook gets once.
func TestRiskyHeldApprovalNeedsTheAcknowledgement(t *testing.T) {
	d, sp := startTestDaemon(t)
	enableRiskyApprovals(t, d, 30*time.Second, nil)
	_, a, b := twoWindowSession(t, d, "work")
	makeSessionWithWindow(t, d, "other")
	c := dialVerb(t, sp)
	tui := attachTUI(t, sp, "other")

	setAgentState(t, c, "work", a, "needs_input", "approval", riskyLine)
	pending, _ := requestApprovalWith(t, sp, map[string]any{
		"session": "work", "window": a, "harness": "claude", "summary": riskyLine,
		"options": []string{"once", "deny"}, "tool": "Bash", "target": "rm -rf build/", "deny_message": true,
	})
	it := heldItem(t, c, a)
	id := it["request_id"].(string)
	if got := itemRisk(it); !slices.Equal(got, []string{risk.RuleRecursiveDelete}) || it["deny_message"] != true {
		t.Fatalf("the held item is %v, want it marked risky with deny_message", it)
	}

	for _, ack := range [][]string{nil, {"sudo"}, {risk.RuleRecursiveDelete, "sudo"}} {
		resp := replyWith(c, t, id, ApprovalOnce, tui.HumanNonce(), map[string]any{"risk_ack": ack})
		mustRefuse(t, resp, ErrVerbRiskUnacknowledged, fmt.Sprintf("an allow with risk_ack %v", ack))
	}
	select {
	case resp := <-pending:
		t.Fatalf("ASSERTION: the hold ended on an unacknowledged allow: %v", resp)
	default:
	}
	res := result(t, replyWith(c, t, id, ApprovalOnce, tui.HumanNonce(), map[string]any{"risk_ack": []string{risk.RuleRecursiveDelete}, "summary": riskyLine}))
	if res["applied"] != true {
		t.Fatalf("the acknowledged allow answered %v", res)
	}
	if got := awaitResult(t, pending); got["decision"] != ApprovalOnce {
		t.Fatalf("the hook got %v", got)
	}

	// A deny is one press: no acknowledgement, and its reason reaches the
	// hook cleaned.
	setAgentState(t, c, "work", b, "needs_input", "approval", riskyLine)
	pending, _ = requestApprovalWith(t, sp, map[string]any{
		"session": "work", "window": b, "harness": "claude", "summary": riskyLine, "tool": "Bash", "target": "rm -rf build/", "deny_message": true,
	})
	id = heldItem(t, c, b)["request_id"].(string)
	res = result(t, replyWith(c, t, id, ApprovalDeny, tui.HumanNonce(), map[string]any{"message": "keep build/\x1b[2J, it has the cache"}))
	if res["applied"] != true {
		t.Fatalf("the deny answered %v", res)
	}
	got := awaitResult(t, pending)
	if got["decision"] != ApprovalDeny || got["message"] != "keep build/[2J, it has the cache" {
		t.Fatalf("the hook got %v", got)
	}
}

// TestRiskFromTheLineWhenTheHookNamesNoCall: a request without tool and
// target is matched on its line, and a call that matches nothing needs no
// acknowledgement, which is how an older client answers it.
func TestRiskFromTheLineWhenTheHookNamesNoCall(t *testing.T) {
	d, sp := startTestDaemon(t)
	enableRiskyApprovals(t, d, 30*time.Second, nil)
	_, a, b := twoWindowSession(t, d, "work")
	makeSessionWithWindow(t, d, "other")
	c := dialVerb(t, sp)
	tui := attachTUI(t, sp, "other")

	line := "approve Bash: git push --force origin main"
	setAgentState(t, c, "work", a, "needs_input", "approval", line)
	pending, _ := requestApprovalWith(t, sp, map[string]any{"session": "work", "window": a, "harness": "claude", "summary": line})
	it := heldItem(t, c, a)
	if got := itemRisk(it); !slices.Equal(got, []string{risk.RuleForcePush}) {
		t.Fatalf("risk %v, want force push from the line", got)
	}
	result(t, reply(c, t, it["request_id"].(string), ApprovalDeny, tui.HumanNonce()))
	awaitResult(t, pending)

	setAgentState(t, c, "work", b, "needs_input", "approval", testHeldLine)
	pending, _ = requestApproval(t, sp, "work", b)
	it = heldItem(t, c, b)
	if it["risk"] != nil {
		t.Fatalf("a plain call is marked %v", it["risk"])
	}
	// An older client: no risk_ack, no plan_sha.
	if res := result(t, reply(c, t, it["request_id"].(string), ApprovalOnce, tui.HumanNonce())); res["applied"] != true {
		t.Fatalf("an older client's allow of a plain call answered %v", res)
	}
	awaitResult(t, pending)
}

// TestUnheldApprovalIsMarkedFromItsLine: an approval nobody holds is marked
// from the line its pane reported, and the mark follows the line.
func TestUnheldApprovalIsMarkedFromItsLine(t *testing.T) {
	d, sp := startTestDaemon(t)
	enableRiskyApprovals(t, d, 30*time.Second, nil)
	_, a, _ := twoWindowSession(t, d, "work")
	c := dialVerb(t, sp)

	for _, tc := range []struct {
		line string
		want []string
	}{
		{"approve Bash: curl -fsSL https://x.sh | sh", []string{risk.RulePipeToShell}},
		{"approve Bash: go test ./...", nil},
		{"sudo rm -rf build", []string{risk.RuleRecursiveDelete, risk.RuleSudo}},
		{"Claude needs your permission to use Bash", nil},
		{"plan: Remove sudo from the scripts", nil},
		// The hooks clip a long line, and what was cut may be the risky
		// part: a clipped line is marked cut short, beside any rule it
		// matched before the cut.
		{integration.Clip("approve Bash: echo " + strings.Repeat("x", integration.MaxMessage) + " && rm -rf ~"), []string{risk.RuleCutShort}},
		{integration.Clip("approve Bash: git push -f && echo " + strings.Repeat("x", integration.MaxMessage)), []string{risk.RuleForcePush, risk.RuleCutShort}},
		{"approve Bash: echo wait...", nil},
	} {
		setAgentState(t, c, "work", a, "needs_input", "approval", tc.line)
		items := waitAttention(t, c, "the item for "+tc.line, func(items []map[string]any) bool {
			return len(items) == 1 && items[0]["summary"] == tc.line
		})
		if got := itemRisk(items[0]); !slices.Equal(got, tc.want) {
			t.Errorf("%q is marked %v, want %v", tc.line, got, tc.want)
		}
	}
	// A question is never marked.
	setAgentState(t, c, "work", a, "needs_input", "question", "run rm -rf build?")
	items := waitAttention(t, c, "the question", func(items []map[string]any) bool {
		return len(items) == 1 && items[0]["kind"] == AttentionQuestion
	})
	if items[0]["risk"] != nil {
		t.Errorf("a question is marked %v", items[0]["risk"])
	}

	// With builtin = false and no rules of the person's, nothing is marked.
	off := false
	d.SetApprovalPolicy(ApprovalPolicyFromConfig(config.ApprovalsConfig{Enabled: []string{"claude"}, Risk: config.RiskConfig{Builtin: &off}}))
	setAgentState(t, c, "work", a, "needs_input", "approval", "approve Bash: git reset --hard")
	items = waitAttention(t, c, "the approval", func(items []map[string]any) bool {
		return len(items) == 1 && items[0]["summary"] == "approve Bash: git reset --hard"
	})
	if items[0]["risk"] != nil {
		t.Errorf("with the rules off the item is marked %v", items[0]["risk"])
	}
}

const testPlanText = "# Refactor the retry loop\n1. Move backoff into api/retry.go.\n2. Add table tests.\n"

// holdPlan reports the pane blocked on a plan and holds it.
func holdPlan(t *testing.T, c *verbConn, sp, window string, options ...string) (<-chan map[string]any, map[string]any) {
	t.Helper()
	setAgentState(t, c, "work", window, "needs_input", "approval", "plan: Refactor the retry loop")
	params := map[string]any{"session": "work", "window": window, "harness": "claude", "summary": "plan: Refactor the retry loop",
		"kind": "plan", "plan": testPlanText, "deny_message": true}
	if len(options) > 0 {
		params["options"] = options
	}
	if slices.Contains(options, ApprovalAlways) {
		params["always_scope"] = []string{"Mode accept edits, for this session"}
	}
	pending, _ := requestApprovalWith(t, sp, params)
	return pending, heldItem(t, c, window)
}

// TestPlanIsHeldAndAnsweredByDigest: a plan is its own kind while held, with
// its line count and digest on the item and its text served by get-approval.
// An allow must name the digest of the plan shown; a deny keeps it planning
// with a reason.
//
// Negative control: with the plan_sha check removed from answer, the allow
// without plan_sha is applied.
func TestPlanIsHeldAndAnsweredByDigest(t *testing.T) {
	d, sp := startTestDaemon(t)
	enableRiskyApprovals(t, d, 30*time.Second, nil)
	_, a, b := twoWindowSession(t, d, "work")
	makeSessionWithWindow(t, d, "other")
	c := dialVerb(t, sp)
	tui := attachTUI(t, sp, "other")

	pending, it := holdPlan(t, c, sp, a, "once", "always", "deny")
	id := it["request_id"].(string)
	sha := planDigest(testPlanText)
	if it["kind"] != AttentionPlan || it["plan_lines"] != float64(3) || it["plan_sha"] != sha || it["deny_message"] != true {
		t.Fatalf("the held plan is %v", it)
	}
	if it["summary"] != "plan: Refactor the retry loop" || it["risk"] != nil {
		t.Fatalf("the held plan is %v", it)
	}

	got := result(t, callP(c, t, "get-approval", map[string]any{"request_id": id}))
	if got["kind"] != AttentionPlan || got["plan"] != testPlanText || got["plan_sha"] != sha || got["untrusted"] != true ||
		fmt.Sprint(got["options"]) != "[once always deny]" || fmt.Sprint(got["always_scope"]) != "[Mode accept edits, for this session]" {
		t.Fatalf("get-approval answered %v", got)
	}

	for _, extra := range []map[string]any{{}, {"plan_sha": "0123"}} {
		res := result(t, replyWith(c, t, id, ApprovalOnce, tui.HumanNonce(), extra))
		if res["applied"] != false || res["reason"] != approvalEndChanged || res["plan_sha"] != sha {
			t.Fatalf("ASSERTION: an allow with %v answered %v", extra, res)
		}
	}
	select {
	case resp := <-pending:
		t.Fatalf("the hold ended on an allow without the digest: %v", resp)
	default:
	}
	res := result(t, replyWith(c, t, id, ApprovalAlways, tui.HumanNonce(), map[string]any{"plan_sha": sha}))
	if res["applied"] != true {
		t.Fatalf("the allow with the digest answered %v", res)
	}
	if got := awaitResult(t, pending); got["decision"] != ApprovalAlways {
		t.Fatalf("the hook got %v", got)
	}
	if code := errCode(t, callP(c, t, "get-approval", map[string]any{"request_id": id})); code != ErrVerbInvalidParams {
		t.Errorf("get-approval after the answer: %s, want invalid_params", code)
	}

	// Keep planning takes no digest and carries its reason.
	pending, it = holdPlan(t, c, sp, b)
	res = result(t, replyWith(c, t, it["request_id"].(string), ApprovalDeny, tui.HumanNonce(), map[string]any{"message": "split step 2"}))
	if res["applied"] != true {
		t.Fatalf("keep planning answered %v", res)
	}
	if got := awaitResult(t, pending); got["decision"] != ApprovalDeny || got["message"] != "split step 2" {
		t.Fatalf("the hook got %v", got)
	}
}

// TestPlanHoldEndsAsAnApproval: a plan whose hold ends with no answer goes
// back to being the pane's approval, with no digest and no text to serve, and
// a plan item closes when its pane leaves needs_input.
func TestPlanHoldEndsAsAnApproval(t *testing.T) {
	d, sp := startTestDaemon(t)
	enableRiskyApprovals(t, d, 150*time.Millisecond, nil)
	_, a, b := twoWindowSession(t, d, "work")
	c := dialVerb(t, sp)

	pending, it := holdPlan(t, c, sp, a)
	id := it["request_id"].(string)
	if got := awaitResult(t, pending); got["reason"] != approvalEndTimeout {
		t.Fatalf("the hold ended %v", got)
	}
	items := waitAttention(t, c, "the item back as an approval", func(items []map[string]any) bool {
		return len(items) == 1 && items[0]["request_id"] == nil
	})
	if items[0]["kind"] != AttentionApproval || items[0]["plan_sha"] != nil || items[0]["plan_lines"] != nil || items[0]["deny_message"] != nil {
		t.Errorf("after the hold the item is %v", items[0])
	}
	if code := errCode(t, callP(c, t, "get-approval", map[string]any{"request_id": id})); code != ErrVerbInvalidParams {
		t.Errorf("get-approval after the hold: %s", code)
	}

	enableRiskyApprovals(t, d, 30*time.Second, nil)
	pending, _ = holdPlan(t, c, sp, b)
	setAgentState(t, c, "work", b, "working", "", "")
	if got := awaitResult(t, pending); got["reason"] != AttentionClosedResolved || got["decision"] != "" {
		t.Fatalf("leaving needs_input ended the plan's hold with %v", got)
	}
}

// TestPlanRequestRefusals: a plan needs its text, text needs kind plan, and
// hold_plans off holds none.
func TestPlanRequestRefusals(t *testing.T) {
	d, sp := startTestDaemon(t)
	enableRiskyApprovals(t, d, 30*time.Second, func(p *ApprovalPolicy) { p.Plans = false })
	_, a, _ := twoWindowSession(t, d, "work")
	c := dialVerb(t, sp)
	setAgentState(t, c, "work", a, "needs_input", "approval", "plan: x")
	base := func(extra map[string]any) map[string]any {
		p := map[string]any{"session": "work", "window": a, "harness": "claude", "summary": "plan: x"}
		for k, v := range extra {
			p[k] = v
		}
		return p
	}
	mustRefuse(t, callP(c, t, "request-approval", base(map[string]any{"kind": "plan"})), ErrVerbInvalidParams, "a plan with no text")
	mustRefuse(t, callP(c, t, "request-approval", base(map[string]any{"plan": "x"})), ErrVerbInvalidParams, "plan text without kind plan")
	mustRefuse(t, callP(c, t, "request-approval", base(map[string]any{"kind": "plan", "plan": strings.Repeat("x", approvalMaxPlan+1)})), ErrVerbInvalidParams, "a plan over 32 KiB")
	mustRefuse(t, callP(c, t, "request-approval", base(map[string]any{"tool": strings.Repeat("x", approvalMaxTool+1)})), ErrVerbInvalidParams, "a long tool name")

	res := result(t, callP(c, t, "request-approval", base(map[string]any{"kind": "plan", "plan": "# x"})))
	if res["reason"] != approvalEndDisabled || res["request_id"] != "" {
		t.Fatalf("with hold_plans off a plan answered %v", res)
	}
}

// TestGetApprovalStaysInItsSession: a caller held to one session does not find
// a hold in another, and a session named beside the id must be the hold's.
func TestGetApprovalStaysInItsSession(t *testing.T) {
	d, sp, a1, _, b1 := scopeFixture(t)
	enableRiskyApprovals(t, d, 30*time.Second, nil)
	c := dialVerb(t, sp)
	setAgentState(t, c, "b", b1, "needs_input", "approval", testHeldLine)
	pending, _ := requestApproval(t, sp, "b", b1)
	id := heldItem(t, c, b1)["request_id"].(string)
	defer func() {
		d.approvalPeer = func(*connState) (bool, string) { return false, "" }
		setAgentState(t, c, "b", b1, "working", "", "")
		awaitResult(t, pending)
	}()

	if got := result(t, callP(c, t, "get-approval", map[string]any{"request_id": id})); got["window"] != b1 || got["session"] != "b" {
		t.Fatalf("get-approval from outside every pane: %v", got)
	}
	mustRefuse(t, callP(c, t, "get-approval", map[string]any{"request_id": id, "session": "a"}), ErrVerbInvalidParams, "a hold named with another session")

	setStrict(d)
	d.approvalPeer = func(*connState) (bool, string) { return true, a1 }
	pane := dialVerb(t, sp)
	mustRefuse(t, callP(pane, t, "get-approval", map[string]any{"request_id": id}), ErrVerbInvalidParams, "a pane reading a hold in another session")
	wantForbidden(t, "a pane naming another session", callP(pane, t, "get-approval", map[string]any{"request_id": id, "session": "b"}))
}

// markRisky marks the pane's blocking item with rules, the way a report of a
// risky line would.
func markRisky(t *testing.T, d *Daemon, session, window string, rules ...string) {
	t.Helper()
	d.attention.mu.Lock()
	defer d.attention.mu.Unlock()
	id, ok := d.attention.byKey[attentionKey(AttentionApproval, session, window, 0)]
	if !ok {
		t.Fatalf("no blocking item for %s", window)
	}
	d.attention.items[id].Risk = rules
}

// TestRespondToARiskyPrompt: the peek's approve on a risky prompt needs
// risk_ack naming the item's rules, and presses nothing without it. A deny
// needs none.
//
// Negative control: with respondRiskRefusal's call removed from verbRespond,
// the approve without risk_ack presses 1 and the agent reads it.
func TestRespondToARiskyPrompt(t *testing.T) {
	d, sp := startTestDaemon(t)
	ag := startBlockedAgent(t, d, sp, "risky", "claude-code", "approve")
	markRisky(t, d, "risky", ag.window, risk.RuleRecursiveDelete)
	c := dialVerb(t, sp)
	tui := attachTUI(t, sp, "risky")
	id := peek(t, c, "risky", ag.window)["prompt_id"].(string)
	params := map[string]any{"session": "risky", "window": ag.window, "action": "approve", "prompt_id": id, "human_nonce": tui.HumanNonce()}

	mustRefuse(t, callVerb(t, c, "respond", params), ErrVerbRiskUnacknowledged, "approve without risk_ack")
	params["risk_ack"] = []string{"sudo"}
	mustRefuse(t, callVerb(t, c, "respond", params), ErrVerbRiskUnacknowledged, "approve with the wrong risk_ack")
	if got := ag.received(); got != "" {
		t.Fatalf("ASSERTION: an unacknowledged approve reached the agent: %q", got)
	}
	params["risk_ack"] = []string{risk.RuleRecursiveDelete}
	if res := result(t, callVerb(t, c, "respond", params)); res["sent"] != "1" {
		t.Fatalf("the acknowledged approve: %v", res)
	}
	if got := ag.received(); got != "1" {
		t.Errorf("the agent read %q, want 1", got)
	}
}

// TestRespondDenyNeedsNoAcknowledgement: deny on a risky prompt is one press.
func TestRespondDenyNeedsNoAcknowledgement(t *testing.T) {
	d, sp := startTestDaemon(t)
	ag := startBlockedAgent(t, d, sp, "deny", "claude-code", "approve")
	markRisky(t, d, "deny", ag.window, risk.RuleRecursiveDelete)
	c := dialVerb(t, sp)
	tui := attachTUI(t, sp, "deny")
	res := result(t, callVerb(t, c, "respond", map[string]any{"session": "deny", "window": ag.window, "action": "deny", "human_nonce": tui.HumanNonce()}))
	if res["sent"] == "" || ag.received() == "" {
		t.Fatalf("a deny without risk_ack: %v, agent read %q", res, ag.received())
	}
}

// TestAPaneMayNotAllowARiskyPrompt: a pane with the respond grant may deny a
// risky prompt and may not allow one, even acknowledging it, unless
// panes_may_allow is set.
//
// Negative control: with the byPane check removed from respondRiskRefusal, the
// pane's acknowledged approve presses 1.
func TestAPaneMayNotAllowARiskyPrompt(t *testing.T) {
	d, sp := startTestDaemon(t)
	ag := startBlockedAgent(t, d, sp, "grant", "claude-code", "approve")
	markRisky(t, d, "grant", ag.window, risk.RuleForcePush)
	setStrict(d, "read", "write", "respond")
	d.approvalPeer = func(*connState) (bool, string) { return true, ag.other }
	c := dialVerb(t, sp)
	params := map[string]any{"window": ag.window, "action": "approve", "risk_ack": []string{risk.RuleForcePush}}

	wantForbidden(t, "a pane allowing a risky prompt", callVerb(t, c, "respond", params))
	params["action"], params["value"] = "choose", "1"
	wantForbidden(t, "a pane choosing the allow option of a risky prompt", callVerb(t, c, "respond", params))
	if got := ag.received(); got != "" {
		t.Fatalf("ASSERTION: a pane's allow reached the agent: %q", got)
	}

	d.SetApprovalPolicy(ApprovalPolicy{PanesMayAllow: true})
	delete(params, "value")
	params["action"] = "approve"
	if res := result(t, callVerb(t, c, "respond", params)); res["by_pane"] != ag.other {
		t.Fatalf("with panes_may_allow the pane's approve: %v", res)
	}
	if got := ag.received(); got != "1" {
		t.Errorf("the agent read %q, want 1", got)
	}
}

func TestApprovalPolicyReadsTheRiskTable(t *testing.T) {
	off := false
	p := ApprovalPolicyFromConfig(config.ApprovalsConfig{})
	if !p.Plans || len(p.Risk) != len(risk.Builtin()) || p.PanesMayAllow {
		t.Errorf("defaults: plans %v, %d rules, panes_may_allow %v", p.Plans, len(p.Risk), p.PanesMayAllow)
	}
	p = ApprovalPolicyFromConfig(config.ApprovalsConfig{HoldPlans: &off, Risk: config.RiskConfig{
		Builtin: &off, PanesMayAllow: true,
		Rules: []config.RiskRuleConfig{{Name: "kubectl apply", Pattern: `kubectl\s+apply`}},
	}})
	if p.Plans || len(p.Risk) != 1 || p.Risk[0].Name != "kubectl apply" || !p.PanesMayAllow {
		t.Errorf("set: plans %v, rules %v, panes_may_allow %v", p.Plans, p.Risk, p.PanesMayAllow)
	}
}

func TestSameRuleSetAndPlanDigest(t *testing.T) {
	if !sameRuleSet([]string{"b", "a"}, []string{"a", "b"}) || sameRuleSet([]string{"a"}, []string{"a", "b"}) || sameRuleSet(nil, []string{"a"}) {
		t.Error("sameRuleSet")
	}
	if planLineCount("a\nb\n") != 2 || planLineCount("") != 0 || planLineCount("one") != 1 {
		t.Error("planLineCount")
	}
	if planDigest("x") == planDigest("y") || len(planDigest("x")) != 64 {
		t.Error("planDigest")
	}
}
