package session

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// TestSetSessionNameVerbRoundTrip drives set-session-name and set-session-accent
// over the real socket and reads them back through session-info, and pins the
// point of the whole feature: the label is not the identity, so session_name is
// untouched by a rename.
func TestSetSessionNameVerbRoundTrip(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "work")
	before := sess.GetState().Version

	c := dialVerb(t, sp)

	res := result(t, c.call(t, `{"id":1,"verb":"set-session-name","params":{"session":"work","name":"Payments API"}}`))
	if res["display_name"] != "Payments API" {
		t.Fatalf("set-session-name returned display_name %v, want Payments API", res["display_name"])
	}
	if res["session"] != "work" {
		t.Fatalf("set-session-name returned session %v, want work (the identity must not move)", res["session"])
	}
	if after := sess.GetState().Version; after <= before {
		t.Fatalf("version did not bump: before=%d after=%d", before, after)
	}

	_ = result(t, c.call(t, `{"id":2,"verb":"set-session-accent","params":{"session":"work","accent":"cyan"}}`))

	info := result(t, c.call(t, `{"id":3,"verb":"session-info","params":{"session":"work"}}`))
	if info["display_name"] != "Payments API" {
		t.Fatalf("session-info display_name = %v, want Payments API", info["display_name"])
	}
	if info["accent"] != "cyan" {
		t.Fatalf("session-info accent = %v, want cyan", info["accent"])
	}
	if info["session_name"] != "work" {
		t.Fatalf("session-info session_name = %v, want work", info["session_name"])
	}

	// The session is still addressable by its identity, which is what would break
	// if a rename had written through to Name.
	if d.manager.GetSession("work") == nil {
		t.Fatal("session is no longer reachable by name after a rename")
	}

	// An empty name clears the label.
	_ = result(t, c.call(t, `{"id":4,"verb":"set-session-name","params":{"session":"work","name":""}}`))
	cleared := result(t, c.call(t, `{"id":5,"verb":"session-info","params":{"session":"work"}}`))
	if cleared["display_name"] != "" {
		t.Fatalf("display_name after clearing = %v, want empty", cleared["display_name"])
	}
}

// TestSessionLabelReachesEveryClient checks the two halves of "every client sees
// it": the mutation is announced on the state push (which the daemon fans out to
// every attached client), and a sync from a client that knows nothing about the
// label does not wipe it for the others.
func TestSessionLabelReachesEveryClient(t *testing.T) {
	sess := newTestSession(t)
	pushes := recordStateSink(sess)

	if err := sess.SetDisplayName("Payments API"); err != nil {
		t.Fatalf("SetDisplayName: %v", err)
	}
	if err := sess.SetAccent("cyan"); err != nil {
		t.Fatalf("SetAccent: %v", err)
	}

	got := pushes()
	if len(got) != 2 {
		t.Fatalf("push count = %d, want 2", len(got))
	}
	if got[0].DisplayName != "Payments API" {
		t.Fatalf("pushed display name = %q, want Payments API", got[0].DisplayName)
	}
	if got[1].Accent != "cyan" {
		t.Fatalf("pushed accent = %q, want cyan", got[1].Accent)
	}

	// What a second client pushes: a snapshot with no label at all, which is every
	// client today and every older client after this change.
	incoming := sess.GetState()
	incoming.DisplayName = ""
	incoming.Accent = ""
	sess.UpdateState(incoming)

	after := sess.GetState()
	if after.DisplayName != "Payments API" || after.Accent != "cyan" {
		t.Fatalf("a client sync wiped the label: name=%q accent=%q", after.DisplayName, after.Accent)
	}
}

// TestSessionLabelSurvivesResurrection checks a rename outlives the daemon: the
// label is written with the rest of the session state and comes back on restore.
func TestSessionLabelSurvivesResurrection(t *testing.T) {
	t.Cleanup(useResurrectionDir(t.TempDir()))

	sess := newTestSession(t)
	if err := sess.SetDisplayName("Payments API"); err != nil {
		t.Fatalf("SetDisplayName: %v", err)
	}
	if err := sess.SetAccent("cyan"); err != nil {
		t.Fatalf("SetAccent: %v", err)
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
	if loaded.DisplayName != "Payments API" {
		t.Fatalf("restored display name = %q, want Payments API", loaded.DisplayName)
	}
	if loaded.Accent != "cyan" {
		t.Fatalf("restored accent = %q, want cyan", loaded.Accent)
	}
}

// TestUnnamedSessionStateIsUnchanged is the byte-level compatibility check: a
// session nobody renamed serializes exactly as it did before the label existed,
// so an older client and an older daemon read the same bytes they always did.
func TestUnnamedSessionStateIsUnchanged(t *testing.T) {
	sess := newTestSession(t)
	if _, err := sess.AddDaemonWindow("shell", nil); err != nil {
		t.Fatalf("AddDaemonWindow: %v", err)
	}

	data, err := json.Marshal(sess.GetState())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, key := range []string{"display_name", "accent"} {
		if strings.Contains(string(data), `"`+key+`"`) {
			t.Errorf("unnamed session state carries %q: %s", key, data)
		}
	}
}

// TestPreChangeResurrectionStateLoads proves a state file written before this
// change still loads, with the new fields simply absent.
func TestPreChangeResurrectionStateLoads(t *testing.T) {
	t.Cleanup(useResurrectionDir(t.TempDir()))

	// Verbatim shape of a pre-change state file: no display_name, no accent, no
	// workspace_names, no resurrection_version.
	const legacy = `{
  "name": "work",
  "windows": [{"id": "w1", "title": "shell", "x": 0, "y": 0, "width": 80, "height": 24, "z": 0, "workspace": 1, "pty_id": "p1"}],
  "current_workspace": 1,
  "master_ratio": 0.5,
  "auto_tiling": false,
  "width": 80,
  "height": 24
}`
	if err := os.WriteFile(getResurrectionPath("work"), []byte(legacy), 0o600); err != nil {
		t.Fatalf("write legacy state: %v", err)
	}

	loaded, err := LoadResurrectionState("work")
	if err != nil {
		t.Fatalf("pre-change state failed to load: %v", err)
	}
	if loaded.Name != "work" || len(loaded.Windows) != 1 {
		t.Fatalf("pre-change state loaded wrong: %+v", loaded)
	}
	if loaded.DisplayName != "" || loaded.Accent != "" {
		t.Fatalf("absent label fields did not read as unset: %+v", loaded)
	}
}
