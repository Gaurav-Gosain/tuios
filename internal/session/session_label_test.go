package session

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

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
