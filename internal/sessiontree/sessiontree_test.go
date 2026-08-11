package sessiontree

import "testing"

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
	got := ""
	for _, s := range tree.Sessions {
		got += s.ID
	}
	if got != "abc" {
		t.Fatalf("session order = %q, want abc", got)
	}
}
