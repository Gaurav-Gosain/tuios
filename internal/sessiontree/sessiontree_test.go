package sessiontree

import (
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

// TestSessionTitleFallsBackToItsDirectory pins the label precedence: a name the
// user gave wins, the directory stands in when there is none, and the session
// name is the last resort. The branch rides the node whichever title won.
func TestSessionTitleFallsBackToItsDirectory(t *testing.T) {
	cases := []struct {
		in         SessionInput
		wantTitle  string
		wantBranch string
	}{
		{SessionInput{Name: "session-0", Dir: "repo", Branch: "main"}, "repo", "main"},
		{SessionInput{Name: "session-0", Dir: "repo", Branch: "main", DisplayName: "api"}, "api", "main"},
		{SessionInput{Name: "session-0", Branch: "main"}, "session-0", "main"},
		{SessionInput{Name: "session-0", Dir: "repo"}, "repo", ""},
	}
	for _, c := range cases {
		node := BuildSession(c.in)
		if node.Title != c.wantTitle {
			t.Errorf("%+v: Title = %q, want %q", c.in, node.Title, c.wantTitle)
		}
		if node.Branch != c.wantBranch {
			t.Errorf("%+v: Branch = %q, want %q", c.in, node.Branch, c.wantBranch)
		}
		if node.ID != "session-0" {
			t.Errorf("%+v: ID = %q, the label must never move the identity", c.in, node.ID)
		}
	}
}
