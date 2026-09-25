package session

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// These tests pin the agent review, triage, queue and approval verbs: who may
// call each one, over which link capability, and the wire fields they add.

// TestMarkAttentionIsThePersonsOnly: the proof comes before anything else, so
// a caller without a live nonce is refused before the item is even looked up.
func TestMarkAttentionIsThePersonsOnly(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "work")
	makeSessionWithWindow(t, d, "other")
	c := dialVerb(t, sp)
	for _, nonce := range []string{"", "0123456789abcdef0123456789abcdef"} {
		mustRefuse(t, callP(c, t, "mark-attention", map[string]any{"id": "1", "action": "snooze", "for_ms": 1000, "human_nonce": nonce}),
			ErrVerbNotHuman, "mark-attention with nonce "+nonce)
	}
	// The person passes the proof and reaches the item, which is not there.
	tui := attachTUI(t, sp, "other")
	mustRefuse(t, callP(c, t, "mark-attention", map[string]any{"id": "1", "action": "snooze", "for_ms": 1000, "human_nonce": tui.HumanNonce()}),
		ErrVerbInvalidParams, "mark-attention from the person on an id that names nothing")
	mustRefuse(t, callP(c, t, "mark-attention", map[string]any{"id": "1", "action": "hide", "human_nonce": tui.HumanNonce()}),
		ErrVerbInvalidParams, "an unknown action")
}

// TestAgentWorkVerbsAreHeldToPaneGrants: a pane without admin reaches only its
// own session and fan group with the reads, needs write for the writes and fan
// for verify-fan, types only into panes that hold nothing it does not, and
// cannot keep a fan or touch the Inbox.
func TestAgentWorkVerbsAreHeldToPaneGrants(t *testing.T) {
	// The panes start outside any repository, so the review verbs answer
	// not_repo past the grant check without reading one.
	t.Chdir(t.TempDir())
	d, sp, a1, a2, b1 := scopeFixture(t)
	setStrict(d)
	d.setApprovalPeer(func(*connState) (bool, string) { return true, a1 })
	c := dialVerb(t, sp)

	// Its own session: the grant check passes and the handler answers.
	for verb, params := range map[string]map[string]any{
		"review-diff": {"window": a2},
		"review-note": {"action": "list", "window": a2},
		"send-review": {"window": a2},
	} {
		wantReached(t, verb+" in the pane's own session", callP(c, t, verb, params))
	}
	// agent-activity is built: the grant check passes and it answers.
	result(t, callP(c, t, "agent-activity", map[string]any{"window": a2}))
	// The queue's verbs are built: they pass the grant check and answer.
	result(t, callP(c, t, "list-queued", map[string]any{"window": a2}))
	result(t, callP(c, t, "cancel-queued", map[string]any{"window": a2, "all": true}))
	mustRefuse(t, callP(c, t, "queue-prompt", map[string]any{"window": a2, "text": "hi"}), ErrVerbInvalidParams, "queue-prompt in the pane's own session, for a pane with no agent")
	// The built fan verbs pass the grant check and reach their handler, which
	// answers for a session that is not a fan.
	wantReached(t, "compare-fan in the pane's own session", callP(c, t, "compare-fan", map[string]any{}))
	wantReached(t, "verify-fan in the pane's own session", callP(c, t, "verify-fan", map[string]any{"command": "true"}))
	// get-approval is built: in its own session the grant check passes and
	// the handler finds no such hold.
	mustRefuse(t, callP(c, t, "get-approval", map[string]any{"request_id": "9f86d081884c7d65"}), ErrVerbInvalidParams, "get-approval in the pane's own session")
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
	wantReached(t, "review-diff with read", callP(c, t, "review-diff", map[string]any{"window": a2}))
}

