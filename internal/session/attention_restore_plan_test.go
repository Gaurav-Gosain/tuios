package session

import (
	"fmt"
	"testing"
	"time"
)

// TestRestoredPlanIsThePanesApproval: a held plan the person dismisses ends
// its hold, and the hook gives the prompt back to the pane. Restoring that
// dismiss brings back the pane's approval, as when a hold ends any other way:
// kind approval, with no digest, line count or reason flag, since there is
// no plan to serve and no hold to answer. A restored plan kept as a plan
// would sit under Plans with nothing get-approval can read.
//
// Negative control: with rememberUndoLocked keeping the plan fields, the
// restored item is kind plan with its plan_sha.
func TestRestoredPlanIsThePanesApproval(t *testing.T) {
	d, sp := startTestDaemon(t)
	enableRiskyApprovals(t, d, 30*time.Second, nil)
	_, a, _ := twoWindowSession(t, d, "work")
	makeSessionWithWindow(t, d, "other")
	c := dialVerb(t, sp)
	tui := attachTUI(t, sp, "other")
	nonce := tui.HumanNonce()

	pending, it := holdPlan(t, c, sp, a)
	id := it["id"].(string)
	if it["kind"] != AttentionPlan {
		t.Fatalf("the held plan is %v", it)
	}
	result(t, c.call(t, fmt.Sprintf(`{"id":1,"verb":"dismiss-attention","params":{"id":%q,"human_nonce":%q}}`, id, nonce)))
	if got := awaitResult(t, pending); got["decision"] != "" {
		t.Fatalf("the dismiss answered the hook with %v", got)
	}
	if items, _ := listAttention(t, c, ""); len(items) != 0 {
		t.Fatalf("the dismissed plan is listed: %v", items)
	}

	result(t, callP(c, t, "mark-attention", map[string]any{"id": id, "action": "restore", "human_nonce": nonce}))
	items, _ := listAttention(t, c, "")
	if len(items) != 1 || items[0]["id"] != id {
		t.Fatalf("after restore the Inbox lists %v", items)
	}
	got := items[0]
	if got["kind"] != AttentionApproval || got["request_id"] != nil || got["plan_sha"] != nil ||
		got["plan_lines"] != nil || got["deny_message"] != nil {
		t.Errorf("the restored plan is %v, want the pane's approval with no hold and no plan", got)
	}
}
