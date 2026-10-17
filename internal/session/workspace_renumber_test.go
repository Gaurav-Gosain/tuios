package session

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestSavedDisplayOrderRestoresAsNumbering is the state file of a build that
// arranged the dock's pills as a display order. The order is read from the
// file, applied once as a renumber when the session is restored, and not kept:
// the workspaces come back numbered the way their pills were shown, with their
// panes and names, and the next save carries no order.
func TestSavedDisplayOrderRestoresAsNumbering(t *testing.T) {
	d, _ := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "legacy")
	st := sess.ResurrectionState()
	st.Windows[0].Workspace = 3
	st.CurrentWorkspace = 3
	st.WorkspaceNames = map[int]string{1: "EDIT", 3: "DPLY"}
	st.WorkspaceOrder = []int{3, 1}
	raw, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"workspace_order":[3,1]`) {
		t.Fatalf("the fixture does not carry the old field:\n%s", raw)
	}
	var saved SessionState
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if err := d.manager.DeleteSession("legacy"); err != nil {
		t.Fatal(err)
	}

	back, err := d.restoreSession(&saved)
	if err != nil {
		t.Fatal(err)
	}
	got := back.GetState()
	if got.WorkspaceOrder != nil {
		t.Errorf("the restored session kept the display order %v", got.WorkspaceOrder)
	}
	if got.WorkspaceNames[1] != "DPLY" || got.WorkspaceNames[2] != "EDIT" {
		t.Errorf("names are %v, want DPLY on 1 and EDIT on 2, the order the pills were shown in", got.WorkspaceNames)
	}
	if len(got.Windows) != 1 || got.Windows[0].Workspace != 1 || got.CurrentWorkspace != 1 {
		t.Errorf("the pane is on workspace %d and the session shows %d, want both on 1", got.Windows[0].Workspace, got.CurrentWorkspace)
	}
	// The tab that was workspace 3 keeps its herdr tab id, now as workspace 1.
	if got.WorkspaceTabKey(1) != 3 {
		t.Errorf("workspace 1 has tab key %d, want 3, the number its tab id was made from", got.WorkspaceTabKey(1))
	}
}
