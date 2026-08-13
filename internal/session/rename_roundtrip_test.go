package session

import (
	"testing"
)

// TestSpacedAndNonASCIINamesSurviveTheVerbAndTheStateFile is the persistence
// half of letting a user type them: the rename editor can now produce a name
// with a space or a multi-byte rune, and it only counts as a name if it comes
// back off the socket and off disk exactly as it went down.
func TestSpacedAndNonASCIINamesSurviveTheVerbAndTheStateFile(t *testing.T) {
	t.Cleanup(useResurrectionDir(t.TempDir()))

	const label = "Payments API 日本語"
	const wsName = "café review"

	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "work")
	c := dialVerb(t, sp)

	res := result(t, c.call(t, `{"id":1,"verb":"set-session-name","params":{"session":"work","name":"`+label+`"}}`))
	if res["display_name"] != label {
		t.Fatalf("set-session-name returned %v, want %q", res["display_name"], label)
	}
	res = result(t, c.call(t, `{"id":2,"verb":"set-workspace-name","params":{"session":"work","workspace":2,"name":"`+wsName+`"}}`))
	if res == nil {
		t.Fatal("set-workspace-name returned no result")
	}

	info := result(t, c.call(t, `{"id":3,"verb":"session-info","params":{"session":"work"}}`))
	if info["display_name"] != label {
		t.Fatalf("session-info display_name = %v, want %q", info["display_name"], label)
	}
	if info["session_name"] != "work" {
		t.Fatalf("the identity moved: session_name = %v", info["session_name"])
	}

	state := sess.GetState()
	if state.WorkspaceNames[2] != wsName {
		t.Fatalf("workspace name in state = %q, want %q", state.WorkspaceNames[2], wsName)
	}

	state.Name = "work"
	if len(state.Windows) == 0 {
		t.Fatal("test session has no window to name")
	}
	state.Windows[0].CustomName = "über tests"
	if err := SaveSessionForResurrection(state); err != nil {
		t.Fatalf("SaveSessionForResurrection: %v", err)
	}
	loaded, err := LoadResurrectionState("work")
	if err != nil {
		t.Fatalf("LoadResurrectionState: %v", err)
	}
	if loaded.DisplayName != label {
		t.Errorf("display name came back %q, want %q", loaded.DisplayName, label)
	}
	if loaded.WorkspaceNames[2] != wsName {
		t.Errorf("workspace name came back %q, want %q", loaded.WorkspaceNames[2], wsName)
	}
	if len(loaded.Windows) == 0 || loaded.Windows[0].CustomName != "über tests" {
		t.Errorf("window name came back %+v, want über tests", loaded.Windows)
	}
}
