package session

import "testing"

// TestAVerifyResultLiftsTheErroredBlock: a card in verify, held by a pane
// dispatch gave it to, is blocked when that pane's agent errors. The verify
// result lets the pane go, and the block goes with it: the block belongs to
// the holder. Before, the card kept the block with no holder, and no pane
// could lift it.
//
// The e2e suite cannot reach this card: dispatch gives a pane an open card,
// so a dispatched pane holds a card in verify only after a hand move under
// a running verify. So this test drives TaskState.apply. The Lean model
// proves the rule for every card (letting_go_lifts_errored_block in
// spec/tasks).
func TestAVerifyResultLiftsTheErroredBlock(t *testing.T) {
	tk := TaskState{ID: "t1", Stage: TaskStageVerify, Holder: "w1", HeldAt: 3, Dispatched: true,
		Blocked: &TaskBlock{Reason: taskBlockErrored + ": exit 1", By: taskByDaemon, At: 4},
		Verify:  &TaskVerify{State: VerifyRunning, StartedAt: 5}}
	m := cardMove{kind: moveVerified, by: taskByDaemon, stage: TaskStageReview, token: 5,
		verify: &TaskVerify{State: VerifyPassed, StartedAt: 5, FinishedAt: 9}, reason: "verify passed"}
	if action, _, verr := tk.apply(m, 9); action != TaskActionVerified || verr != nil {
		t.Fatalf("the verify result gave action %q and refusal %v, want %q", action, verr, TaskActionVerified)
	}
	if tk.Holder != "" || tk.Blocked != nil || tk.Stage != TaskStageReview {
		t.Fatalf("after the verify result the card is in %s, held by %q, blocked %+v; want review, no holder, no block", tk.Stage, tk.Holder, tk.Blocked)
	}
	if broken := cardRulesBroken(&tk, m); len(broken) > 0 {
		t.Fatalf("the card breaks its rules: %q", broken)
	}
}

// TestAClaimNamesAPane: a claim with no window is refused and changes
// nothing, also as a refresh of the names of a card no pane holds. Before,
// that refresh wrote holder names on a card with no holder.
//
// Every verb names the claiming pane before it asks for the move, so the e2e
// suite cannot send such a claim. The Lean model proves the rule for every
// card (claim_names_a_pane and holderClean_preserved in spec/tasks).
func TestAClaimNamesAPane(t *testing.T) {
	tk := TaskState{ID: "t1", Stage: TaskStageTodo}
	m := cardMove{kind: moveClaim, by: "far", window: "", refresh: true, names: true, name: "far", harness: "codex"}
	action, _, verr := tk.apply(m, 9)
	if action != "" || verr == nil || verr.Code != ErrVerbInvalidParams || verr.Hint == nil || verr.Hint.Param != "window" {
		t.Fatalf("a claim with no window gave action %q and refusal %+v, want an invalid_params refusal on window", action, verr)
	}
	if tk.Holder != "" || tk.HolderName != "" || tk.HolderHarness != "" || tk.Stage != TaskStageTodo || len(tk.History) != 0 {
		t.Fatalf("the refused claim changed the card: %+v", tk)
	}
}
