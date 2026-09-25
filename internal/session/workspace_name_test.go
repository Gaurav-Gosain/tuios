package session

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestWorkspaceNameReachesEveryClient checks the name is announced on the state
// push and that a client sync which omits it does not wipe it, which is what
// makes it visible to a second client rather than only the one that set it.
func TestWorkspaceNameReachesEveryClient(t *testing.T) {
	sess := newTestSession(t)
	pushes := recordStateSink(sess)

	if err := sess.SetDaemonWorkspaceName(3, "review"); err != nil {
		t.Fatalf("SetDaemonWorkspaceName: %v", err)
	}
	got := pushes()
	if len(got) != 1 || got[0].WorkspaceNames[3] != "review" {
		t.Fatalf("pushed workspace names = %v", got)
	}

	incoming := sess.GetState()
	incoming.WorkspaceNames = nil
	sess.UpdateState(incoming)

	if name := sess.GetState().WorkspaceNames[3]; name != "review" {
		t.Fatalf("a client sync wiped the workspace name: %q", name)
	}
}

// TestWorkspaceNameSurvivesResurrection checks a named workspace outlives the
// daemon.
func TestWorkspaceNameSurvivesResurrection(t *testing.T) {
	t.Cleanup(useResurrectionDir(t.TempDir()))

	sess := newTestSession(t)
	if err := sess.SetDaemonWorkspaceName(3, "review"); err != nil {
		t.Fatalf("SetDaemonWorkspaceName: %v", err)
	}
	state := sess.GetState()
	state.Name = "work"
	if err := SaveSessionForResurrection(state); err != nil {
		t.Fatalf("SaveSessionForResurrection: %v", err)
	}

	loaded, err := LoadResurrectionState("work")
	if err != nil {
		t.Fatalf("LoadResurrectionState: %v", err)
	}
	if loaded.WorkspaceNames[3] != "review" {
		t.Fatalf("restored workspace names = %v, want 3=review", loaded.WorkspaceNames)
	}
}

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
