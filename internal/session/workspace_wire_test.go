package session

import (
	"encoding/json"
	"strings"
	"testing"
)

// The two workspace fields are additive, so the only thing worth pinning is
// that they survive a round trip both ways: a newer peer's payload must not
// break an older reader, and an older peer's payload must read back as
// "unknown" rather than as workspace zero meaning something.

// TestWorkspaceFieldsRoundTrip: a listing built by a daemon that knows about
// workspaces reaches a client that does too, intact.
func TestWorkspaceFieldsRoundTrip(t *testing.T) {
	in := SessionInfo{
		Name:             "work",
		CurrentWorkspace: 2,
		Windows: []WindowSummary{
			{ID: "w1", Title: "nvim", Workspace: 1},
			{ID: "w2", Title: "build", Workspace: 2},
		},
	}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out SessionInfo
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.CurrentWorkspace != 2 {
		t.Errorf("CurrentWorkspace = %d, want 2", out.CurrentWorkspace)
	}
	if len(out.Windows) != 2 || out.Windows[0].Workspace != 1 || out.Windows[1].Workspace != 2 {
		t.Errorf("window workspaces did not survive the trip: %+v", out.Windows)
	}
}

// TestOlderPayloadReadsAsUnknown: a daemon that predates the fields sends
// neither, and the client has to read that as "I do not know" rather than as
// workspace zero.
func TestOlderPayloadReadsAsUnknown(t *testing.T) {
	older := `{"name":"work","id":"x","window_count":1,` +
		`"windows":[{"id":"w1","title":"nvim"}]}`
	var out SessionInfo
	if err := json.Unmarshal([]byte(older), &out); err != nil {
		t.Fatalf("an older listing failed to parse: %v", err)
	}
	if out.CurrentWorkspace != 0 {
		t.Errorf("CurrentWorkspace = %d, want 0 for a payload that never mentioned it", out.CurrentWorkspace)
	}
	if len(out.Windows) != 1 || out.Windows[0].Workspace != 0 {
		t.Errorf("a window with no workspace read back as %+v", out.Windows)
	}
	// Everything the older payload did carry is untouched.
	if out.Name != "work" || out.Windows[0].Title != "nvim" {
		t.Errorf("the older fields were disturbed: %+v", out)
	}
}

// TestNewerPayloadIsIgnorableByAnOlderReader stands in for the other direction:
// the fields are omitted when zero and named, so a reader that does not know
// them drops them and keeps the rest. Modelled with a struct that lacks them.
func TestNewerPayloadIsIgnorableByAnOlderReader(t *testing.T) {
	data, err := json.Marshal(SessionInfo{
		Name:             "work",
		CurrentWorkspace: 3,
		Windows:          []WindowSummary{{ID: "w1", Title: "nvim", Workspace: 3}},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// The shape an older build compiled against.
	var older struct {
		Name    string `json:"name"`
		Windows []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"windows"`
	}
	if err := json.Unmarshal(data, &older); err != nil {
		t.Fatalf("an older reader could not parse a newer listing: %v", err)
	}
	if older.Name != "work" || len(older.Windows) != 1 || older.Windows[0].Title != "nvim" {
		t.Errorf("an older reader lost the fields it does know: %+v", older)
	}
}

// TestZeroWorkspaceIsOmitted keeps the wire quiet for the case that carries no
// information, which is what makes the skew above free.
func TestZeroWorkspaceIsOmitted(t *testing.T) {
	data, err := json.Marshal(SessionInfo{Name: "work", Windows: []WindowSummary{{ID: "w1", Title: "nvim"}}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, key := range []string{"current_workspace", "workspace"} {
		if got := string(data); strings.Contains(got, key) {
			t.Errorf("a listing with no workspace data still sent %q: %s", key, got)
		}
	}
}
