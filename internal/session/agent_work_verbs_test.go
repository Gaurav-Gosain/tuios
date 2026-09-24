package session

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// These tests pin the foundation of the agent review, triage, queue and
// approval work: what each new verb may be called by, over which link
// capability, and the wire fields it adds. The tables are the security model's
// first line, so each entry is written out here rather than read back from the
// table it checks.

// agentWorkVerbTable is what each new verb needs: its restricted-connection
// and pane-grant class, its link capabilities, and whether it types into a
// pane.
var agentWorkVerbTable = []struct {
	verb   string
	scope  scopeKind
	link   []string
	typing bool
}{
	{"review-diff", scopeRead, []string{config.LinkAllowWrite}, false},
	{"compare-fan", scopeRead, []string{config.LinkAllowList}, false},
	{"agent-activity", scopeRead, []string{config.LinkAllowList}, false},
	{"list-queued", scopeRead, []string{config.LinkAllowList}, false},
	{"get-approval", scopeRead, []string{config.LinkAllowList}, false},
	{"review-note", scopeWrite, []string{config.LinkAllowWrite}, false},
	{"send-review", scopeWrite, []string{config.LinkAllowWrite}, true},
	{"queue-prompt", scopeWrite, []string{config.LinkAllowWrite}, true},
	{"cancel-queued", scopeWrite, []string{config.LinkAllowWrite}, false},
	{"verify-fan", scopeLaunch, []string{config.LinkAllowOpen, config.LinkAllowWrite}, false},
	{"keep-fan", scopeDeny, []string{config.LinkAllowWrite}, false},
	{"mark-attention", scopeDeny, []string{config.LinkAllowRespond}, false},
}

func TestAgentWorkVerbsAreClassifiedAsThePlanSays(t *testing.T) {
	for _, row := range agentWorkVerbTable {
		if _, ok := verbRegistry[row.verb]; !ok {
			t.Errorf("%s is not registered", row.verb)
			continue
		}
		if got, ok := verbScopes[row.verb]; !ok || got != row.scope {
			t.Errorf("%s has scope %v (listed %v), want %v", row.verb, got, ok, row.scope)
		}
		if got := grantKind(row.verb); got != row.scope {
			t.Errorf("%s needs %v from a pane, want %v", row.verb, got, row.scope)
		}
		if got := verbCapabilities[row.verb]; !slices.Equal(got, row.link) {
			t.Errorf("%s needs %v over a link, want %v", row.verb, got, row.link)
		}
		if typingVerbs[row.verb] != row.typing {
			t.Errorf("%s typing = %v, want %v", row.verb, typingVerbs[row.verb], row.typing)
		}
	}
}

// TestAgentWorkVerbsAnswerNotBuilt: a registered verb whose handler has not
// landed says so, with the internal code and a message naming the verb, and
// does nothing.
func TestAgentWorkVerbsAnswerNotBuilt(t *testing.T) {
	d, sp := startTestDaemon(t)
	_, a, _ := twoWindowSession(t, d, "work")
	c := dialVerb(t, sp)
	for verb, params := range map[string]map[string]any{
		"review-diff":    {"session": "work", "window": a},
		"review-note":    {"action": "list", "session": "work", "window": a},
		"send-review":    {"session": "work", "window": a},
		"compare-fan":    {"session": "work"},
		"verify-fan":     {"session": "work", "command": "true"},
		"keep-fan":       {"session": "work"},
		"agent-activity": {"session": "work", "window": a},
		"queue-prompt":   {"session": "work", "window": a, "text": "hello"},
		"list-queued":    {"session": "work", "window": a},
		"cancel-queued":  {"session": "work", "window": a, "all": true},
		"get-approval":   {"request_id": "9f86d081884c7d65"},
	} {
		resp := callP(c, t, verb, params)
		mustRefuse(t, resp, ErrVerbInternal, verb+" before it is built")
		if msg := resp["error"].(map[string]any)["message"].(string); !strings.Contains(msg, verb) || !strings.Contains(msg, "not built yet") {
			t.Errorf("%s says %q, want it to name itself as not built", verb, msg)
		}
	}
	// The parameter checks run before the stub, so the contract is already
	// what the handlers will enforce.
	mustRefuse(t, callP(c, t, "review-note", map[string]any{"action": "shout"}), ErrVerbInvalidParams, "an unknown note action")
	mustRefuse(t, callP(c, t, "review-diff", map[string]any{"context": 21}), ErrVerbInvalidParams, "context past 20")
	mustRefuse(t, callP(c, t, "verify-fan", map[string]any{"command": " "}), ErrVerbInvalidParams, "an empty check")
	mustRefuse(t, callP(c, t, "queue-prompt", map[string]any{"window": a, "text": strings.Repeat("x", queueMaxText+1)}), ErrVerbInvalidParams, "a message over 16 KiB")
	mustRefuse(t, callP(c, t, "cancel-queued", map[string]any{"window": a}), ErrVerbInvalidParams, "a cancel that names nothing")
	mustRefuse(t, callP(c, t, "agent-activity", map[string]any{"limit": 257}), ErrVerbInvalidParams, "a limit past the ring")
}

