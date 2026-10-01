package session

import (
	"slices"
	"testing"
)

// close-workspace closes every pane on its workspace and nothing else: the
// panes of another workspace and a scratch pane stay. A scratch workspace
// named as the target is closed.
func TestCloseWorkspaceClosesOnlyItsWorkspace(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "w")
	keep := sess.GetState().Windows[0].ID
	var two []string
	for range 2 {
		w, err := sess.AddDaemonWindow("two", nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := sess.MoveDaemonWindowToWorkspace(w.ID, 2); err != nil {
			t.Fatal(err)
		}
		two = append(two, w.ID)
	}
	if err := sess.mutateState(func(st *SessionState) error {
		for i := range st.Windows {
			if st.Windows[i].ID == two[1] {
				st.Windows[i].Scratch = true
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	c := dialVerb(t, sp)
	got := result(t, callP(c, t, "close-workspace", map[string]any{"session": "w", "workspace": 2}))
	if closed, _ := got["closed"].([]any); len(closed) != 1 || closed[0] != two[0] {
		t.Fatalf("closed = %v, want only %s", got["closed"], two[0])
	}
	var left []string
	for _, w := range sess.GetState().Windows {
		left = append(left, w.ID)
	}
	if !slices.Contains(left, keep) || !slices.Contains(left, two[1]) || slices.Contains(left, two[0]) {
		t.Fatalf("windows left = %v", left)
	}

	// A scratch workspace is closed when it is the target.
	if got := WindowsToCloseOnWorkspace([]WindowState{{ID: "s", Workspace: ScratchWorkspaceBase, Scratch: true}}, ScratchWorkspaceBase); len(got) != 1 {
		t.Fatalf("a targeted scratch workspace closes %v", got)
	}
	resp := callP(c, t, "close-workspace", map[string]any{"session": "w", "workspace": 42})
	if code := errCode(t, resp); code != ErrVerbInvalidParams {
		t.Fatalf("workspace 42 answered %s", code)
	}
}

// A pane needs the admin grant to close a workspace.
func TestCloseWorkspaceNeedsAdmin(t *testing.T) {
	d, sp, a1, _, _ := scopeFixture(t)
	setStrict(d)
	d.setApprovalPeer(func(*connState) (bool, string) { return true, a1 })
	c := dialVerb(t, sp)
	wantForbidden(t, "close-workspace from a pane without admin", callP(c, t, "close-workspace", map[string]any{"session": "a"}))
	if n := len(d.manager.GetSession("a").GetState().Windows); n != 2 {
		t.Fatalf("a refused close-workspace left %d windows, want 2", n)
	}
}
