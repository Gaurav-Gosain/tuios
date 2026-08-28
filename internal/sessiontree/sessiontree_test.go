package sessiontree

import (
	"strings"
	"testing"
)

func TestRollUpStatePriority(t *testing.T) {
	cases := []struct {
		name   string
		states []string
		want   string
	}{
		{"empty", nil, ""},
		{"all none", []string{"", "", ""}, ""},
		{"idle over none", []string{"", "idle"}, "idle"},
		{"unseen done over working and idle", []string{"idle", "done", "working"}, "done"},
		{"working over idle", []string{"idle", "working"}, "working"},
		{"needs_input over working", []string{"working", "needs_input", "done"}, "needs_input"},
		{"errored wins over everything", []string{"working", "needs_input", "errored", "done"}, "errored"},
		{"done over idle", []string{"idle", "done"}, "done"},
		{"unknown treated as none", []string{"bogus", ""}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := RollUpState(c.states); got != c.want {
				t.Fatalf("RollUpState(%v) = %q, want %q", c.states, got, c.want)
			}
		})
	}
}

func TestBuildSessionWithWindowsRollsUpAndCounts(t *testing.T) {
	s := SessionInput{
		Name:      "work",
		Attached:  true,
		IsCurrent: true,
		Windows: []WindowInput{
			{ID: "w1", Title: "build", AgentState: "working", Focused: false},
			{ID: "w2", Title: "logs", AgentState: "needs_input", Focused: true},
			{ID: "w3", Title: "edit", AgentState: "", Focused: false},
		},
	}
	node := BuildSession(s)

	if node.Kind != KindSession || node.ID != "work" || node.Title != "work" {
		t.Fatalf("session identity wrong: %+v", node)
	}
	if !node.Attached || !node.IsCurrent {
		t.Fatalf("attached/current flags lost: %+v", node)
	}
	if node.WindowCount != 3 {
		t.Fatalf("WindowCount = %d, want 3", node.WindowCount)
	}
	if node.AgentState != "needs_input" {
		t.Fatalf("rolled-up state = %q, want needs_input", node.AgentState)
	}
	if len(node.Children) != 3 {
		t.Fatalf("children = %d, want 3", len(node.Children))
	}
	// The focused window must be the only current one.
	current := 0
	for _, c := range node.Children {
		if c.Kind != KindWindow {
			t.Fatalf("child not a window: %+v", c)
		}
		if c.IsCurrent {
			current++
			if c.ID != "w2" {
				t.Fatalf("wrong current window: %s", c.ID)
			}
		}
	}
	if current != 1 {
		t.Fatalf("current windows = %d, want 1", current)
	}
}

func TestBuildSessionCoarseKeepsCountAndNoChildren(t *testing.T) {
	// A non-attached session with no per-window detail: keep the count, no
	// children, no rolled-up glyph (nothing to roll up yet).
	node := BuildSession(SessionInput{Name: "other", WindowCount: 4})
	if node.WindowCount != 4 {
		t.Fatalf("WindowCount = %d, want 4", node.WindowCount)
	}
	if node.Children != nil {
		t.Fatalf("expected nil children for coarse session, got %d", len(node.Children))
	}
	if node.AgentState != "" {
		t.Fatalf("coarse session should have no rolled-up state, got %q", node.AgentState)
	}
}

func TestBuildPreservesOrder(t *testing.T) {
	tree := Build([]SessionInput{
		{Name: "a", WindowCount: 1},
		{Name: "b", WindowCount: 2},
		{Name: "c", WindowCount: 3},
	})
	var got strings.Builder
	for _, s := range tree.Sessions {
		got.WriteString(s.ID)
	}
	if got.String() != "abc" {
		t.Fatalf("session order = %q, want abc", got.String())
	}
}

// TestBuildSessionDisambiguatesRows is the tree's half of the promise that no
// two rows in one session ever read the same: five bare shells in one directory
// resolve to one label, and a list of identical rows names nothing.
func TestBuildSessionDisambiguatesRows(t *testing.T) {
	windows := make([]WindowInput, 0, 5)
	for i := range 5 {
		windows = append(windows, WindowInput{ID: string(rune('a' + i)), Title: "tuios"})
	}
	node := BuildSession(SessionInput{Name: "s", Windows: windows})

	seen := make(map[string]bool, len(node.Children))
	for _, c := range node.Children {
		if seen[c.Title] {
			t.Fatalf("two rows read %q", c.Title)
		}
		seen[c.Title] = true
	}
	if got := node.Children[0].Title; got != "tuios 1" {
		t.Errorf("first row = %q, want %q", got, "tuios 1")
	}
	if got := node.Children[4].Title; got != "tuios 5" {
		t.Errorf("last row = %q, want %q", got, "tuios 5")
	}
}

// TestBuildSessionLeavesDistinctRowsAlone: an ordinal is a cost, paid only by
// the rows that need it.
func TestBuildSessionLeavesDistinctRowsAlone(t *testing.T) {
	node := BuildSession(SessionInput{Name: "s", Windows: []WindowInput{
		{ID: "a", Title: "nvim"},
		{ID: "b", Title: "tuios"},
		{ID: "c", Title: "tuios"},
	}})
	if got := node.Children[0].Title; got != "nvim" {
		t.Errorf("a unique row was renamed to %q", got)
	}
	if node.Children[1].Title == node.Children[2].Title {
		t.Errorf("the colliding rows still read %q", node.Children[1].Title)
	}
}
