package session

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// The workspace order is the one arrangement every client attached to a session
// sees, and it is presentation only: a workspace keeps its number, and the
// number is what the verbs, the keys, the focus map, the trees and each window
// go on addressing. These pin both halves of that.

// TestSetWorkspaceOrderVerbRoundTrip drives the verb over the real socket, reads
// it back through session-info, and checks that rearranging the workspaces moved
// nothing that addresses one by number.
func TestSetWorkspaceOrderVerbRoundTrip(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "work")
	before := sess.GetState().Version

	c := dialVerb(t, sp)

	res := result(t, c.call(t, `{"id":1,"verb":"set-workspace-order","params":{"session":"work","order":[3,1,2]}}`))
	if got := numbers(t, res["workspace_order"]); !slices.Equal(got, []int{3, 1, 2}) {
		t.Fatalf("set-workspace-order returned %v", res["workspace_order"])
	}
	if after := sess.GetState().Version; after <= before {
		t.Fatalf("version did not bump: before=%d after=%d", before, after)
	}

	info := result(t, c.call(t, `{"id":2,"verb":"session-info","params":{"session":"work"}}`))
	if got := numbers(t, info["workspace_order"]); !slices.Equal(got, []int{3, 1, 2}) {
		t.Fatalf("session-info reports order %v, want [3 1 2]", info["workspace_order"])
	}

	// The identity is untouched: workspace 2 is still workspace 2, whatever
	// position it now shows in.
	if err := sess.SwitchDaemonWorkspace(2); err != nil {
		t.Fatalf("SwitchDaemonWorkspace(2) after rearranging: %v", err)
	}
	if got := sess.GetState().CurrentWorkspace; got != 2 {
		t.Fatalf("switching to workspace 2 landed on %d", got)
	}
}

// TestAnAscendingOrderLeavesNoTrace: a session that has never been rearranged,
// and one dragged back into its original arrangement, are the same session, so
// neither may carry an order in serialized state.
func TestAnAscendingOrderLeavesNoTrace(t *testing.T) {
	d, _ := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "work")

	if err := sess.SetDaemonWorkspaceOrder([]int{1, 2, 3}); err != nil {
		t.Fatalf("SetDaemonWorkspaceOrder: %v", err)
	}
	if got := sess.GetState().WorkspaceOrder; got != nil {
		t.Errorf("an ascending order was stored as %v, want nothing", got)
	}

	raw, err := json.Marshal(sess.GetState())
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	if strings.Contains(string(raw), "workspace_order") {
		t.Errorf("serialized state carries a workspace_order it does not need:\n%s", raw)
	}
}

// TestAnOrderIsSanitisedRatherThanTrusted: the order arrives from a drag over a
// list the client rendered, and the daemon's own list may since have changed. An
// entry naming a workspace this session does not have, or one already placed,
// cannot mean anything, so it is dropped instead of stored.
func TestAnOrderIsSanitisedRatherThanTrusted(t *testing.T) {
	d, _ := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "work")

	if err := sess.SetDaemonWorkspaceOrder([]int{3, 0, 1, 99, 3, 2, -4}); err != nil {
		t.Fatalf("SetDaemonWorkspaceOrder: %v", err)
	}
	if got := sess.GetState().WorkspaceOrder; !slices.Equal(got, []int{3, 1, 2}) {
		t.Errorf("stored order is %v, want the in-range entries once each", got)
	}

	// A list where nothing at all is in range is a caller error rather than a
	// request to clear the arrangement, and says so.
	if err := sess.SetDaemonWorkspaceOrder([]int{0, 99}); err == nil {
		t.Error("an order with no workspace in range was accepted")
	}
}

// TestAClientSyncDoesNotFlattenTheOrder is the WorkspaceNames rule applied to
// the order: an ordinary state sync omits it, so a client that has never
// rearranged anything must not wipe the arrangement every other client is
// looking at by syncing.
func TestAClientSyncDoesNotFlattenTheOrder(t *testing.T) {
	canonical := &SessionState{Name: "work", WorkspaceOrder: []int{3, 1, 2}}
	incoming := &SessionState{Name: "work"}

	retainDaemonExclusive(incoming, canonical)

	if !slices.Equal(incoming.WorkspaceOrder, []int{3, 1, 2}) {
		t.Errorf("a sync that omitted the order left %v, want the canonical [3 1 2]", incoming.WorkspaceOrder)
	}
}

// numbers reads a JSON array of workspace numbers back as ints.
func numbers(t *testing.T, v any) []int {
	t.Helper()
	raw, ok := v.([]any)
	if !ok {
		t.Fatalf("workspace order has the wrong shape: %v", v)
	}
	out := make([]int, 0, len(raw))
	for _, n := range raw {
		f, ok := n.(float64)
		if !ok {
			t.Fatalf("workspace order holds a non-number: %v", n)
		}
		out = append(out, int(f))
	}
	return out
}