// TestMarkAttentionIsThePersonsOnly: the proof comes before anything else, so
// a caller without a live nonce is refused even while the verb is a stub.
func TestMarkAttentionIsThePersonsOnly(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "work")
	makeSessionWithWindow(t, d, "other")
	c := dialVerb(t, sp)
	for _, nonce := range []string{"", "0123456789abcdef0123456789abcdef"} {
		mustRefuse(t, callP(c, t, "mark-attention", map[string]any{"id": "1", "action": "snooze", "for_ms": 1000, "human_nonce": nonce}),
			ErrVerbNotHuman, "mark-attention with nonce "+nonce)
	}
	// The person passes the proof and reaches the stub.
	tui := attachTUI(t, sp, "other")
	mustRefuse(t, callP(c, t, "mark-attention", map[string]any{"id": "1", "action": "snooze", "for_ms": 1000, "human_nonce": tui.HumanNonce()}),
		ErrVerbInternal, "mark-attention from the person before it is built")
	mustRefuse(t, callP(c, t, "mark-attention", map[string]any{"id": "1", "action": "hide", "human_nonce": tui.HumanNonce()}),
		ErrVerbInvalidParams, "an unknown action")
}

// TestAgentWorkVerbsAreHeldToPaneGrants: a pane without admin reaches only its
// own session and fan group with the reads, needs write for the writes and fan
// for verify-fan, types only into panes that hold nothing it does not, and
// cannot keep a fan or touch the Inbox.
func TestAgentWorkVerbsAreHeldToPaneGrants(t *testing.T) {
	d, sp, a1, a2, b1 := scopeFixture(t)
	setStrict(d)
	d.approvalPeer = func(*connState) (bool, string) { return true, a1 }
	c := dialVerb(t, sp)

	// Its own session: the grant check passes and the stub answers.
	for verb, params := range map[string]map[string]any{
		"review-diff":    {"window": a2},
		"compare-fan":    {},
		"agent-activity": {"window": a2},
		"list-queued":    {"window": a2},
		"get-approval":   {"request_id": "9f86d081884c7d65"},
		"review-note":    {"action": "list", "window": a2},
		"send-review":    {"window": a2},
		"queue-prompt":   {"window": a2, "text": "hi"},
		"cancel-queued":  {"window": a2, "all": true},
		"verify-fan":     {"command": "true"},
	} {
		mustRefuse(t, callP(c, t, verb, params), ErrVerbInternal, verb+" in the pane's own session")
	}
	// Another session is out of reach for every one of them.
	for verb, params := range map[string]map[string]any{
		"review-diff":    {"session": "b", "window": b1},
		"compare-fan":    {"session": "b"},
		"agent-activity": {"session": "b", "window": b1},
		"list-queued":    {"session": "b", "window": b1},
		"get-approval":   {"session": "b", "request_id": "9f86d081884c7d65"},
		"review-note":    {"action": "list", "session": "b", "window": b1},
		"send-review":    {"session": "b", "window": b1},
		"queue-prompt":   {"session": "b", "window": b1, "text": "hi"},
		"cancel-queued":  {"session": "b", "window": b1, "all": true},
		"verify-fan":     {"session": "b", "command": "true"},
	} {
		wantForbidden(t, verb+" in another session", callP(c, t, verb, params))
	}
	// The person's and admin's verbs.
	wantForbidden(t, "keep-fan", callP(c, t, "keep-fan", map[string]any{"session": "a"}))
	wantForbidden(t, "mark-attention", callP(c, t, "mark-attention", map[string]any{"id": "1", "action": "wake", "human_nonce": "x"}))

	// read alone: no writes, no launch.
	setStrict(d, "read")
	for verb, params := range map[string]map[string]any{
		"review-note":   {"action": "list", "window": a2},
		"send-review":   {"window": a2},
		"queue-prompt":  {"window": a2, "text": "hi"},
		"cancel-queued": {"window": a2, "all": true},
		"verify-fan":    {"command": "true"},
	} {
		wantForbidden(t, verb+" with only read", callP(c, t, verb, params))
	}
	mustRefuse(t, callP(c, t, "review-diff", map[string]any{"window": a2}), ErrVerbInternal, "review-diff with read")
}

