package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// TestBuildSessionTreeStandaloneRollsUpAndMarksFocus builds an OS with no
// daemon client (the standalone path) and three windows, and checks the tree
// it returns: one synthetic session holding all three windows, only the
// focused window marked current, and the session's rolled-up agent state
// matching sessiontree.RollUpState over the windows' raw states.
func TestBuildSessionTreeStandaloneRollsUpAndMarksFocus(t *testing.T) {
	working := &terminal.Window{ID: "w1", CustomName: "build", AgentState: "working"}
	idle := &terminal.Window{ID: "w2", AgentState: "idle"}
	idle.SetTitle("live-title")
	blank := &terminal.Window{ID: "w3"}

	m := &OS{
		Windows:       []*terminal.Window{working, idle, blank},
		FocusedWindow: 2,
	}

	tree := m.BuildSessionTree()

	if len(tree.Sessions) != 1 {
		t.Fatalf("Sessions = %d, want 1 (standalone mode has no daemon client)", len(tree.Sessions))
	}
	session := tree.Sessions[0]
	if session.Kind != sessiontree.KindSession {
		t.Fatalf("root node Kind = %v, want KindSession", session.Kind)
	}
	if !session.IsCurrent || !session.Attached {
		t.Fatalf("standalone session must be current and attached: %+v", session)
	}
	if len(session.Children) != 3 {
		t.Fatalf("Children = %d, want 3", len(session.Children))
	}

	wantTitles := map[string]string{
		"w1": "build",      // CustomName wins
		"w2": "live-title", // falls back to Title()
		"w3": "shell",      // falls back to the "shell" default
	}
	wantStates := map[string]string{"w1": "working", "w2": "idle", "w3": ""}

	current := 0
	for _, c := range session.Children {
		if c.Kind != sessiontree.KindWindow {
			t.Fatalf("child %q Kind = %v, want KindWindow", c.ID, c.Kind)
		}
		if want := wantTitles[c.ID]; c.Title != want {
			t.Errorf("window %s Title = %q, want %q", c.ID, c.Title, want)
		}
		if want := wantStates[c.ID]; c.AgentState != want {
			t.Errorf("window %s AgentState = %q, want %q", c.ID, c.AgentState, want)
		}
		if c.IsCurrent {
			current++
			if c.ID != "w3" {
				t.Errorf("wrong window marked current: %s, want w3 (index %d)", c.ID, m.FocusedWindow)
			}
		}
	}
	if current != 1 {
		t.Fatalf("windows marked IsCurrent = %d, want exactly 1 (the focused window)", current)
	}

	wantRollUp := sessiontree.RollUpState([]string{"working", "idle", ""})
	if session.AgentState != wantRollUp {
		t.Errorf("session AgentState = %q, want %q (sessiontree.RollUpState of the windows)", session.AgentState, wantRollUp)
	}
	if session.AgentState != "working" {
		t.Errorf("session AgentState = %q, want %q ('working' outranks 'idle')", session.AgentState, "working")
	}
}

// TestBuildSessionTreeStandaloneSessionName checks the synthetic session's
// name: "local" when SessionName is empty (the ordinary standalone case), and
// the client's own SessionName when one happens to be set with no daemon
// attached.
func TestBuildSessionTreeStandaloneSessionName(t *testing.T) {
	win := &terminal.Window{ID: "w1"}

	m := &OS{Windows: []*terminal.Window{win}, FocusedWindow: 0}
	tree := m.BuildSessionTree()
	if got := tree.Sessions[0].ID; got != "local" {
		t.Errorf("session name with empty SessionName = %q, want %q", got, "local")
	}

	m2 := &OS{Windows: []*terminal.Window{win}, FocusedWindow: 0, SessionName: "named"}
	tree2 := m2.BuildSessionTree()
	if got := tree2.Sessions[0].ID; got != "named" {
		t.Errorf("session name with SessionName set = %q, want %q", got, "named")
	}
}

// TestBuildSessionTreeForeignSessionExpands checks that a non-attached session
// whose window summaries are in the client cache produces expandable Children,
// rolls up their agent state, and is not marked current. This is what lets the
// sidebar expand any session, not just the attached one.
func TestBuildSessionTreeForeignSessionExpands(t *testing.T) {
	client := session.NewTUIClient()
	client.UpdateSessionCache([]session.SessionInfo{
		{Name: "attached"},
		{Name: "other", Windows: []session.WindowSummary{
			{ID: "ow1", Title: "vim", AgentState: "working"},
			{ID: "ow2", Title: "shell"},
		}},
	})

	m := &OS{
		Windows:       []*terminal.Window{{ID: "aw1"}},
		FocusedWindow: 0,
		SessionName:   "attached",
		DaemonClient:  client,
	}

	tree := m.BuildSessionTree()

	var other *sessiontree.Node
	for i := range tree.Sessions {
		if tree.Sessions[i].ID == "other" {
			other = &tree.Sessions[i]
		}
	}
	if other == nil {
		t.Fatalf("session %q missing from tree %+v", "other", tree.Sessions)
	}
	if other.IsCurrent {
		t.Errorf("foreign session must not be marked current")
	}
	if len(other.Children) != 2 {
		t.Fatalf("foreign session Children = %d, want 2 (expandable)", len(other.Children))
	}
	wantTitles := map[string]string{"ow1": "vim", "ow2": "shell"}
	for _, c := range other.Children {
		if c.Kind != sessiontree.KindWindow {
			t.Errorf("child %q Kind = %v, want KindWindow", c.ID, c.Kind)
		}
		if want := wantTitles[c.ID]; c.Title != want {
			t.Errorf("window %s Title = %q, want %q", c.ID, c.Title, want)
		}
	}
	if want := sessiontree.RollUpState([]string{"working", ""}); other.AgentState != want {
		t.Errorf("foreign session AgentState = %q, want %q", other.AgentState, want)
	}
}

// TestBuildSessionTreeSkipsNilWindows guards against a nil entry in m.Windows
// (a torn-down window mid-close) crashing the tree build instead of just being
// skipped, mirroring currentSessionInput's nil check.
func TestBuildSessionTreeSkipsNilWindows(t *testing.T) {
	win := &terminal.Window{ID: "w1"}
	m := &OS{Windows: []*terminal.Window{win, nil}, FocusedWindow: 0}

	tree := m.BuildSessionTree()
	if len(tree.Sessions[0].Children) != 1 {
		t.Fatalf("Children = %d, want 1 (nil window skipped)", len(tree.Sessions[0].Children))
	}
}
