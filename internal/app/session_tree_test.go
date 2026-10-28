package app

import (
	"slices"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/worktree"
)

// TestBuildSessionTreeSkipsNilWindows guards against a nil entry in m.Windows
// (a torn-down window mid-close) crashing the tree build instead of just being
// skipped, mirroring currentSessionInput's nil check.
func TestBuildSessionTreeSkipsNilWindows(t *testing.T) {
	win := &terminal.Window{ID: "w1"}
	m := &OS{Settings: config.Global, Windows: []*terminal.Window{win, nil}, FocusedWindow: 0}

	tree := m.BuildSessionTree()
	if len(tree.Sessions[0].Children) != 1 {
		t.Fatalf("Children = %d, want 1 (nil window skipped)", len(tree.Sessions[0].Children))
	}
}

// issue1299Sessions is the listing from #1299, in the order the sessions were
// made: api (the main checkout), api-feat-a (a worktree), notes (a plain
// folder), api-feat-b (a worktree). The rail draws it as api, api-feat-a,
// api-feat-b, notes. current is the session that is attached.
func issue1299Sessions(current string) []sessiontree.Node {
	api := &sessiontree.WorktreeRef{Repo: "api", Branch: "main", Main: true}
	inputs := []sessiontree.SessionInput{
		{Name: "api", Worktree: api},
		{Name: "api-feat-a", Worktree: &sessiontree.WorktreeRef{Repo: "api", Branch: "feat-a"}},
		{Name: "notes"},
		{Name: "api-feat-b", Worktree: &sessiontree.WorktreeRef{Repo: "api", Branch: "feat-b"}},
	}
	for i := range inputs {
		inputs[i].IsCurrent = inputs[i].Name == current
	}
	return sessiontree.Build(inputs).Sessions
}

func sessionIDs(nodes []sessiontree.Node) []string {
	ids := make([]string, 0, len(nodes))
	for _, n := range nodes {
		ids = append(ids, n.ID)
	}
	return ids
}

// TestRailSessionOrderFollowsTheWorktreeGroups is #1299: next_session,
// prev_session and switch_session_N walk the sessions in the order the rail
// draws them, with a repository's worktree sessions under it, not in the
// order the sessions were made.
func TestRailSessionOrderFollowsTheWorktreeGroups(t *testing.T) {
	got := sessionIDs(railSessionOrder(issue1299Sessions("api-feat-a")))
	want := []string{"api", "api-feat-a", "api-feat-b", "notes"}
	if !slices.Equal(got, want) {
		t.Fatalf("rail session order = %v, want %v", got, want)
	}

	sessions := railSessionOrder(issue1299Sessions("api-feat-a"))
	if next := railNeighbourIn(sessions, 1); next != "api-feat-b" {
		t.Errorf("next from api-feat-a = %q, want api-feat-b", next)
	}
	if prev := railNeighbourIn(sessions, -1); prev != "api" {
		t.Errorf("prev from api-feat-a = %q, want api", prev)
	}
	sessions = railSessionOrder(issue1299Sessions("notes"))
	if next := railNeighbourIn(sessions, 1); next != "api" {
		t.Errorf("next from notes = %q, want api (wrapped)", next)
	}
	if prev := railNeighbourIn(sessions, -1); prev != "api-feat-b" {
		t.Errorf("prev from notes = %q, want api-feat-b", prev)
	}
}

// TestRailSessionOrderUngroupedKeepsTheListOrder is the positive half: with no
// worktree record and no space the rail draws the sessions in the list's own
// order, and cycling keeps to it.
func TestRailSessionOrderUngroupedKeepsTheListOrder(t *testing.T) {
	nodes := sessiontree.Build([]sessiontree.SessionInput{
		{Name: "b"}, {Name: "a", IsCurrent: true}, {Name: "c"},
	}).Sessions
	sessions := railSessionOrder(nodes)
	if got, want := sessionIDs(sessions), []string{"b", "a", "c"}; !slices.Equal(got, want) {
		t.Fatalf("rail session order = %v, want %v", got, want)
	}
	if next := railNeighbourIn(sessions, 1); next != "c" {
		t.Errorf("next from a = %q, want c", next)
	}
}

// TestRailSessionOrderFollowsTheSpaces is the space half of the grouping: a
// session filed under a space is drawn under the space's heading, before the
// sessions in no space, and the cycle follows it there. The heading itself is
// not a session and is never a target.
func TestRailSessionOrderFollowsTheSpaces(t *testing.T) {
	nodes := sessiontree.Build([]sessiontree.SessionInput{
		{Name: "loose", IsCurrent: true},
		{Name: "filed", Space: "work"},
	}).Sessions
	sessions := railSessionOrder(nodes)
	if got, want := sessionIDs(sessions), []string{"filed", "loose"}; !slices.Equal(got, want) {
		t.Fatalf("rail session order = %v, want %v", got, want)
	}
	if next := railNeighbourIn(sessions, 1); next != "filed" {
		t.Errorf("next from loose = %q, want filed", next)
	}
}

// TestCycleSessionFollowsTheWorktreeGroups is #1299 through the client: with
// the daemon's listing in the order the sessions were made, the next session
// from api-feat-a is api-feat-b, which the rail draws below it, not notes, and
// switch_session_N numbers the sessions in the order the rail draws them.
func TestCycleSessionFollowsTheWorktreeGroups(t *testing.T) {
	wt := func(branch string) *session.WorktreeInfo {
		return &session.WorktreeInfo{Info: worktree.Info{Repo: "api", Branch: branch}}
	}
	client := session.NewTUIClient()
	client.UpdateSessionCache([]session.SessionInfo{
		{Name: "api"},
		{Name: "api-feat-a", Worktree: wt("feat-a")},
		{Name: "notes"},
		{Name: "api-feat-b", Worktree: wt("feat-b")},
	})
	m := &OS{
		Settings:        config.Global,
		SessionName:     "api-feat-a",
		SessionWorktree: wt("feat-a"),
		DaemonClient:    client,
	}
	if next := m.railNeighbourSession(1); next != "api-feat-b" {
		t.Errorf("next from api-feat-a = %q, want api-feat-b", next)
	}
	if prev := m.railNeighbourSession(-1); prev != "api" {
		t.Errorf("prev from api-feat-a = %q, want api", prev)
	}
	for n, want := range []string{"api", "api-feat-a", "api-feat-b", "notes"} {
		if got, ok := m.railSessionAt(n); !ok || got.ID != want {
			t.Errorf("switch_session_%d = %q (ok %v), want %s", n+1, got.ID, ok, want)
		}
	}
	if got, ok := m.railSessionAt(4); ok {
		t.Errorf("switch_session_5 = %q, want no session", got.ID)
	}
}