// TestQueuedTypingIsHeldToTheTarget: queue-prompt and send-review type into a
// pane later, and are held at the call like send-text: never into a pane that
// holds more than the caller, and never into a prompt without respond.
func TestQueuedTypingIsHeldToTheTarget(t *testing.T) {
	d, sp, a1, a2, _ := scopeFixture(t)
	d.approvalPeer = func(*connState) (bool, string) { return false, "" }
	person := dialVerb(t, sp)
	result(t, callP(person, t, "set-pane-grants", map[string]any{"session": "a", "window": a1, "grants": []string{"read", "write"}}))

	d.approvalPeer = func(*connState) (bool, string) { return true, a1 }
	c := dialVerb(t, sp)
	// a2 is on the open default, so it holds admin.
	wantForbidden(t, "queue-prompt into an admin sibling", callP(c, t, "queue-prompt", map[string]any{"window": a2, "text": "hi"}))
	wantForbidden(t, "send-review into an admin sibling", callP(c, t, "send-review", map[string]any{"window": a2}))
	// Its own pane is its own to type into.
	mustRefuse(t, callP(c, t, "queue-prompt", map[string]any{"window": a1, "text": "hi"}), ErrVerbInternal, "queue-prompt into itself")

	// A sibling that holds no more, on a prompt: respond is needed.
	d.approvalPeer = func(*connState) (bool, string) { return false, "" }
	result(t, callP(person, t, "set-pane-grants", map[string]any{"session": "a", "window": a2, "grants": []string{"read"}}))
	d.approvalPeer = func(*connState) (bool, string) { return true, a1 }
	mustRefuse(t, callP(c, t, "queue-prompt", map[string]any{"window": a2, "text": "hi"}), ErrVerbInternal, "queue-prompt into a sibling that holds less")
	d.approvalPeer = func(*connState) (bool, string) { return false, "" }
	setAgentState(t, person, "a", a2, string(AgentStateNeedsInput), "approval", "run rm -rf build?")
	d.approvalPeer = func(*connState) (bool, string) { return true, a1 }
	wantForbidden(t, "queue-prompt into a prompt", callP(c, t, "queue-prompt", map[string]any{"window": a2, "text": "yes"}))
}