// TestQueuedTypingIsHeldToTheTarget: queue-prompt and send-review type into a
// pane later, and are held at the call like send-text: never into a pane that
// holds more than the caller, and never into a prompt without respond.
func TestQueuedTypingIsHeldToTheTarget(t *testing.T) {
	d, sp, a1, a2, _ := scopeFixture(t)
	d.setApprovalPeer(func(*connState) (bool, string) { return false, "" })
	person := dialVerb(t, sp)
	result(t, callP(person, t, "set-pane-grants", map[string]any{"session": "a", "window": a1, "grants": []string{"read", "write"}}))

	d.setApprovalPeer(func(*connState) (bool, string) { return true, a1 })
	c := dialVerb(t, sp)
	// a2 is on the open default, so it holds admin.
	wantForbidden(t, "queue-prompt into an admin sibling", callP(c, t, "queue-prompt", map[string]any{"window": a2, "text": "hi"}))
	wantForbidden(t, "send-review into an admin sibling", callP(c, t, "send-review", map[string]any{"window": a2}))
	// Its own pane is its own to type into. It runs no agent, so the handler
	// refuses what the grant check let through.
	mustRefuse(t, callP(c, t, "queue-prompt", map[string]any{"window": a1, "text": "hi"}), ErrVerbInvalidParams, "queue-prompt into itself")

	// A sibling that holds no more, on a prompt: respond is needed.
	d.setApprovalPeer(func(*connState) (bool, string) { return false, "" })
	result(t, callP(person, t, "set-pane-grants", map[string]any{"session": "a", "window": a2, "grants": []string{"read"}}))
	d.setApprovalPeer(func(*connState) (bool, string) { return true, a1 })
	mustRefuse(t, callP(c, t, "queue-prompt", map[string]any{"window": a2, "text": "hi"}), ErrVerbInvalidParams, "queue-prompt into a sibling that holds less")
	d.setApprovalPeer(func(*connState) (bool, string) { return false, "" })
	setAgentState(t, person, "a", a2, string(AgentStateNeedsInput), "approval", "run rm -rf build?")
	d.setApprovalPeer(func(*connState) (bool, string) { return true, a1 })
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
	t.Chdir(t.TempDir())
	d, sp := startTestDaemon(t)
	_, a, _ := twoWindowSession(t, d, "work")
	d.SetLinkPolicies(map[string]config.HostConfig{
		"viewer": {Allow: []string{"list"}},
		"opener": {Allow: []string{"list", "open"}},
	})
	viewer := dialLink(t, sp)
	result(t, linkPeer(t, viewer, "viewer"))
	wantReached(t, "compare-fan from a machine that may list", callP(viewer, t, "compare-fan", map[string]any{"session": "work"}))
	result(t, callP(viewer, t, "agent-activity", map[string]any{"session": "work", "window": a}))
	result(t, callP(viewer, t, "list-queued", map[string]any{"session": "work", "window": a}))
	mustRefuse(t, callP(viewer, t, "get-approval", map[string]any{"request_id": "9f86d081884c7d65"}), ErrVerbInvalidParams, "get-approval from a machine that may list")
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
	wantReached(t, "review-diff with the default policy", callP(anon, t, "review-diff", map[string]any{"session": "work", "window": a}))
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

	// The risk, plan and deny reason fields are built and tested in
	// approval_risk_test.go. The kind is still checked here.
	setAgentState(t, c, "work", a, "needs_input", "approval", testHeldLine)
	mustRefuse(t, callP(c, t, "request-approval", map[string]any{"session": "work", "window": a, "harness": "claude", "summary": testHeldLine, "kind": "poem"}),
		ErrVerbInvalidParams, "an unknown kind")
	if items, _ := listAttention(t, c, ""); len(items) != 1 || items[0]["request_id"] != nil {
		t.Fatalf("a refused request held something: %v", items)
	}

	// A reply without the new fields is answered as before.
	pending, _ := requestApproval(t, sp, "work", a, "once", "deny")
	id := heldItem(t, c, a)["request_id"].(string)
	res := result(t, reply(c, t, id, ApprovalOnce, tui.HumanNonce()))
	if res["applied"] != true {
		t.Fatalf("a plain reply answered %v", res)
	}
	awaitResult(t, pending)

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

// TestHostAgentQueuedIsClamped holds a linked host's queue count to what a
// queue of this build can hold: a host is untrusted, and the count is drawn
// on the rail.
func TestHostAgentQueuedIsClamped(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   int
		want int
	}{
		{"negative", -5, 0},
		{"zero", 0, 0},
		{"inside", 3, 3},
		{"at the cap", config.MaxQueueMax, config.MaxQueueMax},
		{"over the cap", 1 << 30, config.MaxQueueMax},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(map[string]any{
				"session": "work",
				"agents":  []map[string]any{{"window_id": "w1", "name": "a", "state": "working", "queued": tc.in}},
			})
			if err != nil {
				t.Fatal(err)
			}
			rows, err := decodeHostAgents(raw, "")
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != 1 || rows[0].Queued != tc.want {
				t.Fatalf("queued %d read as %+v, want %d", tc.in, rows, tc.want)
			}
		})
	}
}
