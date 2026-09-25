package session

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestUnnamedWorkspaceStateIsUnchanged is the byte-level compatibility check for
// workspaces: naming one workspace and clearing it again must leave the state
// exactly as it started, with no empty map and no key in the serialized form.
func TestUnnamedWorkspaceStateIsUnchanged(t *testing.T) {
	sess := newTestSession(t)
	if _, err := sess.AddDaemonWindow("shell", nil); err != nil {
		t.Fatalf("AddDaemonWindow: %v", err)
	}

	baseline, err := json.Marshal(sess.GetState())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(baseline), `"workspace_names"`) {
		t.Fatalf("an untouched session carries workspace_names: %s", baseline)
	}

	if err := sess.SetDaemonWorkspaceName(2, "review"); err != nil {
		t.Fatalf("SetDaemonWorkspaceName: %v", err)
	}
	if err := sess.SetDaemonWorkspaceName(2, ""); err != nil {
		t.Fatalf("SetDaemonWorkspaceName clear: %v", err)
	}
	after, err := json.Marshal(sess.GetState())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(after), `"workspace_names"`) {
		t.Fatalf("a cleared workspace name left a key behind: %s", after)
	}
}