// TestRestrictedConnectionsAndAgentWorkVerbs: read_only refuses what types,
// writes the queue or notes, or launches; scope own refuses the person's
// verbs.
func TestRestrictedConnectionsAndAgentWorkVerbs(t *testing.T) {
	d, _ := startTestDaemon(t)
	cs := &connState{}
	cs.scope.Store(&connScope{readOnly: true})
	for _, verb := range []string{"queue-prompt", "send-review", "review-note", "cancel-queued", "verify-fan"} {
		if _, verr := d.checkScope(cs, verb, json.RawMessage(`{}`)); verr == nil || verr.Code != ErrVerbForbidden {
			t.Errorf("%s on a read-only connection was not refused: %v", verb, verr)
		}
	}
	for _, verb := range []string{"review-diff", "compare-fan", "agent-activity", "list-queued", "get-approval"} {
		if _, verr := d.checkScope(cs, verb, json.RawMessage(`{}`)); verr != nil {
			t.Errorf("%s, a read, was refused on a read-only connection: %v", verb, verr.Message)
		}
	}
	for _, verb := range []string{"keep-fan", "mark-attention"} {
		if _, verr := d.checkScope(cs, verb, json.RawMessage(`{}`)); verr == nil || verr.Code != ErrVerbForbidden {
			t.Errorf("%s on a restricted connection was not refused: %v", verb, verr)
		}
	}
}

// TestAgentWorkVerbsAreHeldToTheLinkPolicy: a machine that may only list can
// read the counts and states and not the diff, and a machine that may open
// but not write cannot run a check.
func TestAgentWorkVerbsAreHeldToTheLinkPolicy(t *testing.T) {
	d, sp := startTestDaemon(t)
	_, a, _ := twoWindowSession(t, d, "work")
	d.SetLinkPolicies(map[string]config.HostConfig{
		"viewer": {Allow: []string{"list"}},
		"opener": {Allow: []string{"list", "open"}},
	})
	viewer := dialLink(t, sp)
	result(t, linkPeer(t, viewer, "viewer"))
	for verb, params := range map[string]map[string]any{
		"compare-fan":    {"session": "work"},
		"agent-activity": {"session": "work", "window": a},
		"list-queued":    {"session": "work", "window": a},
		"get-approval":   {"request_id": "9f86d081884c7d65"},
	} {
		mustRefuse(t, callP(viewer, t, verb, params), ErrVerbInternal, verb+" from a machine that may list")
	}
	for verb, params := range map[string]map[string]any{
		"review-diff":    {"session": "work", "window": a},
		"review-note":    {"action": "list", "session": "work", "window": a},
		"send-review":    {"session": "work", "window": a},
		"queue-prompt":   {"session": "work", "window": a, "text": "hi"},
		"cancel-queued":  {"session": "work", "window": a, "all": true},
		"keep-fan":       {"session": "work"},
		"verify-fan":     {"session": "work", "command": "true"},
		"mark-attention": {"id": "1", "action": "wake", "human_nonce": "x"},
	} {
		mustRefuse(t, callP(viewer, t, verb, params), ErrVerbForbidden, verb+" from a machine that may only list")
	}
	opener := dialLink(t, sp)
	result(t, linkPeer(t, opener, "opener"))
	mustRefuse(t, callP(opener, t, "verify-fan", map[string]any{"session": "work", "command": "true"}),
		ErrVerbForbidden, "verify-fan from a machine that may open but not write")

	// The default grants write and not respond.
	anon := dialLink(t, sp)
	mustRefuse(t, callP(anon, t, "review-diff", map[string]any{"session": "work", "window": a}), ErrVerbInternal, "review-diff with the default policy")
	mustRefuse(t, callP(anon, t, "mark-attention", map[string]any{"id": "1", "action": "wake", "human_nonce": "x"}),
		ErrVerbForbidden, "mark-attention with the default policy")
}

