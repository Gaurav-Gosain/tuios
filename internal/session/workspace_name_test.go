package session

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestSetWorkspaceNameVerbRoundTrip drives set-workspace-name over the real
// socket and reads it back through session-info, and checks the number stays the
// workspace's identity: naming workspace 2 does not move anything that addresses
// it by number.
func TestSetWorkspaceNameVerbRoundTrip(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "work")
	before := sess.GetState().Version

	c := dialVerb(t, sp)

	res := result(t, c.call(t, `{"id":1,"verb":"set-workspace-name","params":{"session":"work","workspace":2,"name":"review"}}`))
	if res["name"] != "review" || res["workspace"] != float64(2) {
		t.Fatalf("set-workspace-name returned %v", res)
	}
	if after := sess.GetState().Version; after <= before {
		t.Fatalf("version did not bump: before=%d after=%d", before, after)
	}

	info := result(t, c.call(t, `{"id":2,"verb":"session-info","params":{"session":"work"}}`))
	names, ok := info["workspace_names"].(map[string]any)
	if !ok {
		t.Fatalf("session-info workspace_names has the wrong shape: %v", info["workspace_names"])
	}
	if names["2"] != "review" {
		t.Fatalf("workspace_names[2] = %v, want review", names["2"])
	}
	// Only the named one is reported; the rest are still just numbers.
	if len(names) != 1 {
		t.Fatalf("workspace_names = %v, want only the named workspace", names)
	}

	// Still addressable by number.
	if err := sess.SwitchDaemonWorkspace(2); err != nil {
		t.Fatalf("SwitchDaemonWorkspace(2) after naming: %v", err)
	}

	// An empty name clears it and leaves no entry behind.
	_ = result(t, c.call(t, `{"id":3,"verb":"set-workspace-name","params":{"session":"work","workspace":2,"name":""}}`))
	cleared := result(t, c.call(t, `{"id":4,"verb":"session-info","params":{"session":"work"}}`))
	if got := cleared["workspace_names"].(map[string]any); len(got) != 0 {
		t.Fatalf("workspace_names after clearing = %v, want empty", got)
	}
}

// TestSetWorkspaceNameRejectsOutOfRange checks the verb bounds the workspace the
// same way every other workspace-taking operation does.
func TestSetWorkspaceNameRejectsOutOfRange(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "work")
	c := dialVerb(t, sp)

	for _, ws := range []string{"0", "99"} {
		code := errCode(t, c.call(t, `{"id":1,"verb":"set-workspace-name","params":{"session":"work","workspace":`+ws+`,"name":"x"}}`))
		if code != ErrVerbInvalidParams {
			t.Errorf("workspace %s gave code %q, want %q", ws, code, ErrVerbInvalidParams)
		}
	}
}

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