// TestNewParamsOfOlderVerbsDoNothingUntilBuilt: an older verb that takes a new
// parameter refuses a call that sets it rather than dropping it, and does
// nothing, except where dropping it is what keeps a report working.
func TestNewParamsOfOlderVerbsDoNothingUntilBuilt(t *testing.T) {
	d, sp := startTestDaemon(t)
	enableApprovals(t, d, 30*time.Second)
	sess, a, b := twoWindowSession(t, d, "work")
	makeSessionWithWindow(t, d, "other")
	c := dialVerb(t, sp)
	tui := attachTUI(t, sp, "other")

	// A plan or a risk field holds nothing: the hook gives the prompt back.
	setAgentState(t, c, "work", a, "needs_input", "approval", testHeldLine)
	for _, extra := range []map[string]any{{"kind": "plan", "plan": "# Plan"}, {"tool": "Bash"}, {"target": "rm -rf build"}, {"deny_message": true}} {
		params := map[string]any{"session": "work", "window": a, "harness": "claude", "summary": testHeldLine}
		for k, v := range extra {
			params[k] = v
		}
		mustRefuse(t, callP(c, t, "request-approval", params), ErrVerbInternal, fmt.Sprintf("request-approval with %v", extra))
	}
	mustRefuse(t, callP(c, t, "request-approval", map[string]any{"session": "work", "window": a, "harness": "claude", "summary": testHeldLine, "kind": "poem"}),
		ErrVerbInvalidParams, "an unknown kind")
	if items, _ := listAttention(t, c, ""); len(items) != 1 || items[0]["request_id"] != nil {
		t.Fatalf("a refused request held something: %v", items)
	}

	// An answer that names a risk or a plan answers nothing.
	pending, _ := requestApproval(t, sp, "work", a, "once", "deny")
	id := heldItem(t, c, a)["request_id"].(string)
	for _, extra := range []map[string]any{{"risk_ack": []string{"sudo"}}, {"plan_sha": "abc"}} {
		params := map[string]any{"request_id": id, "decision": "once", "human_nonce": tui.HumanNonce()}
		for k, v := range extra {
			params[k] = v
		}
		mustRefuse(t, callP(c, t, "reply-approval", params), ErrVerbInternal, fmt.Sprintf("reply-approval with %v", extra))
	}
	select {
	case resp := <-pending:
		t.Fatalf("the hold ended on a refused reply: %v", resp)
	default:
	}
	// Without them the reply is answered as before.
	res := result(t, reply(c, t, id, ApprovalOnce, tui.HumanNonce()))
	if res["applied"] != true {
		t.Fatalf("a plain reply answered %v", res)
	}
	awaitResult(t, pending)

	// respond with risk_ack presses nothing.
	setAgentState(t, c, "work", b, "needs_input", "approval", "ok?")
	mustRefuse(t, callP(c, t, "respond", map[string]any{"session": "work", "window": b, "action": "approve", "risk_ack": []string{"sudo"}, "human_nonce": tui.HumanNonce()}),
		ErrVerbInternal, "respond with risk_ack")
	if w, _ := findWindowState(sess.GetState(), b); w.AgentState != AgentStateNeedsInput {
		t.Errorf("respond with risk_ack moved the pane to %s", w.AgentState.Name())
	}

	// activity rides a state report, so the state still applies; its shape is
	// still checked.
	res = result(t, callP(c, t, "set-agent-state", map[string]any{"session": "work", "window": b, "state": "working",
		"activity": map[string]any{"event": "tool", "tool": "Bash", "target": "go test ./..."}}))
	if res["applied"] != true {
		t.Errorf("a report with activity was not applied: %v", res)
	}
	mustRefuse(t, callP(c, t, "set-agent-state", map[string]any{"session": "work", "window": b, "state": "idle",
		"activity": map[string]any{"event": "dance"}}), ErrVerbInvalidParams, "an unknown activity event")
	if w, _ := findWindowState(sess.GetState(), b); w.AgentState != AgentStateWorking {
		t.Errorf("a refused report moved the pane to %s", w.AgentState.Name())
	}

	// Nothing is snoozed, so include_snoozed lists what the list lists.
	plain, _ := listAttention(t, c, "")
	with, _ := listAttention(t, c, `{"include_snoozed":true}`)
	if len(plain) != len(with) {
		t.Errorf("include_snoozed listed %d items, the plain list %d", len(with), len(plain))
	}

	// get-agent-state and list-agents say how much is queued.
	got := result(t, callP(c, t, "get-agent-state", map[string]any{"session": "work", "window": b}))
	if got["queued"] != float64(0) {
		t.Errorf("get-agent-state queued = %v, want 0", got["queued"])
	}
}

// TestAttentionItemWireIsAdditive: an item without the new fields encodes as
// it did before they existed, and one with them round trips.
func TestAttentionItemWireIsAdditive(t *testing.T) {
	raw, err := json.Marshal(AttentionItem{ID: "1", Kind: AttentionApproval, Session: "work", Since: 1, Seq: 2})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"snoozed_until", "marked_unread", "risk", "deny_message", "plan_lines", "plan_sha"} {
		if strings.Contains(string(raw), `"`+key+`"`) {
			t.Errorf("an item without %s encodes it: %s", key, raw)
		}
	}
	in := AttentionItem{ID: "2", Kind: AttentionPlan, Session: "work", SnoozedUntil: -1, MarkedUnread: true,
		Risk: []string{"sudo", "force push"}, DenyMessage: true, PlanLines: 14, PlanSHA: "ab12"}
	raw, _ = json.Marshal(in)
	var out AttentionItem
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if !attentionSame(in, out) || out.PlanSHA != "ab12" || out.SnoozedUntil != -1 {
		t.Errorf("round trip lost a field: %+v", out)
	}
	// An older reader, which knows none of them, still decodes the item.
	var old struct {
		ID   string `json:"id"`
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(raw, &old); err != nil || old.ID != "2" || old.Kind != AttentionPlan {
		t.Errorf("an older reader decoded %+v (%v)", old, err)
	}
}

// TestAttentionSameSeesTheNewFields: a change to any of them is news, or an
// update would not be published.
func TestAttentionSameSeesTheNewFields(t *testing.T) {
	base := AttentionItem{Kind: AttentionApproval, Session: "work", Window: "w"}
	for name, change := range map[string]func(*AttentionItem){
		"snoozed_until": func(it *AttentionItem) { it.SnoozedUntil = 5 },
		"marked_unread": func(it *AttentionItem) { it.MarkedUnread = true },
		"risk":          func(it *AttentionItem) { it.Risk = []string{"sudo"} },
		"deny_message":  func(it *AttentionItem) { it.DenyMessage = true },
		"plan_lines":    func(it *AttentionItem) { it.PlanLines = 3 },
		"plan_sha":      func(it *AttentionItem) { it.PlanSHA = "x" },
	} {
		changed := base
		change(&changed)
		if attentionSame(base, changed) {
			t.Errorf("a change to %s reads as no change", name)
		}
	}
}

// TestAPlanSharesThePanesBlockingItem: a plan is keyed with the pane's
// approval and question, so the pane leaving needs_input closes it.
func TestAPlanSharesThePanesBlockingItem(t *testing.T) {
	if attentionKey(AttentionPlan, "work", "w", 0) != attentionKey(AttentionApproval, "work", "w", 0) {
		t.Fatal("a plan is keyed apart from the pane's approval")
	}
	a, events := recordingAttention()
	a.mu.Lock()
	a.upsertLocked(AttentionItem{Kind: AttentionPlan, Session: "work", Window: "w1", Summary: "Refactor the retry loop"})
	a.mu.Unlock()
	if items := openItems(t, a); len(items) != 1 || items[0].Kind != AttentionPlan {
		t.Fatalf("the plan did not open: %+v", items)
	}
	a.noteSessionEvent("work", agentEvent("w1", "needs_input", "working", "", "", 0, 0))
	if items := openItems(t, a); len(items) != 0 {
		t.Fatalf("leaving needs_input left %+v open", items)
	}
	if got := actions(*events); got != "open:plan close:plan" {
		t.Errorf("events = %q", got)
	}
	if i := slices.Index(AttentionKindNames, AttentionPlan); i != 1 {
		t.Errorf("plan sorts at %d, want right after approval", i)
	}
	if st := AttentionSelectorTarget(AttentionItem{Kind: AttentionPlan}); st.State != AgentStateNeedsInput.Name() || !st.NeedsYou {
		t.Errorf("a plan reads to a selector as %+v", st)
	}
}

// TestAHostsRiskAndPlanLengthAreMirrored: display only, and a host cannot
// hand this machine a plan digest to answer with.
func TestAHostsRiskAndPlanLengthAreMirrored(t *testing.T) {
	in := AttentionItem{ID: "7", Kind: AttentionPlan, Session: "work", Since: 1,
		Risk: []string{"sudo\x1b[31m"}, PlanLines: 14, PlanSHA: "ab", DenyMessage: true, SnoozedUntil: 9, MarkedUnread: true}
	out, ok := sanitizeHostItem("build", in, time.Now())
	if !ok {
		t.Fatal("the host's plan was not mirrored")
	}
	if len(out.Risk) != 1 || strings.ContainsRune(out.Risk[0], 0x1b) || out.PlanLines != 14 {
		t.Errorf("mirrored %+v", out)
	}
	if out.PlanSHA != "" || out.DenyMessage || out.SnoozedUntil != 0 || out.MarkedUnread {
		t.Errorf("a host item carries fields only this machine's own items may: %+v", out)
	}
	if !slices.Contains(hostCloseReasons, AttentionClosedSnoozed) {
		t.Error("a host's snoozed close reads as resolved")
	}
}

// TestTheQueueCountIsTheDaemons: no client sets it, so a client push neither
// raises nor clears it, and a restore clears it.
func TestTheQueueCountIsTheDaemons(t *testing.T) {
	canonical := &SessionState{Windows: []WindowState{{ID: "a", AgentQueued: 2}, {ID: "b"}}}
	incoming := &SessionState{Windows: []WindowState{{ID: "a"}, {ID: "b", AgentQueued: 5}}}
	retainDaemonExclusive(incoming, canonical)
	if incoming.Windows[0].AgentQueued != 2 || incoming.Windows[1].AgentQueued != 0 {
		t.Errorf("after a client push the queues read %d and %d, want 2 and 0",
			incoming.Windows[0].AgentQueued, incoming.Windows[1].AgentQueued)
	}
	w := WindowState{ID: "a", AgentQueued: 3}
	clearLiveAgent(&w)
	if w.AgentQueued != 0 {
		t.Errorf("a restored pane still counts %d queued", w.AgentQueued)
	}
	// It reaches a client listing another session, and a change to it is a
	// change the listing compares.
	if windowSummariesAgree(WindowSummary{ID: "a", AgentQueued: 1}, WindowSummary{ID: "a"}) {
		t.Error("two summaries that differ in the queue agree")
	}
	one := StateFingerprint(&SessionState{Windows: []WindowState{{ID: "a", AgentQueued: 1}}})
	two := StateFingerprint(&SessionState{Windows: []WindowState{{ID: "a", AgentQueued: 2}}})
	if one == two {
		t.Error("the state fingerprint does not see the queue")
	}
}

// TestTheActivityEventIsOptIn: a subscriber that names no types never gets
// it; one that names it does.
func TestTheActivityEventIsOptIn(t *testing.T) {
	ev := streamEvent{Type: EventAgentActivity, Session: "work", Window: "w"}
	if (eventFilter{}).match(ev) {
		t.Error("a subscriber that named no types got agent-activity")
	}
	if !(eventFilter{types: map[string]bool{EventAgentActivity: true}}).match(ev) {
		t.Error("a subscriber that named agent-activity did not get it")
	}
	if !(eventFilter{}).match(streamEvent{Type: EventAgentState}) {
		t.Error("the opt-in rule dropped an ordinary event")
	}
	if !slices.Contains(knownEventTypes, EventAgentActivity) {
		t.Error("subscribe does not accept agent-activity in types")
	}
}

// TestTheFanVerifyFieldIsAdditive: a worktree with no check encodes as before.
func TestTheFanVerifyFieldIsAdditive(t *testing.T) {
	raw, _ := json.Marshal(WorktreeInfo{Group: "retry"})
	if strings.Contains(string(raw), "verify") {
		t.Errorf("a worktree with no check encodes one: %s", raw)
	}
	exit := 1
	raw, _ = json.Marshal(WorktreeInfo{Group: "retry", Verify: &FanVerify{Command: "make lint", State: VerifyFailed, Exit: &exit}})
	var out WorktreeInfo
	if err := json.Unmarshal(raw, &out); err != nil || out.Verify == nil || *out.Verify.Exit != 1 || out.Verify.State != VerifyFailed {
		t.Errorf("round trip gave %+v (%v)", out.Verify, err)
	}
}
